package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Viking602/azem/internal/resource"
)

const (
	maxASTEditFiles       = 1000
	maxASTProposalBytes   = 64 << 20
	maxASTRewriteTemplate = 64 << 10
	astProposalLifetime   = 30 * time.Minute
)

type astEditOp struct {
	Pattern string `json:"pat"`
	Output  string `json:"out"`
}

type astEditInput struct {
	Ops   []astEditOp `json:"ops"`
	Paths []string    `json:"paths"`
}

type astProposalFile struct {
	Path        string
	ResourceURI string
	Original    []byte
	Proposed    []byte
	Mode        os.FileMode
	Count       int
	FirstLine   int
}

type astProposal struct {
	ID            string
	ScopeKey      string
	Workspace     string
	CreatedAt     time.Time
	Files         map[string]*astProposalFile
	Replacements  int
	FilesSearched int
	ParseErrors   []string
}

type astXDevHandler struct {
	bridge    *astBridge
	resources *resource.Router
	mu        sync.Mutex
	pending   map[string]*astProposal
	sequence  atomic.Uint64
}

func newASTXDevHandler(bridge *astBridge, resources *resource.Router) *astXDevHandler {
	if bridge == nil {
		bridge = newASTBridge()
	}
	return &astXDevHandler{bridge: bridge, resources: resources, pending: make(map[string]*astProposal)}
}

func (handler *astXDevHandler) Read(_ context.Context, request resource.Request) (resource.Result, error) {
	device := strings.Trim(request.URI.Opaque, "/")
	switch device {
	case "ast_edit":
		schema := `{"ops":[{"pat":"console.log($$$)","out":"logger.info($$$)"}],"paths":["src/**/*.ts"]}`
		return resource.Result{URI: request.URI.Raw, MediaType: "application/json", Data: []byte(schema), Metadata: map[string]string{"operation": "stage"}}, nil
	case "resolve", "reject":
		handler.mu.Lock()
		proposal := handler.pending[astProposalScope(request.Scope)]
		handler.mu.Unlock()
		if proposal == nil {
			return resource.Result{URI: request.URI.Raw, MediaType: "text/plain", Data: []byte("No staged AST proposal.")}, nil
		}
		return resource.Result{URI: request.URI.Raw, MediaType: "text/plain", Data: []byte(renderASTProposalSummary(proposal, true))}, nil
	default:
		return resource.Result{}, fmt.Errorf("%w: xd://%s", resource.ErrUnsupported, device)
	}
}

func (handler *astXDevHandler) Write(ctx context.Context, request resource.Request, value resource.Result) (resource.Result, error) {
	device := strings.Trim(request.URI.Opaque, "/")
	switch device {
	case "ast_edit":
		return handler.stage(ctx, request, value.Data)
	case "resolve":
		return handler.resolve(ctx, request, string(value.Data))
	case "reject":
		return handler.reject(request, string(value.Data))
	default:
		return resource.Result{}, fmt.Errorf("%w: xd://%s", resource.ErrUnsupported, device)
	}
}

func (handler *astXDevHandler) List(_ context.Context, request resource.Request) ([]resource.Result, error) {
	return []resource.Result{
		{URI: "xd://ast_edit", MediaType: "application/json", Metadata: map[string]string{"operation": "write"}},
		{URI: "xd://resolve", MediaType: "text/plain", Metadata: map[string]string{"operation": "write"}},
		{URI: "xd://reject", MediaType: "text/plain", Metadata: map[string]string{"operation": "write"}},
	}, nil
}

