package maintenance

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type GCOptions struct {
	DB             *sql.DB
	BlobRoot       string
	AttachmentRoot string
	LogRoot        string
	MinimumAge     time.Duration
	LogRetention   time.Duration
	Apply          bool
	Vacuum         bool
	Now            func() time.Time
}

type GCCandidate struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Bytes   int64  `json:"bytes"`
	Removed bool   `json:"removed"`
}

type GCReport struct {
	Candidates   []GCCandidate `json:"candidates"`
	BytesReclaim int64         `json:"bytesReclaim"`
	Removed      int           `json:"removed"`
	Apply        bool          `json:"apply"`
	Vacuumed     bool          `json:"vacuumed"`
}

func Collect(ctx context.Context, options GCOptions) (GCReport, error) {
	if options.DB == nil {
		return GCReport{}, errors.New("garbage collection database is required")
	}
	if options.MinimumAge <= 0 {
		options.MinimumAge = 24 * time.Hour
	}
	if options.LogRetention <= 0 {
		options.LogRetention = 30 * 24 * time.Hour
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	report := GCReport{Apply: options.Apply}
	references, err := referencedBlobs(ctx, options.DB)
	if err != nil {
		return report, err
	}
	if err := collectBlobCandidates(ctx, &report, options, references); err != nil {
		return report, err
	}
	if err := collectAttachmentCandidates(ctx, &report, options); err != nil {
		return report, err
	}
	if err := collectLogCandidates(ctx, &report, options); err != nil {
		return report, err
	}
	if options.Apply && options.Vacuum {
		if _, err := options.DB.ExecContext(ctx, `PRAGMA optimize`); err != nil {
			return report, err
		}
		if _, err := options.DB.ExecContext(ctx, `VACUUM`); err != nil {
			return report, err
		}
		report.Vacuumed = true
	}
	return report, nil
}

func referencedBlobs(ctx context.Context, db *sql.DB) (map[string]struct{}, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT data_sha256 FROM records WHERE data_sha256<>''
		UNION SELECT data_sha256 FROM events WHERE data_sha256<>''
		UNION SELECT model_history_sha256 FROM session_projections WHERE model_history_sha256<>''
		UNION SELECT data_sha256 FROM session_blocks WHERE data_sha256<>''
		UNION SELECT sha256 FROM context_artifacts WHERE sha256<>''
		UNION SELECT content_sha256 FROM session_tool_records WHERE content_sha256<>''
		UNION SELECT structured_sha256 FROM session_tool_records WHERE structured_sha256<>''
		UNION SELECT transcript_sha256 FROM subagent_runs WHERE transcript_sha256<>''
		UNION SELECT output_sha256 FROM subagent_runs WHERE output_sha256<>''
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]struct{})
	for rows.Next() {
		var digest string
		if rows.Scan(&digest) == nil && validDigest(digest) {
			result[digest] = struct{}{}
		}
	}
	return result, rows.Err()
}

func collectBlobCandidates(ctx context.Context, report *GCReport, options GCOptions, references map[string]struct{}) error {
	root := strings.TrimSpace(options.BlobRoot)
	if root == "" {
		return nil
	}
	cutoff := options.Now().Add(-options.MinimumAge)
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.ModTime().After(cutoff) {
			return err
		}
		name := entry.Name()
		kind := "blob"
		candidate := false
		if strings.HasPrefix(name, ".blob-") || strings.HasPrefix(name, ".azem-") {
			kind, candidate = "blob-temp", true
		} else if validDigest(name) {
			_, referenced := references[name]
			candidate = !referenced
		}
		if !candidate {
			return nil
		}
		return reclaimFile(report, path, kind, info.Size(), options.Apply)
	})
}

func collectAttachmentCandidates(ctx context.Context, report *GCReport, options GCOptions) error {
	root := strings.TrimSpace(options.AttachmentRoot)
	if root == "" {
		return nil
	}
	rows, err := options.DB.QueryContext(ctx, `SELECT id FROM sessions`)
	if err != nil {
		return err
	}
	sessions := make(map[string]struct{})
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			sessions[id] = struct{}{}
		}
	}
	rows.Close()
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	cutoff := options.Now().Add(-options.MinimumAge)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if _, exists := sessions[entry.Name()]; exists {
			continue
		}
		path := filepath.Join(root, entry.Name())
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		bytes := directoryBytes(path)
		report.Candidates = append(report.Candidates, GCCandidate{Path: path, Kind: "attachment-session", Bytes: bytes, Removed: options.Apply})
		report.BytesReclaim += bytes
		if options.Apply {
			if err := os.RemoveAll(path); err != nil {
				return err
			}
			report.Removed++
		}
	}
	return nil
}

func collectLogCandidates(ctx context.Context, report *GCReport, options GCOptions) error {
	root := strings.TrimSpace(options.LogRoot)
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	cutoff := options.Now().Add(-options.LogRetention)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.ModTime().After(cutoff) {
			continue
		}
		if err := reclaimFile(report, filepath.Join(root, entry.Name()), "log", info.Size(), options.Apply); err != nil {
			return err
		}
	}
	return nil
}

func reclaimFile(report *GCReport, path, kind string, bytes int64, apply bool) error {
	report.Candidates = append(report.Candidates, GCCandidate{Path: path, Kind: kind, Bytes: bytes, Removed: apply})
	report.BytesReclaim += bytes
	if apply {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		report.Removed++
	}
	return nil
}

func validDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && strings.ToLower(value) == value
}

func directoryBytes(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if info, infoErr := entry.Info(); infoErr == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}
