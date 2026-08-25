package agent

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/Viking602/azem/internal/resource"
	"github.com/Viking602/venat/coding"
	"github.com/Viking602/venat/tool"
	"github.com/klauspost/compress/zstd"
)

const (
	maxOMPWriteBytes      = 16 << 20
	maxArchiveRewriteSize = 256 << 20
	maxArchiveEntries     = 10000
)

var (
	hashlineWriteHeader = regexp.MustCompile(`^\s*(?:¶[^#\r\n]+#[0-9A-Fa-f]{4}|\[[^#\r\n]+#[0-9A-Fa-f]{4}\])\s*$`)
	hashlineWriteLine   = regexp.MustCompile(`^\s*\d+:`)
	writeTempCounter    atomic.Uint64
)

type ompWriteDriver struct {
	root         string
	snapshotRead tool.Driver
	resources    *resource.Router
	broker       *fileMutationBrokerRef
}

type ompWriteInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type ompWriteResult struct {
	Path           string `json:"path"`
	Header         string `json:"header,omitempty"`
	Bytes          int    `json:"bytes"`
	Created        bool   `json:"created,omitempty"`
	Overwritten    bool   `json:"overwritten,omitempty"`
	MadeExecutable bool   `json:"madeExecutable,omitempty"`
	Kind           string `json:"kind"`
	Content        string `json:"content"`
}

func newOMPWriteDriver(root string, snapshotRead tool.Driver, resources *resource.Router, broker *fileMutationBrokerRef) tool.Driver {
	return &ompWriteDriver{root: root, snapshotRead: snapshotRead, resources: resources, broker: broker}
}

func (driver *ompWriteDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name:        coding.ToolWriteFile,
		Description: "Create or replace a file, registered internal resource, archive member, or SQLite row. Stage structural rewrites by writing JSON to xd://ast_edit, then finalize with one reason sentence to xd://resolve or xd://reject. Hashline display prefixes copied from read output are removed safely.",
		InputSchema: tool.Schema{
			Type: "object", Required: []string{"path", "content"}, AdditionalProperties: &additional,
			Properties: map[string]tool.Schema{
				"path":    {Type: "string", Description: "Workspace path, archive member, SQLite row, or writable scheme:// resource URI."},
				"content": {Type: "string", Description: "Complete file content or resource payload."},
			},
		},
		EffectType: tool.EffectWrite, RequiresActionTask: true, RiskLevel: "medium", PolicyTags: []string{"coding", "write", "workspace"}, Concurrency: tool.ConcurrencyExclusive, ConcurrencyGroup: "workspace-files",
	}
}

func (driver *ompWriteDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input ompWriteInput
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return writeError(call, fmt.Errorf("decode write arguments: %w", err)), nil
	}
	input.Path = unwrapWritePath(strings.TrimSpace(input.Path))
	if input.Path == "" {
		return writeError(call, errors.New("path is required")), nil
	}
	if len(input.Content) > maxOMPWriteBytes {
		return writeError(call, fmt.Errorf("content exceeds %d bytes", maxOMPWriteBytes)), nil
	}
	cleanContent, stripped := stripCopiedHashlines(input.Content)
	var result tool.Result
	var err error
	if strings.Contains(input.Path, "://") {
		result, err = driver.writeURI(ctx, call, input.Path, cleanContent, stripped)
	} else if archivePath, member, ok := splitArchiveMember(input.Path); ok {
		result, err = driver.writeArchive(ctx, call, archivePath, member, cleanContent, stripped)
	} else if databasePath, selector, ok := splitSQLiteSelector(input.Path); ok && selector != "sqlite_master" {
		result, err = driver.writeSQLite(ctx, call, databasePath, selector, cleanContent, stripped)
	} else {
		result, err = driver.writeLocal(ctx, call, input.Path, cleanContent, stripped)
	}
	if err != nil {
		return writeError(call, err), nil
	}
	return result, nil
}