func (handler *astXDevHandler) stage(ctx context.Context, request resource.Request, payload []byte) (resource.Result, error) {
	var input astEditInput
	if err := json.Unmarshal(payload, &input); err != nil {
		return resource.Result{}, fmt.Errorf("decode ast_edit input: %w", err)
	}
	if len(input.Ops) == 0 || len(input.Paths) == 0 {
		return resource.Result{}, errors.New("ast_edit requires non-empty ops and paths")
	}
	rewrites := make(map[string]string, len(input.Ops))
	for index, operation := range input.Ops {
		if operation.Pattern == "" {
			return resource.Result{}, fmt.Errorf("ops[%d].pat is empty", index)
		}
		if err := validateASTPattern(operation.Pattern); err != nil {
			return resource.Result{}, fmt.Errorf("ops[%d]: %w", index, err)
		}
		if len(operation.Pattern) > maxASTPatternBytes || len(operation.Output) > maxASTRewriteTemplate {
			return resource.Result{}, fmt.Errorf("ops[%d] exceeds the 64 KiB pattern/template limit", index)
		}
		if _, duplicate := rewrites[operation.Pattern]; duplicate {
			return resource.Result{}, fmt.Errorf("duplicate rewrite pattern %q", operation.Pattern)
		}
		rewrites[operation.Pattern] = operation.Output
	}
	scopeKey := astProposalScope(request.Scope)
	handler.mu.Lock()
	handler.expireLocked(time.Now())
	if handler.pending[scopeKey] != nil {
		handler.mu.Unlock()
		return resource.Result{}, errors.New("a staged AST proposal is already pending; resolve or reject it first")
	}
	handler.mu.Unlock()

	var targets []astSearchTarget
	for _, path := range input.Paths {
		resolved, err := resolveASTSearchTargets(ctx, request.Scope.Workspace, handler.resources, request.Scope, path)
		if err != nil {
			cleanupASTTargets(targets)
			return resource.Result{}, err
		}
		targets = append(targets, resolved...)
	}
	defer cleanupASTTargets(targets)
	proposal := &astProposal{
		ID: fmt.Sprintf("ast-%d", handler.sequence.Add(1)), ScopeKey: scopeKey, Workspace: request.Scope.Workspace,
		CreatedAt: time.Now(), Files: make(map[string]*astProposalFile),
	}
	changesByPath := make(map[string][]astNativeChange)
	internalByPath := make(map[string]astSearchTarget)
	for _, target := range targets {
		var native astNativeEditResult
		options := map[string]any{
			"rewriteOps": input.Ops, "path": target.baseAbsolute, "dryRun": true,
			"maxFiles": maxASTEditFiles, "failOnParseError": true,
		}
		if target.glob != "" {
			options["glob"] = target.glob
		}
		if err := handler.bridge.invoke(ctx, "edit", options, &native); err != nil {
			return resource.Result{}, err
		}
		if native.LimitReached {
			return resource.Result{}, errors.New("ast_edit exceeded the 1000-file safety limit")
		}
		proposal.Replacements += native.TotalReplacements
		proposal.FilesSearched += native.FilesSearched
		proposal.ParseErrors = append(proposal.ParseErrors, native.ParseErrors...)
		for _, change := range native.Changes {
			path, err := rebaseASTResultPath(request.Scope.Workspace, target, change.Path)
			if err != nil {
				return resource.Result{}, err
			}
			change.Path = path
			changesByPath[path] = append(changesByPath[path], change)
			if target.internalURI != "" {
				internalByPath[path] = target
			}
		}
	}
	if proposal.Replacements == 0 || len(changesByPath) == 0 {
		return resource.Result{}, errors.New("ast_edit produced no replacements")
	}
	if len(changesByPath) > maxASTEditFiles {
		return resource.Result{}, errors.New("ast_edit touched more than 1000 files")
	}
	totalBytes := 0
	for path, changes := range changesByPath {
		var original []byte
		mode := os.FileMode(0o644)
		resourceURI := ""
		if target, ok := internalByPath[path]; ok {
			resourceURI = target.internalURI
			original = append([]byte(nil), target.internalData...)
			uri, err := resource.Parse(resourceURI)
			if err != nil || uri.Scheme == "xd" || !slices.Contains(handler.resources.Operations(uri.Scheme), "write") {
				return resource.Result{}, fmt.Errorf("internal AST target %s is not writable", resourceURI)
			}
		} else {
			root, err := os.OpenRoot(request.Scope.Workspace)
			if err != nil {
				return resource.Result{}, err
			}
			original, err = root.ReadFile(filepath.FromSlash(path))
			if err == nil {
				if info, statErr := root.Stat(filepath.FromSlash(path)); statErr == nil {
					mode = info.Mode().Perm()
				} else {
					err = statErr
				}
			}
			_ = root.Close()
			if err != nil {
				return resource.Result{}, err
			}
		}
		proposed, firstLine, err := applyASTChanges(original, changes)
		if err != nil {
			return resource.Result{}, fmt.Errorf("%s: %w", path, err)
		}
		totalBytes += len(original) + len(proposed)
		if totalBytes > maxASTProposalBytes {
			return resource.Result{}, errors.New("AST proposal exceeds 64 MiB")
		}
		proposal.Files[path] = &astProposalFile{Path: path, ResourceURI: resourceURI, Original: original, Proposed: proposed, Mode: mode, Count: len(changes), FirstLine: firstLine}
	}
	handler.mu.Lock()
	defer handler.mu.Unlock()
	handler.expireLocked(time.Now())
	if handler.pending[scopeKey] != nil {
		return resource.Result{}, errors.New("another AST proposal was staged concurrently")
	}
	handler.pending[scopeKey] = proposal
	return astXDevResult(request.URI.Raw, renderASTProposalSummary(proposal, false), map[string]string{"proposal": proposal.ID, "state": "staged"}), nil
}

