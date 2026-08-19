// Package evidence ranks bounded workspace and durable-history evidence.
package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Viking602/azem/internal/session"
)

const (
	MaxIndexPaths   = 4096
	MaxIndexedBytes = 256 << 10
)

type HistorySearch func(context.Context, string, string, int, int, int) ([]session.HistoryRecord, error)

type RepositorySignalV1 struct {
	Path        string
	Diagnostics []string
	Touched     bool
	Related     []string
}

type CandidateV1 struct {
	Ref         session.SourceRefV1
	Authority   string
	Kind        string
	Path        string
	Module      string
	Symbols     []string
	Imports     []string
	Diagnostics []string
	Related     []string
	Touched     bool
	Content     string
}

type RankedEvidenceV1 struct {
	Candidate CandidateV1
	Score     int
	Features  map[string]int
}

type RetrieveInput struct {
	SessionID  string
	Query      string
	Limit      int
	ByteBudget int
	Candidates []CandidateV1
}

type Retriever struct {
	History HistorySearch
}

// IndexWorkspace reads only caller-supplied paths. It never walks the whole
// repository, and Go syntax is parsed only when a bounded candidate is a Go file.
func IndexWorkspace(workspace string, signals []RepositorySignalV1) ([]CandidateV1, error) {
	if len(signals) > MaxIndexPaths {
		return nil, fmt.Errorf("evidence: %d paths exceeds limit %d", len(signals), MaxIndexPaths)
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("evidence: resolve workspace root: %w", err)
	}
	result := make([]CandidateV1, 0, len(signals))
	seen := make(map[string]struct{}, len(signals))
	for _, signal := range signals {
		relative, _, err := safeWorkspacePath(root, signal.Path)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[relative]; exists {
			continue
		}
		seen[relative] = struct{}{}
		file, err := openWorkspaceFile(root, relative)
		if err != nil {
			return nil, fmt.Errorf("evidence: open %s: %w", relative, err)
		}
		info, statErr := file.Stat()
		if statErr != nil {
			_ = file.Close()
			return nil, fmt.Errorf("evidence: inspect %s: %w", relative, statErr)
		}
		if !info.Mode().IsRegular() {
			_ = file.Close()
			return nil, fmt.Errorf("evidence: %s is not a regular file", relative)
		}
		payload, readErr := io.ReadAll(io.LimitReader(file, MaxIndexedBytes+1))
		closeErr := file.Close()
		if readErr != nil {
			return nil, fmt.Errorf("evidence: read %s: %w", relative, readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("evidence: close %s: %w", relative, closeErr)
		}
		if len(payload) > MaxIndexedBytes {
			return nil, fmt.Errorf("evidence: %s exceeds %d-byte index limit", relative, MaxIndexedBytes)
		}
		digest := sha256.Sum256(payload)
		candidate := CandidateV1{
			Ref:       session.SourceRefV1{Kind: "workspace_file", ID: relative, SHA256: hex.EncodeToString(digest[:])},
			Authority: "workspace", Kind: repositoryKind(relative), Path: relative, Module: filepath.ToSlash(filepath.Dir(relative)),
			Diagnostics: append([]string(nil), signal.Diagnostics...), Related: append([]string(nil), signal.Related...),
			Touched: signal.Touched, Content: string(payload),
		}
		if filepath.Ext(relative) == ".go" {
			candidate.Symbols, candidate.Imports, candidate.Module = parseGoStructure(relative, payload, candidate.Module)
		}
		result = append(result, candidate)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

func (r Retriever) Retrieve(ctx context.Context, input RetrieveInput) ([]RankedEvidenceV1, error) {
	query := strings.TrimSpace(input.Query)
	if query == "" || input.ByteBudget <= 0 {
		return nil, nil
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 8
	}
	if limit > 50 {
		limit = 50
	}
	candidates := append([]CandidateV1(nil), input.Candidates...)
	if r.History != nil && input.SessionID != "" {
		history, err := r.History(ctx, input.SessionID, query, limit, input.ByteBudget/4, input.ByteBudget)
		if err != nil {
			return nil, err
		}
		for _, record := range history {
			content := record.Content
			if content == "" {
				content = record.Preview
			}
			candidates = append(candidates, CandidateV1{
				Ref:       session.SourceRefV1{Kind: "history_" + record.SourceType, ID: record.SourceID},
				Authority: "history", Kind: record.SourceType, Content: content,
			})
		}
	}
	tokens := queryTokens(query)
	ranked := make([]RankedEvidenceV1, 0, len(candidates))
	for _, candidate := range candidates {
		score, features := scoreCandidate(tokens, candidate)
		if score <= 0 {
			continue
		}
		ranked = append(ranked, RankedEvidenceV1{Candidate: candidate, Score: score, Features: features})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Score != ranked[j].Score {
			return ranked[i].Score > ranked[j].Score
		}
		leftWorkspace := ranked[i].Candidate.Authority == "workspace"
		rightWorkspace := ranked[j].Candidate.Authority == "workspace"
		if leftWorkspace != rightWorkspace {
			return leftWorkspace
		}
		return ranked[i].Candidate.Ref.Kind+"\x00"+ranked[i].Candidate.Ref.ID < ranked[j].Candidate.Ref.Kind+"\x00"+ranked[j].Candidate.Ref.ID
	})
	result := make([]RankedEvidenceV1, 0, min(limit, len(ranked)))
	used := 0
	for _, item := range ranked {
		remaining := input.ByteBudget - used
		if remaining <= 0 || len(result) == limit {
			break
		}
		if len(item.Candidate.Content) > remaining {
			item.Candidate.Content = truncateUTF8(item.Candidate.Content, remaining)
		}
		used += len(item.Candidate.Content)
		result = append(result, item)
	}
	return result, nil
}

func scoreCandidate(tokens []string, candidate CandidateV1) (int, map[string]int) {
	features := make(map[string]int)
	fields := map[string]string{
		"path": strings.ToLower(candidate.Path), "module": strings.ToLower(candidate.Module),
		"symbols": strings.ToLower(strings.Join(candidate.Symbols, " ")), "imports": strings.ToLower(strings.Join(candidate.Imports, " ")),
		"diagnostics": strings.ToLower(strings.Join(candidate.Diagnostics, " ")), "related": strings.ToLower(strings.Join(candidate.Related, " ")),
		"content": strings.ToLower(candidate.Content),
	}
	weights := map[string]int{"path": 7, "module": 5, "symbols": 10, "imports": 4, "diagnostics": 9, "related": 6, "content": 1}
	for _, queryToken := range tokens {
		for field, value := range fields {
			if strings.Contains(value, queryToken) {
				features[field] += weights[field]
			}
		}
	}
	if candidate.Touched && len(features) > 0 {
		features["touched"] = 3
	}
	if candidate.Authority == "workspace" && len(features) > 0 {
		features["workspace_authority"] = 2
	}
	total := 0
	for _, value := range features {
		total += value
	}
	return total, features
}

func parseGoStructure(path string, payload []byte, fallbackModule string) ([]string, []string, string) {
	parsed, err := parser.ParseFile(token.NewFileSet(), path, payload, parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, fallbackModule
	}
	symbols := make([]string, 0, len(parsed.Decls))
	for _, declaration := range parsed.Decls {
		switch value := declaration.(type) {
		case *ast.FuncDecl:
			symbols = append(symbols, value.Name.Name)
		case *ast.GenDecl:
			for _, spec := range value.Specs {
				switch named := spec.(type) {
				case *ast.TypeSpec:
					symbols = append(symbols, named.Name.Name)
				case *ast.ValueSpec:
					for _, name := range named.Names {
						symbols = append(symbols, name.Name)
					}
				}
			}
		}
	}
	imports := make([]string, 0, len(parsed.Imports))
	for _, imported := range parsed.Imports {
		if value, err := strconv.Unquote(imported.Path.Value); err == nil {
			imports = append(imports, value)
		}
	}
	sort.Strings(symbols)
	sort.Strings(imports)
	return symbols, imports, parsed.Name.Name
}

func safeWorkspacePath(root, path string) (string, string, error) {
	if strings.TrimSpace(path) == "" || filepath.IsAbs(path) {
		return "", "", fmt.Errorf("evidence: unsafe workspace path %q", path)
	}
	relative := filepath.Clean(path)
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("evidence: unsafe workspace path %q", path)
	}
	absolute := filepath.Join(root, relative)
	return filepath.ToSlash(relative), absolute, nil
}

func repositoryKind(path string) string {
	base := filepath.Base(path)
	switch base {
	case "go.mod", "go.sum", "package.json", "Makefile", "Cargo.toml", "pyproject.toml":
		return "build_config"
	}
	if strings.HasSuffix(path, "_test.go") || strings.Contains(filepath.ToSlash(path), "/test/") || strings.Contains(filepath.ToSlash(path), "/tests/") {
		return "test"
	}
	return "source"
}

func queryTokens(query string) []string {
	seen := make(map[string]struct{})
	var tokens []string
	for _, token := range strings.FieldsFunc(strings.ToLower(query), func(value rune) bool {
		return !unicode.IsLetter(value) && !unicode.IsDigit(value) && value != '_' && value != '/' && value != '.'
	}) {
		if len(token) < 2 {
			continue
		}
		if _, exists := seen[token]; exists {
			continue
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}
	return tokens
}

func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