func (driver *ompWriteDriver) writeURI(ctx context.Context, call tool.Call, path, content string, stripped bool) (tool.Result, error) {
	if driver.resources == nil {
		return tool.Result{}, errors.New("internal resources are unavailable")
	}
	base, selector := splitReadSelector(path, "")
	caller, _ := tool.CallerFromContext(ctx)
	written, err := driver.resources.Write(ctx, base, selector, resource.Scope{
		SessionID: caller.SessionID, RunID: caller.TeamRunID, Workspace: driver.root,
	}, resource.Result{URI: base, MediaType: "text/plain; charset=utf-8", Data: []byte(content)})
	if err != nil {
		return tool.Result{}, err
	}
	if written.Metadata["toolResult"] == "true" {
		structured, _ := json.Marshal(written)
		return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: string(written.Data), Structured: structured}, nil
	}
	metadata := map[string]string(nil)
	if stripped {
		metadata = map[string]string{"hashlines": "stripped"}
	}
	outputPath := base
	if written.URI != "" {
		outputPath = written.URI
	}
	return writeSuccess(call, outputPath, "resource", len(content), false, true, false, "", metadata, ""), nil
}

func (driver *ompWriteDriver) writeLocal(ctx context.Context, call tool.Call, path, content string, stripped bool) (tool.Result, error) {
	if err := rejectWriteSelectorMisfire(driver.root, path, content); err != nil {
		return tool.Result{}, err
	}
	relative, err := workspaceRelativePath(driver.root, path)
	if err != nil {
		return tool.Result{}, err
	}
	root, err := os.OpenRoot(driver.root)
	if err != nil {
		return tool.Result{}, err
	}
	defer root.Close()
	info, statErr := root.Stat(filepath.FromSlash(relative))
	exists := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return tool.Result{}, statErr
	}
	if exists && !info.Mode().IsRegular() {
		return tool.Result{}, fmt.Errorf("write target %q is not a regular file", path)
	}
	mode := os.FileMode(0o644)
	if exists {
		mode = info.Mode().Perm()
	}
	madeExecutable, err := writeRootedFile(ctx, root, filepath.FromSlash(relative), []byte(content), mode, exists, strings.HasPrefix(content, "#!"))
	if err != nil && permissionMutationError(err) {
		if broker := driver.broker.get(); broker != nil {
			if destination, resolveErr := brokerDestination(driver.root, relative, true); resolveErr == nil {
				handled, _ := broker.BrokerWrite(ctx, destination, []byte(content), err, brokerCallerSession(ctx))
				if handled {
					err = nil
					madeExecutable = false
				}
			}
		}
	}
	if err != nil {
		return tool.Result{}, err
	}
	header := driver.recordWriteSnapshot(ctx, relative)
	metadata := map[string]string(nil)
	if stripped {
		metadata = map[string]string{"hashlines": "stripped"}
	}
	return writeSuccess(call, relative, "file", len(content), !exists, exists, madeExecutable, header, metadata, ""), nil
}

func (driver *ompWriteDriver) recordWriteSnapshot(ctx context.Context, relative string) string {
	if driver.snapshotRead == nil {
		return ""
	}
	arguments, _ := json.Marshal(map[string]any{"path": relative, "startLine": 1, "endLine": 1})
	result, err := driver.snapshotRead.Execute(ctx, tool.Call{ID: "write-snapshot", Name: coding.ToolReadFile, Arguments: arguments}, nil)
	if err != nil || result.IsError {
		return ""
	}
	var observed coding.ReadFileToolResult
	if json.Unmarshal(result.Structured, &observed) == nil && observed.Tag != "" {
		return "[" + observed.Path + "#" + observed.Tag + "]"
	}
	return ""
}

func (driver *ompWriteDriver) writeArchive(ctx context.Context, call tool.Call, archivePath, member, content string, stripped bool) (tool.Result, error) {
	member, err := normalizeArchiveMember(member)
	if err != nil {
		return tool.Result{}, err
	}
	relative, err := workspaceRelativePath(driver.root, archivePath)
	if err != nil {
		return tool.Result{}, err
	}
	root, err := os.OpenRoot(driver.root)
	if err != nil {
		return tool.Result{}, err
	}
	defer root.Close()
	info, statErr := root.Stat(filepath.FromSlash(relative))
	exists := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return tool.Result{}, statErr
	}
	if exists && !info.Mode().IsRegular() {
		return tool.Result{}, fmt.Errorf("archive target %q is not a regular file", archivePath)
	}
	mode := os.FileMode(0o644)
	if exists {
		mode = info.Mode().Perm()
	}
	if err := rewriteArchiveEntry(ctx, root, filepath.FromSlash(relative), member, []byte(content), exists, mode); err != nil {
		return tool.Result{}, err
	}
	metadata := map[string]string(nil)
	if stripped {
		metadata = map[string]string{"hashlines": "stripped"}
	}
	return writeSuccess(call, relative+":"+member, "archive", len(content), !exists, exists, false, "", metadata, ""), nil
}