func applyASTChanges(original []byte, changes []astNativeChange) ([]byte, int, error) {
	sort.Slice(changes, func(left, right int) bool {
		if changes[left].ByteStart != changes[right].ByteStart {
			return changes[left].ByteStart < changes[right].ByteStart
		}
		return changes[left].ByteEnd < changes[right].ByteEnd
	})
	for index, change := range changes {
		if change.ByteStart < 0 || change.ByteEnd < change.ByteStart || change.ByteEnd > len(original) {
			return nil, 0, errors.New("native rewrite span is outside the source")
		}
		if index > 0 && change.ByteStart < changes[index-1].ByteEnd {
			return nil, 0, errors.New("native rewrite spans overlap")
		}
		if !bytes.Equal(original[change.ByteStart:change.ByteEnd], []byte(change.Before)) {
			return nil, 0, errors.New("native rewrite before-text does not match the source bytes")
		}
	}
	proposed := append([]byte(nil), original...)
	for index := len(changes) - 1; index >= 0; index-- {
		change := changes[index]
		next := make([]byte, 0, len(proposed)-(change.ByteEnd-change.ByteStart)+len(change.After))
		next = append(next, proposed[:change.ByteStart]...)
		next = append(next, change.After...)
		next = append(next, proposed[change.ByteEnd:]...)
		proposed = next
	}
	if bytes.Equal(original, proposed) {
		return nil, 0, errors.New("rewrite is a no-op")
	}
	return proposed, changes[0].StartLine, nil
}

func (handler *astXDevHandler) resolve(ctx context.Context, request resource.Request, reason string) (resource.Result, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 1000 || strings.ContainsAny(reason, "\r\n") {
		return resource.Result{}, errors.New("xd://resolve requires one non-empty reason sentence")
	}
	key := astProposalScope(request.Scope)
	handler.mu.Lock()
	defer handler.mu.Unlock()
	handler.expireLocked(time.Now())
	proposal := handler.pending[key]
	if proposal == nil {
		return resource.Result{}, errors.New("no staged AST proposal")
	}
	if filepath.Clean(proposal.Workspace) != filepath.Clean(request.Scope.Workspace) {
		return resource.Result{}, errors.New("staged AST proposal belongs to another workspace")
	}
	if err := handler.applyProposal(ctx, request.Scope, proposal); err != nil {
		return resource.Result{}, err
	}
	delete(handler.pending, key)
	return astXDevResult(request.URI.Raw, "Applied "+renderASTProposalSummary(proposal, true)+"\nReason: "+reason, map[string]string{"proposal": proposal.ID, "state": "applied"}), nil
}

func (handler *astXDevHandler) reject(request resource.Request, reason string) (resource.Result, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 1000 || strings.ContainsAny(reason, "\r\n") {
		return resource.Result{}, errors.New("xd://reject requires one non-empty reason sentence")
	}
	key := astProposalScope(request.Scope)
	handler.mu.Lock()
	defer handler.mu.Unlock()
	proposal := handler.pending[key]
	if proposal == nil {
		return resource.Result{}, errors.New("no staged AST proposal")
	}
	delete(handler.pending, key)
	return astXDevResult(request.URI.Raw, "Rejected AST proposal "+proposal.ID+".\nReason: "+reason, map[string]string{"proposal": proposal.ID, "state": "rejected"}), nil
}

