package agent

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Viking602/azem/internal/resource"
	"github.com/Viking602/venat/coding"
	"github.com/Viking602/venat/message"
	"github.com/Viking602/venat/tool"
	"github.com/klauspost/compress/zstd"
	_ "modernc.org/sqlite"
)

const (
	defaultOMPReadBytes = 1 << 20
	maxOMPReadBytes     = 16 << 20
)

type ompReadDriver struct {
	root          string
	delegate      tool.Driver
	resources     *resource.Router
	networkPolicy string
}

type ompReadInput struct {
	Path      string `json:"path"`
	Selector  string `json:"selector,omitempty"`
	StartLine int    `json:"startLine,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
	Offset    int    `json:"offset,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	MaxBytes  int    `json:"maxBytes,omitempty"`
}

type ompReadResult struct {
	Path      string            `json:"path"`
	Kind      string            `json:"kind"`
	MediaType string            `json:"mediaType,omitempty"`
	Selector  string            `json:"selector,omitempty"`
	Content   string            `json:"content,omitempty"`
	Bytes     int               `json:"bytes,omitempty"`
	Truncated bool              `json:"truncated,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

func newOMPReadDriver(root string, delegate tool.Driver, resources *resource.Router, networkPolicy string) tool.Driver {
	return &ompReadDriver{root: root, delegate: delegate, resources: resources, networkPolicy: networkPolicy}
}

func (driver *ompReadDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name:        coding.ToolReadFile,
		Description: "Read workspace files, directories, URLs, documents, archives, SQLite rows, notebooks, images, and registered internal resource URIs. Local text reads retain hashline edit anchors.",
		InputSchema: tool.Schema{
			Type: "object", Required: []string{"path"}, AdditionalProperties: &additional,
			Properties: map[string]tool.Schema{
				"path":      {Type: "string", Description: "Workspace path, URL, archive member, SQLite selector, or scheme:// resource URI."},
				"selector":  {Type: "string", Description: "raw, conflicts, N, N-M, or N+count."},
				"startLine": {Type: "integer", Description: "Legacy 1-based first line."},
				"endLine":   {Type: "integer", Description: "Legacy 1-based last line."},
				"offset":    {Type: "integer", Description: "1-based first line."},
				"limit":     {Type: "integer", Description: "Maximum lines from offset."},
				"maxBytes":  {Type: "integer", Description: "Maximum returned bytes, capped at 16 MiB."},
			},
		},
		EffectType: tool.EffectReadOnly, RiskLevel: "low", PolicyTags: []string{"coding", "read"},
	}
}

func (driver *ompReadDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input ompReadInput
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return readError(call, fmt.Errorf("decode read arguments: %w", err)), nil
	}
	input.Path = strings.TrimSpace(input.Path)
	if input.Path == "" {
		return readError(call, errors.New("path is required")), nil
	}
	input.MaxBytes = boundedReadBytes(input.MaxBytes)
	path, selector := splitReadSelector(input.Path, strings.TrimSpace(input.Selector))
	input.Path, input.Selector = path, selector
	if selector != "" && selector != "raw" && selector != "conflicts" {
		input.StartLine, input.EndLine = selectorLines(selector)
	}
	if input.Offset > 0 {
		input.StartLine = input.Offset
		if input.Limit > 0 {
			input.EndLine = input.Offset + input.Limit - 1
		}
	}

	var result tool.Result
	var err error
	if isHTTPURL(path) {
		result, err = driver.readURL(ctx, call, input)
	} else if strings.Contains(path, "://") {
		result, err = driver.readURI(ctx, call, input)
	} else if archivePath, member, ok := splitArchiveMember(path); ok {
		result, err = driver.readArchive(ctx, call, input, archivePath, member)
	} else if databasePath, query, ok := splitSQLiteSelector(path); ok {
		result, err = driver.readSQLite(ctx, call, input, databasePath, query)
	} else {
		result, err = driver.readLocal(ctx, call, input)
	}
	if err != nil {
		return readError(call, err), nil
	}
	return result, nil
}

func (driver *ompReadDriver) readURI(ctx context.Context, call tool.Call, input ompReadInput) (tool.Result, error) {
	if driver.resources == nil {
		return tool.Result{}, errors.New("internal resources are unavailable")
	}
	caller, _ := tool.CallerFromContext(ctx)
	result, err := driver.resources.Read(ctx, input.Path, input.Selector, resource.Scope{
		SessionID: caller.SessionID, RunID: caller.TeamRunID, Workspace: driver.root,
	})
	if err != nil {
		return tool.Result{}, err
	}
	return readBytesResult(call, input.Path, input.Selector, "resource", result.MediaType, result.Data, input.MaxBytes, result.Metadata), nil
}

func (driver *ompReadDriver) readURL(ctx context.Context, call tool.Call, input ompReadInput) (tool.Result, error) {
	if driver.networkPolicy != "allow" {
		return tool.Result{}, errors.New("URL reads require workspace network policy allow")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, input.Path, nil)
	if err != nil {
		return tool.Result{}, err
	}
	request.Header.Set("Accept", "text/plain,text/markdown,text/html,application/json,application/pdf;q=0.9,*/*;q=0.1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return tool.Result{}, err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return tool.Result{}, fmt.Errorf("URL returned HTTP %d", response.StatusCode)
	}
	payload, truncated, err := readLimited(response.Body, input.MaxBytes)
	if err != nil {
		return tool.Result{}, err
	}
	mediaType := strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0])
	if mediaType == "text/html" {
		payload = []byte(htmlToText(string(payload)))
	}
	return readBytesResult(call, input.Path, input.Selector, "url", mediaType, payload, input.MaxBytes, map[string]string{
		"status": strconv.Itoa(response.StatusCode), "truncated": strconv.FormatBool(truncated),
	}), nil
}

func (driver *ompReadDriver) readLocal(ctx context.Context, call tool.Call, input ompReadInput) (tool.Result, error) {
	absolute, relative, info, err := secureReadPath(driver.root, input.Path)
	if err != nil {
		return tool.Result{}, err
	}
	if info.IsDir() {
		entries, err := os.ReadDir(absolute)
		if err != nil {
			return tool.Result{}, err
		}
		lines := make([]string, 0, len(entries))
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() {
				name += "/"
			}
			lines = append(lines, name)
		}
		sort.Strings(lines)
		return readTextResult(call, relative, input.Selector, "directory", strings.Join(lines, "\n"), false, nil), nil
	}
	extension := strings.ToLower(filepath.Ext(relative))
	switch extension {
	case ".ipynb":
		payload, truncated, err := readFileLimited(absolute, input.MaxBytes)
		if err != nil {
			return tool.Result{}, err
		}
		text, err := notebookText(payload)
		if err != nil {
			return tool.Result{}, err
		}
		return readTextResult(call, relative, input.Selector, "notebook", applyTextSelector(text, input), truncated, nil), nil
	case ".docx":
		text, err := docxText(absolute, input.MaxBytes)
		if err != nil {
			return tool.Result{}, err
		}
		return readTextResult(call, relative, input.Selector, "document", applyTextSelector(text, input), false, nil), nil
	case ".pdf":
		text, err := pdfText(ctx, absolute, input.MaxBytes)
		if err != nil {
			return tool.Result{}, err
		}
		return readTextResult(call, relative, input.Selector, "document", applyTextSelector(text, input), false, nil), nil
	}
	mediaType := mime.TypeByExtension(extension)
	if strings.HasPrefix(mediaType, "image/") {
		payload, truncated, err := readFileLimited(absolute, input.MaxBytes)
		if err != nil {
			return tool.Result{}, err
		}
		if truncated {
			return tool.Result{}, errors.New("image exceeds read limit")
		}
		content := fmt.Sprintf("Image %s (%s, %d bytes)", relative, mediaType, len(payload))
		structured, _ := json.Marshal(ompReadResult{Path: relative, Kind: "image", MediaType: mediaType, Content: content, Bytes: len(payload)})
		result := tool.Result{ToolCallID: call.ID, Name: call.Name, Content: content, Structured: structured}
		result.Parts = []message.ContentPart{{Kind: message.ContentImage, Data: payload, MediaType: mediaType, Filename: filepath.Base(relative)}}
		return result, nil
	}
	if input.Selector == "conflicts" {
		payload, truncated, err := readFileLimited(absolute, input.MaxBytes)
		if err != nil {
			return tool.Result{}, err
		}
		return readTextResult(call, relative, input.Selector, "conflicts", conflictBlocks(string(payload)), truncated, nil), nil
	}
	if input.Selector == "raw" {
		payload, truncated, err := readFileLimited(absolute, input.MaxBytes)
		if err != nil {
			return tool.Result{}, err
		}
		return readTextResult(call, relative, input.Selector, "text", string(payload), truncated, nil), nil
	}
	if input.StartLine > 0 || input.EndLine > 0 {
		return driver.delegateText(ctx, call, input, relative)
	}
	if summary, ok, err := structuralSummary(absolute, relative, input.MaxBytes); err != nil {
		return tool.Result{}, err
	} else if ok {
		// Record the full file in the delegate snapshot store before returning the summary.
		if _, err := driver.delegateText(ctx, call, ompReadInput{Path: relative, StartLine: 1, EndLine: 1, MaxBytes: input.MaxBytes}, relative); err != nil {
			return tool.Result{}, err
		}
		return readTextResult(call, relative, input.Selector, "structure", summary, false, map[string]string{"hint": "pass selector or line range for source"}), nil
	}
	return driver.delegateText(ctx, call, input, relative)
}

func (driver *ompReadDriver) delegateText(ctx context.Context, call tool.Call, input ompReadInput, relative string) (tool.Result, error) {
	arguments, _ := json.Marshal(map[string]any{
		"path": relative, "startLine": input.StartLine, "endLine": input.EndLine, "maxBytes": input.MaxBytes,
	})
	delegated := call
	delegated.Arguments = arguments
	result, err := driver.delegate.Execute(ctx, delegated, nil)
	if err != nil {
		return tool.Result{}, err
	}
	if !result.IsError {
		var observed coding.ReadFileToolResult
		if json.Unmarshal(result.Structured, &observed) == nil && observed.Path != "" && observed.Tag != "" {
			oldHeader := observed.Header
			observed.Header = "[" + observed.Path + "#" + observed.Tag + "]"
			if strings.HasPrefix(observed.Content, oldHeader) {
				observed.Content = observed.Header + strings.TrimPrefix(observed.Content, oldHeader)
			}
			result.Content = observed.Content
			result.Structured, _ = json.Marshal(observed)
		}
	}
	return result, nil
}

func (driver *ompReadDriver) readArchive(ctx context.Context, call tool.Call, input ompReadInput, archivePath, member string) (tool.Result, error) {
	absolute, relative, info, err := secureReadPath(driver.root, archivePath)
	if err != nil {
		return tool.Result{}, err
	}
	if info.IsDir() {
		return tool.Result{}, errors.New("archive path is a directory")
	}
	payload, err := archiveMember(ctx, absolute, member, input.MaxBytes)
	if err != nil {
		return tool.Result{}, err
	}
	return readBytesResult(call, relative+":"+member, input.Selector, "archive", mime.TypeByExtension(filepath.Ext(member)), payload, input.MaxBytes, map[string]string{"archive": relative, "member": member}), nil
}

func (driver *ompReadDriver) readSQLite(ctx context.Context, call tool.Call, input ompReadInput, databasePath, selector string) (tool.Result, error) {
	absolute, relative, _, err := secureReadPath(driver.root, databasePath)
	if err != nil {
		return tool.Result{}, err
	}
	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(absolute)+"?mode=ro")
	if err != nil {
		return tool.Result{}, err
	}
	defer database.Close()
	query, args, err := sqliteQuery(ctx, database, selector)
	if err != nil {
		return tool.Result{}, err
	}
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return tool.Result{}, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return tool.Result{}, err
	}
	output := make([]map[string]any, 0)
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return tool.Result{}, err
		}
		item := make(map[string]any, len(columns))
		for index, column := range columns {
			if raw, ok := values[index].([]byte); ok {
				item[column] = string(raw)
			} else {
				item[column] = values[index]
			}
		}
		output = append(output, item)
		if len(output) >= 100 {
			break
		}
	}
	encoded, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return tool.Result{}, err
	}
	return readTextResult(call, relative+":"+selector, input.Selector, "sqlite", string(encoded), false, map[string]string{"rows": strconv.Itoa(len(output))}), nil
}

func boundedReadBytes(value int) int {
	if value <= 0 {
		return defaultOMPReadBytes
	}
	return min(value, maxOMPReadBytes)
}

func readBytesResult(call tool.Call, path, selector, kind, mediaType string, payload []byte, limit int, metadata map[string]string) tool.Result {
	truncated := len(payload) > limit
	if truncated {
		payload = payload[:limit]
	}
	if utf8.Valid(payload) || strings.HasPrefix(mediaType, "text/") || mediaType == "application/json" {
		return readTextResult(call, path, selector, kind, string(payload), truncated, metadata)
	}
	content := fmt.Sprintf("Binary %s (%s, %d bytes)", path, mediaType, len(payload))
	structured, _ := json.Marshal(ompReadResult{Path: path, Kind: kind, MediaType: mediaType, Content: content, Bytes: len(payload), Truncated: truncated, Metadata: metadata})
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: content, Structured: structured}
}

func readTextResult(call tool.Call, path, selector, kind, content string, truncated bool, metadata map[string]string) tool.Result {
	structured, _ := json.Marshal(ompReadResult{Path: path, Kind: kind, Selector: selector, Content: content, Bytes: len(content), Truncated: truncated, Metadata: metadata})
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: content, Structured: structured}
}

func readError(call tool.Call, err error) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "coding.read_file failed: " + err.Error(), IsError: true}
}

func splitReadSelector(path, explicit string) (string, string) {
	if explicit != "" || isHTTPURL(path) {
		return path, explicit
	}
	index := strings.LastIndex(path, ":")
	if index <= 0 {
		return path, explicit
	}
	if scheme := strings.Index(path, "://"); scheme >= 0 {
		pathStart := strings.Index(path[scheme+3:], "/")
		if pathStart < 0 || index <= scheme+3+pathStart {
			return path, explicit
		}
	}
	candidate := path[index+1:]
	if candidate == "raw" || candidate == "conflicts" || regexp.MustCompile(`^\d+(?:-\d+|\+\d+)?$`).MatchString(candidate) {
		return path[:index], candidate
	}
	return path, explicit
}

func applyTextSelector(text string, input ompReadInput) string {
	if input.Selector == "raw" || input.Selector == "" && input.StartLine == 0 && input.EndLine == 0 {
		return text
	}
	start, end := selectorLines(input.Selector)
	if input.StartLine > 0 {
		start = input.StartLine
	}
	if input.EndLine > 0 {
		end = input.EndLine
	}
	lines := strings.Split(text, "\n")
	if start <= 0 {
		start = 1
	}
	if end <= 0 || end > len(lines) {
		end = len(lines)
	}
	if start > end || start > len(lines) {
		return ""
	}
	return strings.Join(lines[start-1:end], "\n")
}

func selectorLines(selector string) (int, int) {
	if strings.Contains(selector, "+") {
		parts := strings.SplitN(selector, "+", 2)
		start, _ := strconv.Atoi(parts[0])
		count, _ := strconv.Atoi(parts[1])
		return start, start + count - 1
	}
	if strings.Contains(selector, "-") {
		parts := strings.SplitN(selector, "-", 2)
		start, _ := strconv.Atoi(parts[0])
		end, _ := strconv.Atoi(parts[1])
		return start, end
	}
	line, _ := strconv.Atoi(selector)
	return line, line
}

func conflictBlocks(content string) string {
	lines := strings.Split(content, "\n")
	var blocks []string
	for index := 0; index < len(lines); index++ {
		if !strings.HasPrefix(lines[index], "<<<<<<<") {
			continue
		}
		start := index
		for index < len(lines) && !strings.HasPrefix(lines[index], ">>>>>>>") {
			index++
		}
		if index < len(lines) {
			blocks = append(blocks, strings.Join(lines[start:index+1], "\n"))
		}
	}
	return strings.Join(blocks, "\n\n")
}

func secureReadPath(root, input string) (string, string, os.FileInfo, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", "", nil, err
	}
	target := input
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, filepath.FromSlash(input))
	}
	target = filepath.Clean(target)
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", nil, err
	}
	realTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", "", nil, err
	}
	rel, err := filepath.Rel(realRoot, realTarget)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", nil, errors.New("path escapes workspace")
	}
	info, err := os.Stat(realTarget)
	if err != nil {
		return "", "", nil, err
	}
	return realTarget, filepath.ToSlash(rel), info, nil
}

func isHTTPURL(path string) bool {
	parsed, err := url.Parse(path)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func splitArchiveMember(path string) (string, string, bool) {
	lower := strings.ToLower(path)
	for _, extension := range []string{".tar.gz", ".tar.bz2", ".tar.xz", ".tar.zst", ".tgz", ".tbz2", ".txz", ".tzst", ".tar", ".zip", ".jar", ".war", ".ear", ".apk", ".whl", ".asar", ".rar", ".7z", ".iso", ".cab", ".deb", ".rpm", ".cpio", ".arj", ".lzh", ".gz", ".bz2", ".xz", ".zst", ".ar", ".a"} {
		marker := strings.Index(lower, extension+":")
		if marker >= 0 {
			end := marker + len(extension)
			return path[:end], strings.TrimPrefix(path[end+1:], "/"), true
		}
	}
	return "", "", false
}

func archiveMember(ctx context.Context, archivePath, member string, limit int) ([]byte, error) {
	lower := strings.ToLower(archivePath)
	switch {
	case strings.HasSuffix(lower, ".zip"), strings.HasSuffix(lower, ".jar"), strings.HasSuffix(lower, ".war"), strings.HasSuffix(lower, ".ear"), strings.HasSuffix(lower, ".apk"), strings.HasSuffix(lower, ".whl"):
		archive, err := zip.OpenReader(archivePath)
		if err != nil {
			return nil, err
		}
		defer archive.Close()
		for _, file := range archive.File {
			if filepath.ToSlash(file.Name) != member {
				continue
			}
			reader, err := file.Open()
			if err != nil {
				return nil, err
			}
			defer reader.Close()
			payload, _, err := readLimited(reader, limit)
			return payload, err
		}
		return nil, os.ErrNotExist
	case strings.HasSuffix(lower, ".asar"):
		return readAsarMember(archivePath, member, limit)
	case strings.HasSuffix(lower, ".tar"), strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"), strings.HasSuffix(lower, ".tar.zst"), strings.HasSuffix(lower, ".tzst"):
		file, err := os.Open(archivePath)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		var reader io.Reader = file
		if strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") {
			compressed, err := gzip.NewReader(file)
			if err != nil {
				return nil, err
			}
			defer compressed.Close()
			reader = compressed
		} else if strings.HasSuffix(lower, ".tar.zst") || strings.HasSuffix(lower, ".tzst") {
			compressed, err := zstd.NewReader(file, zstd.WithDecoderMaxMemory(maxArchiveRewriteSize))
			if err != nil {
				return nil, err
			}
			defer compressed.Close()
			reader = compressed
		}
		tarReader := tar.NewReader(reader)
		for {
			header, err := tarReader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if filepath.ToSlash(header.Name) == member {
				payload, _, err := readLimited(tarReader, limit)
				return payload, err
			}
		}
		return nil, os.ErrNotExist
	case strings.HasSuffix(lower, ".gz"):
		file, err := os.Open(archivePath)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		reader, err := gzip.NewReader(file)
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		payload, _, err := readLimited(reader, limit)
		return payload, err
	case strings.HasSuffix(lower, ".zst"):
		file, err := os.Open(archivePath)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		reader, err := zstd.NewReader(file, zstd.WithDecoderMaxMemory(maxArchiveRewriteSize))
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		payload, _, err := readLimited(reader, limit)
		return payload, err
	default:
		return archiveMemberWithBSdtar(ctx, archivePath, member, limit)
	}
}

func archiveMemberWithBSdtar(ctx context.Context, archivePath, member string, limit int) ([]byte, error) {
	binary, err := exec.LookPath("bsdtar")
	if err != nil {
		return nil, errors.New("archive format requires bsdtar")
	}
	command := exec.CommandContext(ctx, binary, "-xOf", archivePath, member)
	var output limitedBuffer
	output.limit = limit
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("bsdtar: %w: %s", err, output.String())
	}
	return output.Bytes(), nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (buffer *limitedBuffer) Write(payload []byte) (int, error) {
	remaining := buffer.limit - buffer.Len()
	if remaining <= 0 {
		return len(payload), nil
	}
	_, _ = buffer.Buffer.Write(payload[:min(len(payload), remaining)])
	return len(payload), nil
}

func splitSQLiteSelector(path string) (string, string, bool) {
	lower := strings.ToLower(path)
	for _, extension := range []string{".sqlite3", ".sqlite", ".db3", ".db"} {
		marker := strings.Index(lower, extension)
		if marker < 0 {
			continue
		}
		end := marker + len(extension)
		if len(path) == end {
			return path, "sqlite_master", true
		}
		if path[end] == ':' {
			return path[:end], path[end+1:], true
		}
	}
	return "", "", false
}

var sqliteIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func sqliteQuery(ctx context.Context, database *sql.DB, selector string) (string, []any, error) {
	selector = strings.TrimSpace(selector)
	if strings.HasPrefix(selector, "?") {
		values, err := url.ParseQuery(strings.TrimPrefix(selector, "?"))
		if err != nil {
			return "", nil, err
		}
		query := strings.TrimSpace(values.Get("q"))
		if !strings.HasPrefix(strings.ToUpper(query), "SELECT ") && !strings.HasPrefix(strings.ToUpper(query), "PRAGMA ") {
			return "", nil, errors.New("SQLite resource query must be SELECT or PRAGMA")
		}
		return query, nil, nil
	}
	table, key, _ := strings.Cut(selector, ":")
	if !sqliteIdentifier.MatchString(table) {
		return "", nil, errors.New("invalid SQLite table selector")
	}
	if key == "" {
		return `SELECT * FROM "` + table + `" LIMIT 100`, nil, nil
	}
	primary, err := sqlitePrimaryKey(ctx, database, table)
	if err != nil {
		return "", nil, err
	}
	return `SELECT * FROM "` + table + `" WHERE ` + sqliteKeyColumn(primary) + `=? LIMIT 1`, []any{key}, nil
}

func sqlitePrimaryKey(ctx context.Context, database *sql.DB, table string) (string, error) {
	rows, err := database.QueryContext(ctx, `PRAGMA table_info("`+table+`")`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	primary := ""
	found := false
	for rows.Next() {
		var index int
		var name, columnType string
		var notNull, primaryIndex int
		var defaultValue any
		if err := rows.Scan(&index, &name, &columnType, &notNull, &defaultValue, &primaryIndex); err != nil {
			return "", err
		}
		found = true
		if primaryIndex == 1 {
			primary = name
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("SQLite table %q does not exist", table)
	}
	return primary, nil
}

func notebookText(payload []byte) (string, error) {
	var notebook struct {
		Cells []struct {
			CellType string   `json:"cell_type"`
			Source   []string `json:"source"`
			Outputs  []struct {
				Text []string `json:"text"`
			} `json:"outputs"`
		} `json:"cells"`
	}
	if err := json.Unmarshal(payload, &notebook); err != nil {
		return "", fmt.Errorf("decode notebook: %w", err)
	}
	var output strings.Builder
	for index, cell := range notebook.Cells {
		fmt.Fprintf(&output, "## Cell %d (%s)\n", index+1, cell.CellType)
		source := strings.Join(cell.Source, "")
		output.WriteString(source)
		if !strings.HasSuffix(source, "\n") {
			output.WriteByte('\n')
		}
		for _, cellOutput := range cell.Outputs {
			output.WriteString(strings.Join(cellOutput.Text, ""))
		}
	}
	return output.String(), nil
}

func docxText(path string, limit int) (string, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer archive.Close()
	for _, file := range archive.File {
		if file.Name != "word/document.xml" {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return "", err
		}
		defer reader.Close()
		payload, _, err := readLimited(reader, limit)
		if err != nil {
			return "", err
		}
		decoder := xml.NewDecoder(bytes.NewReader(payload))
		var output strings.Builder
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", err
			}
			switch current := token.(type) {
			case xml.CharData:
				output.Write([]byte(current))
			case xml.EndElement:
				if current.Name.Local == "p" || current.Name.Local == "tr" {
					output.WriteByte('\n')
				}
			}
		}
		return output.String(), nil
	}
	return "", errors.New("DOCX document.xml is missing")
}

func pdfText(ctx context.Context, path string, limit int) (string, error) {
	binary, err := exec.LookPath("pdftotext")
	if err != nil {
		return "", errors.New("PDF reading requires pdftotext (Poppler)")
	}
	command := exec.CommandContext(ctx, binary, "-layout", path, "-")
	var output limitedBuffer
	output.limit = limit
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("pdftotext: %w: %s", err, output.String())
	}
	return output.String(), nil
}

func structuralSummary(absolute, relative string, limit int) (string, bool, error) {
	extension := strings.ToLower(filepath.Ext(relative))
	payload, _, err := readFileLimited(absolute, limit)
	if err != nil {
		return "", false, err
	}
	switch extension {
	case ".go":
		parsed, err := parser.ParseFile(token.NewFileSet(), absolute, payload, parser.SkipObjectResolution)
		if err != nil {
			return "", false, nil
		}
		var lines []string
		for _, declaration := range parsed.Decls {
			switch current := declaration.(type) {
			case *ast.FuncDecl:
				kind := "func"
				if current.Recv != nil {
					kind = "method"
				}
				lines = append(lines, kind+" "+current.Name.Name)
			case *ast.GenDecl:
				for _, spec := range current.Specs {
					switch named := spec.(type) {
					case *ast.TypeSpec:
						lines = append(lines, "type "+named.Name.Name)
					case *ast.ValueSpec:
						for _, name := range named.Names {
							lines = append(lines, strings.ToLower(current.Tok.String())+" "+name.Name)
						}
					}
				}
			}
		}
		return strings.Join(lines, "\n"), len(lines) > 0, nil
	case ".ts", ".tsx", ".js", ".jsx", ".py", ".rs", ".java", ".swift":
		pattern := regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:async\s+)?(?:class|interface|type|enum|struct|trait|func|function|def)\s+[A-Za-z_$][A-Za-z0-9_$]*[^\n]*`)
		matches := pattern.FindAllString(string(payload), 256)
		for index := range matches {
			matches[index] = strings.TrimSpace(matches[index])
		}
		return strings.Join(matches, "\n"), len(matches) > 0, nil
	default:
		return "", false, nil
	}
}

func htmlToText(input string) string {
	input = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>|<style[^>]*>.*?</style>`).ReplaceAllString(input, "")
	input = regexp.MustCompile(`(?i)<br\s*/?>|</p>|</div>|</li>|</h[1-6]>`).ReplaceAllString(input, "\n")
	input = regexp.MustCompile(`(?s)<[^>]+>`).ReplaceAllString(input, "")
	replacements := strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&", "&quot;", `"`, "&#39;", "'")
	return strings.TrimSpace(replacements.Replace(input))
}

func readFileLimited(path string, limit int) ([]byte, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	return readLimited(file, limit)
}

func readLimited(reader io.Reader, limit int) ([]byte, bool, error) {
	payload, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return nil, false, err
	}
	if len(payload) > limit {
		return payload[:limit], true, nil
	}
	return payload, false, nil
}