func (driver *ompWriteDriver) writeSQLite(ctx context.Context, call tool.Call, databasePath, selector, content string, stripped bool) (tool.Result, error) {
	absolute, relative, _, err := secureReadPath(driver.root, databasePath)
	if err != nil {
		return tool.Result{}, err
	}
	table, key, _ := strings.Cut(selector, ":")
	if !sqliteIdentifier.MatchString(table) {
		return tool.Result{}, errors.New("invalid SQLite table selector")
	}
	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(absolute))
	if err != nil {
		return tool.Result{}, err
	}
	defer database.Close()
	if _, err := database.ExecContext(ctx, "PRAGMA busy_timeout=3000"); err != nil {
		return tool.Result{}, err
	}
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return tool.Result{}, err
	}
	defer transaction.Rollback()
	columns, primaryKey, err := sqliteColumns(ctx, transaction, table)
	if err != nil {
		return tool.Result{}, err
	}
	trimmed := strings.TrimSpace(content)
	var summary string
	if trimmed == "" {
		if key == "" {
			return tool.Result{}, errors.New("SQLite deletes require a row key in the path")
		}
		query := `DELETE FROM "` + table + `" WHERE ` + sqliteKeyColumn(primaryKey) + `=?`
		outcome, err := transaction.ExecContext(ctx, query, key)
		if err != nil {
			return tool.Result{}, err
		}
		changed, _ := outcome.RowsAffected()
		summary = fmt.Sprintf("deleted %d row(s) from %s", changed, table)
	} else {
		values, err := decodeJSONObject([]byte(content))
		if err != nil {
			return tool.Result{}, fmt.Errorf("SQLite write content must be a JSON object: %w", err)
		}
		if err := validateSQLiteColumns(values, columns); err != nil {
			return tool.Result{}, err
		}
		if key == "" {
			if err := sqliteInsert(ctx, transaction, table, values); err != nil {
				return tool.Result{}, err
			}
			summary = "inserted row into " + table
		} else {
			changed, err := sqliteUpdate(ctx, transaction, table, key, primaryKey, values)
			if err != nil {
				return tool.Result{}, err
			}
			summary = fmt.Sprintf("updated %d row(s) in %s", changed, table)
		}
	}
	if err := transaction.Commit(); err != nil {
		return tool.Result{}, err
	}
	metadata := map[string]string{"summary": summary}
	if stripped {
		metadata["hashlines"] = "stripped"
	}
	return writeSuccess(call, relative+":"+selector, "sqlite", len(content), false, true, false, "", metadata, summary), nil
}

func writeSuccess(call tool.Call, path, kind string, bytes int, created, overwritten, executable bool, header string, metadata map[string]string, detail string) tool.Result {
	line := fmt.Sprintf("Successfully wrote %d bytes to %s", bytes, path)
	if detail != "" {
		line = detail
	}
	if header != "" {
		line = header + "\n" + line
	}
	if metadata["hashlines"] == "stripped" {
		line += "\nNote: auto-stripped hashline display prefixes from content before writing."
	}
	if executable {
		line += "\n[Notice: Made executable via chmod +x]"
	}
	structured, _ := json.Marshal(ompWriteResult{Path: path, Header: header, Bytes: bytes, Created: created, Overwritten: overwritten, MadeExecutable: executable, Kind: kind, Content: line})
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: line, Structured: structured}
}

func writeError(call tool.Call, err error) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "coding.write_file failed: " + err.Error(), IsError: true}
}

func unwrapWritePath(path string) string {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, "[") && strings.HasSuffix(path, "]") {
		inner := strings.TrimSuffix(strings.TrimPrefix(path, "["), "]")
		if marker := strings.LastIndex(inner, "#"); marker > 0 && len(inner)-marker == 5 {
			return inner[:marker]
		}
	}
	if strings.HasPrefix(path, "¶") {
		if marker := strings.LastIndex(path, "#"); marker > 1 && len(path)-marker == 5 {
			return path[1:marker]
		}
	}
	return path
}