func (handler *astXDevHandler) applyProposal(ctx context.Context, scope resource.Scope, proposal *astProposal) error {
	root, err := os.OpenRoot(proposal.Workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	var local []*astProposalFile
	var remote []*astProposalFile
	for _, file := range proposal.Files {
		if file.ResourceURI == "" {
			current, err := root.ReadFile(filepath.FromSlash(file.Path))
			if err != nil || !bytes.Equal(current, file.Original) {
				return fmt.Errorf("AST proposal is stale for %s", file.Path)
			}
			local = append(local, file)
		} else {
			current, err := handler.resources.Read(ctx, file.ResourceURI, "raw", scope)
			if err != nil || !bytes.Equal(current.Data, file.Original) {
				return fmt.Errorf("AST proposal is stale for %s", file.ResourceURI)
			}
			remote = append(remote, file)
		}
	}
	sort.Slice(local, func(left, right int) bool { return local[left].Path < local[right].Path })
	sort.Slice(remote, func(left, right int) bool { return remote[left].ResourceURI < remote[right].ResourceURI })
	appliedRemote := make([]*astProposalFile, 0, len(remote))
	for _, file := range remote {
		if _, err := handler.resources.Write(ctx, file.ResourceURI, "", scope, resource.Result{URI: file.ResourceURI, MediaType: "text/plain", Data: file.Proposed}); err != nil {
			return combineRollbackError(err, rollbackASTResources(ctx, handler.resources, scope, appliedRemote))
		}
		appliedRemote = append(appliedRemote, file)
	}
	if err := applyASTLocalFiles(ctx, root, local); err != nil {
		return combineRollbackError(err, rollbackASTResources(ctx, handler.resources, scope, appliedRemote))
	}
	return nil
}

func applyASTLocalFiles(ctx context.Context, root *os.Root, files []*astProposalFile) error {
	temporary := make(map[string]string, len(files))
	originals := make(map[string]originalFileState, len(files))
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			cleanupHashlineTemps(root, temporary)
			return err
		}
		parent := filepath.Dir(filepath.FromSlash(file.Path))
		writer, tempPath, err := openRootTemp(root, parent, ".azem-ast", file.Mode)
		if err != nil {
			cleanupHashlineTemps(root, temporary)
			return err
		}
		writeErr := writer.Chmod(file.Mode.Perm())
		if writeErr == nil {
			_, writeErr = writer.Write(file.Proposed)
		}
		if writeErr == nil {
			writeErr = writer.Sync()
		}
		closeErr := writer.Close()
		if writeErr == nil {
			writeErr = closeErr
		}
		if writeErr != nil {
			_ = root.Remove(tempPath)
			cleanupHashlineTemps(root, temporary)
			return writeErr
		}
		temporary[file.Path] = tempPath
		originals[file.Path] = originalFileState{data: file.Original, mode: file.Mode}
	}
	var mutated []string
	for _, file := range files {
		if err := root.Rename(temporary[file.Path], filepath.FromSlash(file.Path)); err != nil {
			rollbackErr := rollbackHashlineFiles(ctx, root, originals, mutated)
			cleanupHashlineTemps(root, temporary)
			return combineRollbackError(err, rollbackErr)
		}
		delete(temporary, file.Path)
		mutated = append(mutated, file.Path)
	}
	return nil
}

func rollbackASTResources(ctx context.Context, router *resource.Router, scope resource.Scope, files []*astProposalFile) error {
	var failures []string
	for index := len(files) - 1; index >= 0; index-- {
		file := files[index]
		if _, err := router.Write(ctx, file.ResourceURI, "", scope, resource.Result{URI: file.ResourceURI, MediaType: "text/plain", Data: file.Original}); err != nil {
			failures = append(failures, file.ResourceURI+": "+err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func (handler *astXDevHandler) expireLocked(now time.Time) {
	for key, proposal := range handler.pending {
		if now.Sub(proposal.CreatedAt) > astProposalLifetime {
			delete(handler.pending, key)
		}
	}
}

func astProposalScope(scope resource.Scope) string {
	identity := scope.SessionID
	if identity == "" {
		identity = scope.RunID
	}
	if identity == "" {
		identity = "workspace"
	}
	return filepath.Clean(scope.Workspace) + "\x00" + identity
}

func renderASTProposalSummary(proposal *astProposal, compact bool) string {
	paths := make([]string, 0, len(proposal.Files))
	for path := range proposal.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var output strings.Builder
	if !compact {
		fmt.Fprintf(&output, "Staged AST proposal %s: %d replacement(s) across %d file(s).", proposal.ID, proposal.Replacements, len(paths))
		output.WriteString("\nReview the preview, then write one reason sentence to xd://resolve or xd://reject.")
	} else {
		fmt.Fprintf(&output, "AST proposal %s: %d replacement(s) across %d file(s).", proposal.ID, proposal.Replacements, len(paths))
	}
	for _, path := range paths {
		file := proposal.Files[path]
		displayPath := path
		if file.ResourceURI != "" {
			displayPath = file.ResourceURI
		}
		fmt.Fprintf(&output, "\n[%s#%s]\n%d replacement(s)", displayPath, computeHashlineTag(normalizeHashlineText(file.Proposed)), file.Count)
		if !compact {
			diff := compactHashlineDiff(normalizeHashlineText(file.Original), normalizeHashlineText(file.Proposed))
			if diff != "" {
				output.WriteString("\n" + diff)
			}
		}
	}
	return output.String()
}

func astXDevResult(uri, content string, metadata map[string]string) resource.Result {
	if metadata == nil {
		metadata = make(map[string]string)
	}
	metadata["toolResult"] = "true"
	return resource.Result{URI: uri, MediaType: "text/plain", Data: []byte(content), Metadata: metadata}
}