func stripCopiedHashlines(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	header := -1
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if hashlineWriteHeader.MatchString(line) {
			header = index
		}
		break
	}
	if header < 0 {
		return content, false
	}
	output := make([]string, 0, len(lines)-1)
	output = append(output, lines[:header]...)
	for _, line := range lines[header+1:] {
		location := hashlineWriteLine.FindStringIndex(line)
		if location != nil {
			line = line[location[1]:]
		}
		output = append(output, line)
	}
	return strings.Join(output, "\n"), true
}

func rejectWriteSelectorMisfire(root, path, content string) error {
	if strings.Contains(path, ";") {
		parts := strings.Split(path, ";")
		allSelectors := len(parts) > 1
		for _, part := range parts {
			base, selector := splitReadSelector(strings.TrimSpace(part), "")
			if base == part || selector == "" {
				allSelectors = false
				break
			}
		}
		if allSelectors {
			if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path))); os.IsNotExist(err) {
				return errors.New("write target is a semicolon-joined list of read selectors, not one file")
			}
		}
	}
	if content != "" {
		return nil
	}
	base, selector := splitReadSelector(path, "")
	if selector == "" || base == path {
		return nil
	}
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path))); os.IsNotExist(err) {
		return fmt.Errorf("write target %q ends with read selector :%s; use coding.read_file", path, selector)
	}
	return nil
}

func workspaceRelativePath(root, input string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	candidate := filepath.Clean(filepath.FromSlash(input))
	if filepath.IsAbs(candidate) {
		candidate, err = filepath.Rel(root, candidate)
		if err != nil {
			return "", err
		}
	}
	if candidate == "." || candidate == ".." || strings.HasPrefix(candidate, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("write target %q escapes workspace", input)
	}
	return filepath.ToSlash(candidate), nil
}

func writeRootedFile(ctx context.Context, root *os.Root, path string, payload []byte, mode os.FileMode, exists, executable bool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	parent := filepath.Dir(path)
	if parent != "." {
		if err := root.MkdirAll(parent, 0o755); err != nil {
			return false, err
		}
	}
	targetMode := mode
	madeExecutable := executable && targetMode.Perm()&0o111 != 0o111
	if executable {
		targetMode |= 0o111
	}
	writePath := path
	var file *os.File
	var err error
	if exists {
		file, writePath, err = openRootTemp(root, parent, ".azem-write", targetMode)
	} else {
		file, err = root.OpenFile(writePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, targetMode.Perm())
	}
	if err != nil {
		return false, err
	}
	complete := false
	defer func() {
		_ = file.Close()
		if !complete {
			_ = root.Remove(writePath)
		}
	}()
	if err := file.Chmod(targetMode.Perm()); err != nil {
		return false, err
	}
	if _, err := file.Write(payload); err != nil {
		return false, err
	}
	if err := file.Sync(); err != nil {
		return false, err
	}
	if err := file.Close(); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if exists {
		if err := root.Rename(writePath, path); err != nil {
			return false, err
		}
	}
	complete = true
	return madeExecutable, nil
}

func normalizeArchiveMember(member string) (string, error) {
	member = filepath.ToSlash(strings.TrimSpace(member))
	if member == "" || strings.HasPrefix(member, "/") || strings.HasSuffix(member, "/") {
		return "", errors.New("archive write path must target one file")
	}
	parts := strings.Split(member, "/")
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return "", errors.New("archive member cannot contain '..'")
		}
		clean = append(clean, part)
	}
	if len(clean) == 0 {
		return "", errors.New("archive write path must target one file")
	}
	return strings.Join(clean, "/"), nil
}

func rewriteArchiveEntry(ctx context.Context, root *os.Root, path, member string, payload []byte, exists bool, mode os.FileMode) error {
	parent := filepath.Dir(path)
	if parent != "." {
		if err := root.MkdirAll(parent, 0o755); err != nil {
			return err
		}
	}
	output, temporaryPath, err := openRootTemp(root, parent, ".azem-archive", mode)
	if err != nil {
		return err
	}
	defer root.Remove(temporaryPath)
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".zip"), strings.HasSuffix(lower, ".jar"), strings.HasSuffix(lower, ".war"), strings.HasSuffix(lower, ".ear"), strings.HasSuffix(lower, ".apk"), strings.HasSuffix(lower, ".whl"):
		err = rewriteZip(ctx, root, path, output, member, payload, exists)
	case strings.HasSuffix(lower, ".tar"), strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"), strings.HasSuffix(lower, ".tar.zst"), strings.HasSuffix(lower, ".tzst"):
		compression := ""
		if strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") {
			compression = "gzip"
		} else if strings.HasSuffix(lower, ".tar.zst") || strings.HasSuffix(lower, ".tzst") {
			compression = "zstd"
		}
		err = rewriteTar(ctx, root, path, output, member, payload, exists, compression)
	case strings.HasSuffix(lower, ".asar"):
		err = rewriteAsar(ctx, root, path, output, member, payload, exists)
	default:
		_ = output.Close()
		return errors.New("writing this archive format is unsupported")
	}
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return root.Rename(temporaryPath, path)
}

func openRootTemp(root *os.Root, parent, prefix string, mode os.FileMode) (*os.File, string, error) {
	for attempt := 0; attempt < 16; attempt++ {
		name := filepath.Join(parent, fmt.Sprintf("%s-%d-%d", prefix, os.Getpid(), writeTempCounter.Add(1)))
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
		if err == nil {
			return file, name, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
	}
	return nil, "", errors.New("could not allocate a temporary workspace file")
}

func rewriteZip(ctx context.Context, root *os.Root, source string, output *os.File, target string, payload []byte, exists bool) error {
	writer := zip.NewWriter(output)
	complete := false
	defer func() {
		if !complete {
			_ = writer.Close()
			_ = output.Close()
		}
	}()
	var total int64
	count := 0
	if exists {
		sourceFile, err := root.Open(source)
		if err != nil {
			return err
		}
		defer sourceFile.Close()
		info, err := sourceFile.Stat()
		if err != nil {
			return err
		}
		archive, err := zip.NewReader(sourceFile, info.Size())
		if err != nil {
			return err
		}
		for _, entry := range archive.File {
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.Name == target {
				continue
			}
			count++
			if count > maxArchiveEntries {
				return errors.New("archive contains too many entries")
			}
			total += int64(entry.UncompressedSize64)
			if total > maxArchiveRewriteSize {
				return errors.New("archive expands beyond rewrite limit")
			}
			header := entry.FileHeader
			destinationEntry, err := writer.CreateHeader(&header)
			if err != nil {
				return err
			}
			sourceEntry, err := entry.Open()
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(destinationEntry, sourceEntry)
			closeErr := sourceEntry.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
	targetEntry, err := writer.Create(target)
	if err != nil {
		return err
	}
	if _, err := targetEntry.Write(payload); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	complete = true
	return nil
}

func rewriteTar(ctx context.Context, root *os.Root, source string, output *os.File, target string, payload []byte, exists bool, compression string) error {
	var sink io.Writer = output
	var gzipWriter *gzip.Writer
	var zstdWriter *zstd.Encoder
	var err error
	switch compression {
	case "gzip":
		gzipWriter = gzip.NewWriter(output)
		sink = gzipWriter
	case "zstd":
		zstdWriter, err = zstd.NewWriter(output)
		if err != nil {
			return err
		}
		sink = zstdWriter
	}
	writer := tar.NewWriter(sink)
	complete := false
	defer func() {
		if !complete {
			_ = writer.Close()
			if gzipWriter != nil {
				_ = gzipWriter.Close()
			}
			if zstdWriter != nil {
				_ = zstdWriter.Close()
			}
			_ = output.Close()
		}
	}()
	if exists {
		input, err := root.Open(source)
		if err != nil {
			return err
		}
		defer input.Close()
		var sourceReader io.Reader = input
		if compression == "gzip" {
			decompressor, err := gzip.NewReader(input)
			if err != nil {
				return err
			}
			defer decompressor.Close()
			sourceReader = decompressor
		} else if compression == "zstd" {
			decompressor, err := zstd.NewReader(input, zstd.WithDecoderMaxMemory(maxArchiveRewriteSize))
			if err != nil {
				return err
			}
			defer decompressor.Close()
			sourceReader = decompressor
		}
		reader := tar.NewReader(sourceReader)
		var total int64
		count := 0
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			header, nextErr := reader.Next()
			if nextErr == io.EOF {
				break
			}
			if nextErr != nil {
				return nextErr
			}
			count++
			if count > maxArchiveEntries {
				return errors.New("archive contains too many entries")
			}
			if header.Name == target {
				continue
			}
			total += header.Size
			if total > maxArchiveRewriteSize {
				return errors.New("archive expands beyond rewrite limit")
			}
			copyHeader := *header
			if err := writer.WriteHeader(&copyHeader); err != nil {
				return err
			}
			if _, err := io.CopyN(writer, reader, header.Size); err != nil {
				return err
			}
		}
	}
	if err := writer.WriteHeader(&tar.Header{Name: target, Mode: 0o644, Size: int64(len(payload))}); err != nil {
		return err
	}
	if _, err := writer.Write(payload); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if gzipWriter != nil {
		if err := gzipWriter.Close(); err != nil {
			return err
		}
	}
	if zstdWriter != nil {
		if err := zstdWriter.Close(); err != nil {
			return err
		}
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	complete = true
	return nil
}

func sqliteColumns(ctx context.Context, transaction *sql.Tx, table string) (map[string]bool, string, error) {
	rows, err := transaction.QueryContext(ctx, `PRAGMA table_info("`+table+`")`)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	columns := make(map[string]bool)
	primary := ""
	for rows.Next() {
		var index int
		var name, columnType string
		var notNull, primaryIndex int
		var defaultValue any
		if err := rows.Scan(&index, &name, &columnType, &notNull, &defaultValue, &primaryIndex); err != nil {
			return nil, "", err
		}
		columns[name] = true
		if primaryIndex == 1 {
			primary = name
		}
	}
	if len(columns) == 0 {
		return nil, "", fmt.Errorf("SQLite table %q does not exist", table)
	}
	return columns, primary, rows.Err()
}

func sqliteKeyColumn(primary string) string {
	if primary == "" {
		return "rowid"
	}
	return `"` + primary + `"`
}

func decodeJSONObject(payload []byte) (map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	value, err := decodeUniqueJSONValue(decoder, 0)
	if err != nil {
		return nil, err
	}
	values, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("expected object")
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, errors.New("unexpected trailing JSON")
		}
		return nil, err
	}
	return values, nil
}

func decodeUniqueJSONValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, errors.New("JSON nesting exceeds 64 levels")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delimiter {
	case '{':
		values := make(map[string]any)
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			name, ok := nameToken.(string)
			if !ok {
				return nil, errors.New("JSON object key is not a string")
			}
			if _, duplicate := values[name]; duplicate {
				return nil, fmt.Errorf("duplicate JSON object key %q", name)
			}
			value, err := decodeUniqueJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			values[name] = value
		}
		if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
			return nil, errors.New("unterminated JSON object")
		}
		return values, nil
	case '[':
		values := make([]any, 0)
		for decoder.More() {
			value, err := decodeUniqueJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		if end, err := decoder.Token(); err != nil || end != json.Delim(']') {
			return nil, errors.New("unterminated JSON array")
		}
		return values, nil
	default:
		return nil, errors.New("unexpected JSON delimiter")
	}
}

func validateSQLiteColumns(values map[string]any, available map[string]bool) error {
	if len(values) == 0 {
		return errors.New("SQLite row object must contain at least one column")
	}
	for column := range values {
		if !available[column] {
			return fmt.Errorf("SQLite column %q does not exist", column)
		}
	}
	return nil
}

func sqliteInsert(ctx context.Context, transaction *sql.Tx, table string, values map[string]any) error {
	columns := sortedMapKeys(values)
	quoted := make([]string, len(columns))
	placeholders := make([]string, len(columns))
	arguments := make([]any, len(columns))
	for index, column := range columns {
		quoted[index] = `"` + column + `"`
		placeholders[index] = "?"
		arguments[index] = values[column]
	}
	_, err := transaction.ExecContext(ctx, `INSERT INTO "`+table+`" (`+strings.Join(quoted, ",")+`) VALUES (`+strings.Join(placeholders, ",")+`)`, arguments...)
	return err
}

func sqliteUpdate(ctx context.Context, transaction *sql.Tx, table, key, primary string, values map[string]any) (int64, error) {
	columns := sortedMapKeys(values)
	assignments := make([]string, len(columns))
	arguments := make([]any, 0, len(columns)+1)
	for index, column := range columns {
		assignments[index] = `"` + column + `"=?`
		arguments = append(arguments, values[column])
	}
	arguments = append(arguments, key)
	outcome, err := transaction.ExecContext(ctx, `UPDATE "`+table+`" SET `+strings.Join(assignments, ",")+` WHERE `+sqliteKeyColumn(primary)+`=?`, arguments...)
	if err != nil {
		return 0, err
	}
	return outcome.RowsAffected()
}

func sortedMapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
