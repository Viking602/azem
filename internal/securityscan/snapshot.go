package securityscan

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

type Snapshotter struct {
	DataRoot string
	GitPath  string
}

func (s Snapshotter) Prepare(ctx context.Context, scanID string, request StartRequest) (Target, string, error) {
	if err := request.Validate(); err != nil {
		return Target{}, "", err
	}
	root, err := filepath.EvalSymlinks(request.Repository)
	if err != nil {
		return Target{}, "", fmt.Errorf("security scan: resolve repository: %w", err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return Target{}, "", err
	}
	metadata, err := os.Stat(root)
	if err != nil || !metadata.IsDir() {
		return Target{}, "", fmt.Errorf("security scan: repository is not a directory")
	}
	git, gitRoot, gitErr := s.gitRepository(ctx, root)
	if gitErr != nil && !errors.Is(gitErr, errNotGitRepository) {
		return Target{}, "", gitErr
	}
	isGit := gitErr == nil
	if (request.TargetKind == TargetGitRefs || request.TargetKind == TargetWorkingTree) && !isGit {
		return Target{}, "", fmt.Errorf("security scan: diff target requires a Git repository")
	}
	if isGit {
		if filepath.Clean(root) != filepath.Clean(gitRoot) {
			return Target{}, "", fmt.Errorf("security scan: repository must be the Git worktree root; use a path target for a subdirectory")
		}
		root = gitRoot
	}

	remote := ""
	revision := ""
	baseRevision := ""
	headRevision := ""
	if isGit {
		remote = s.gitRemote(ctx, git, root)
		revision, err = s.gitOutput(ctx, git, root, "rev-parse", "HEAD")
		if err != nil {
			return Target{}, "", err
		}
	}
	if request.TargetKind == TargetGitRefs {
		baseRevision, err = s.gitOutput(ctx, git, root, "rev-parse", request.Base+"^{commit}")
		if err != nil {
			return Target{}, "", fmt.Errorf("security scan: resolve diff base: %w", err)
		}
		head := request.Head
		if strings.TrimSpace(head) == "" {
			head = "HEAD"
		}
		headRevision, err = s.gitOutput(ctx, git, root, "rev-parse", head+"^{commit}")
		if err != nil {
			return Target{}, "", fmt.Errorf("security scan: resolve diff head: %w", err)
		}
		if revision != headRevision {
			return Target{}, "", fmt.Errorf("security scan: checkout HEAD does not match requested diff head")
		}
		status, statusErr := s.gitOutput(ctx, git, root, "status", "--porcelain=v1", "--untracked-files=all")
		if statusErr != nil {
			return Target{}, "", statusErr
		}
		if status != "" {
			return Target{}, "", fmt.Errorf("security scan: committed diff requires a clean checkout")
		}
	}
	if request.TargetKind == TargetWorkingTree {
		base := request.Base
		if strings.TrimSpace(base) == "" {
			base = "HEAD"
		}
		baseRevision, err = s.gitOutput(ctx, git, root, "rev-parse", base+"^{commit}")
		if err != nil {
			return Target{}, "", fmt.Errorf("security scan: resolve working-tree base: %w", err)
		}
		headRevision = revision
	}

	targetID, sanitizedRemote, err := TargetIdentity(remote, root)
	if err != nil {
		if remote != "" {
			targetID, sanitizedRemote, err = TargetIdentity("", root)
		}
		if err != nil {
			return Target{}, "", err
		}
	}
	scopeInventory, err := s.inventory(ctx, git, root, isGit, request, baseRevision, headRevision)
	if err != nil {
		return Target{}, "", err
	}
	scopePaths := append([]string(nil), scopeInventory...)
	diffArtifact := ""
	diffDigest := ""
	var diffEvidence []byte
	if request.TargetKind == TargetGitRefs || request.TargetKind == TargetWorkingTree {
		scopePaths, err = s.diffScopePaths(ctx, git, root, request.TargetKind, baseRevision, headRevision)
		if err != nil {
			return Target{}, "", err
		}
		diffEvidence, err = s.diffEvidence(ctx, git, root, request.TargetKind, baseRevision, headRevision, scopePaths)
		if err != nil {
			return Target{}, "", err
		}
		diffArtifact = filepath.ToSlash(filepath.Join(".azem-security", scanID, "diff.patch"))
		sum := sha256.Sum256(diffEvidence)
		diffDigest = "sha256:" + hex.EncodeToString(sum[:])
	}
	if len(scopePaths) == 0 {
		return Target{}, "", fmt.Errorf("security scan: selected target contains no regular files")
	}
	snapshotInventory := append([]string(nil), scopeInventory...)
	if request.TargetKind == TargetGitRefs || request.TargetKind == TargetWorkingTree {
		contextRequest := request
		contextRequest.TargetKind, contextRequest.Paths = TargetRepository, nil
		snapshotInventory, err = s.inventory(ctx, git, root, isGit, contextRequest, baseRevision, headRevision)
		if err != nil {
			return Target{}, "", err
		}
	}
	outputRoot := filepath.Join(s.DataRoot, "security-scans", targetID, scanID)
	sourceRoot := filepath.Join(outputRoot, "source")
	if err := os.MkdirAll(sourceRoot, 0o700); err != nil {
		return Target{}, "", fmt.Errorf("security scan: create snapshot root: %w", err)
	}
	digest, err := copySnapshot(ctx, root, sourceRoot, snapshotInventory)
	if err != nil {
		_ = os.RemoveAll(outputRoot)
		return Target{}, "", err
	}
	if diffArtifact != "" {
		if err := writeSnapshotEvidence(sourceRoot, diffArtifact, diffEvidence); err != nil {
			_ = os.RemoveAll(outputRoot)
			return Target{}, "", err
		}
		scopeInventory = append(scopeInventory, diffArtifact)
		snapshotInventory = append(snapshotInventory, diffArtifact)
		sort.Strings(scopeInventory)
		sort.Strings(snapshotInventory)
	}
	if err := protectSnapshot(sourceRoot); err != nil {
		_ = os.RemoveAll(outputRoot)
		return Target{}, "", err
	}
	includePaths := []string{"."}
	if request.TargetKind == TargetPaths {
		includePaths = append([]string(nil), request.Paths...)
		for index, include := range includePaths {
			normalized, pathErr := SafeRelativePath(filepath.ToSlash(include), true)
			if pathErr != nil {
				return Target{}, "", pathErr
			}
			includePaths[index] = normalized
		}
		sort.Strings(includePaths)
	}
	if request.TargetKind == TargetGitRefs || request.TargetKind == TargetWorkingTree {
		includePaths = append([]string(nil), scopePaths...)
	}
	target := Target{
		Kind: request.TargetKind, Repository: root, TargetID: targetID, DisplayName: filepath.Base(root),
		Remote: sanitizedRemote, Revision: revision, BaseRevision: baseRevision, HeadRevision: headRevision,
		SnapshotDigest: SnapshotAlgorithm + ":sha256:" + digest, SnapshotRoot: sourceRoot,
		IncludePaths: includePaths, ExcludePaths: []string{}, Inventory: scopeInventory, SnapshotPaths: snapshotInventory,
		ScopePaths: scopePaths, DiffArtifact: diffArtifact, DiffDigest: diffDigest,
	}
	return target, filepath.Join(outputRoot, "result"), nil
}

var errNotGitRepository = errors.New("not a Git repository")

func (s Snapshotter) gitRepository(ctx context.Context, root string) (string, string, error) {
	git := strings.TrimSpace(s.GitPath)
	if git == "" {
		var err error
		git, err = exec.LookPath("git")
		if err != nil {
			return "", "", errNotGitRepository
		}
	}
	git, err := filepath.EvalSymlinks(git)
	if err != nil {
		return "", "", fmt.Errorf("security scan: resolve Git executable: %w", err)
	}
	git, err = filepath.Abs(git)
	if err != nil {
		return "", "", err
	}
	if pathWithin(root, git) {
		return "", "", fmt.Errorf("security scan: refusing repository-local Git executable")
	}
	resolved, err := s.gitOutput(ctx, git, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", errNotGitRepository
	}
	resolved, err = filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", "", err
	}
	return git, resolved, nil
}

func sanitizedGitEnvironment() []string {
	blocked := map[string]struct{}{
		"GIT_DIR": {}, "GIT_WORK_TREE": {}, "GIT_INDEX_FILE": {}, "GIT_OBJECT_DIRECTORY": {},
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": {}, "GIT_COMMON_DIR": {}, "GIT_GRAFT_FILE": {},
		"GIT_NAMESPACE": {}, "GIT_PREFIX": {}, "GIT_SHALLOW_FILE": {},
	}
	environment := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		name := strings.ToUpper(strings.SplitN(entry, "=", 2)[0])
		if _, denied := blocked[name]; denied || name == "GIT_ALLOW_PROTOCOL" {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment, "GIT_ALLOW_PROTOCOL=", "GIT_TERMINAL_PROMPT=0")
}

func (s Snapshotter) gitOutput(ctx context.Context, git, root string, arguments ...string) (string, error) {
	args := append([]string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-C", root}, arguments...)
	command := exec.CommandContext(ctx, git, args...)
	command.Env = sanitizedGitEnvironment()
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("security scan: git %s: %w", strings.Join(arguments, " "), err)
	}
	return strings.TrimSpace(string(output)), nil
}

func (s Snapshotter) gitRemote(ctx context.Context, git, root string) string {
	remote, err := s.gitOutput(ctx, git, root, "config", "--get", "remote.origin.url")
	if err != nil || remote == "" {
		return ""
	}
	if at := strings.Index(remote, "@"); at > 0 && !strings.Contains(remote[:at], "://") {
		colon := strings.Index(remote[at+1:], ":")
		if colon >= 0 {
			host := remote[at+1 : at+1+colon]
			remote = "ssh://" + host + "/" + remote[at+1+colon+1:]
		}
	}
	return remote
}

func (s Snapshotter) inventory(ctx context.Context, git, root string, isGit bool, request StartRequest, baseRevision, headRevision string) ([]string, error) {
	var candidates []string
	if isGit {
		arguments := []string{"ls-files", "-co", "--exclude-standard", "-z"}
		if request.TargetKind == TargetGitRefs {
			arguments = []string{"diff", "--name-only", "--diff-filter=ACMRTUXB", "-z", baseRevision + ".." + headRevision}
		}
		if request.TargetKind == TargetWorkingTree {
			arguments = []string{"diff", "--name-only", "--diff-filter=ACMRTUXB", "-z", baseRevision}
		}
		output, err := s.gitRaw(ctx, git, root, arguments...)
		if err != nil {
			return nil, err
		}
		candidates = strings.Split(string(output), "\x00")
		if request.TargetKind == TargetWorkingTree {
			untracked, untrackedErr := s.gitRaw(ctx, git, root, "ls-files", "--others", "--exclude-standard", "-z")
			if untrackedErr != nil {
				return nil, untrackedErr
			}
			candidates = append(candidates, strings.Split(string(untracked), "\x00")...)
		}
	} else {
		err := filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				if entry.Name() == ".git" && current != root {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.Type().IsRegular() {
				relative, err := filepath.Rel(root, current)
				if err != nil {
					return err
				}
				candidates = append(candidates, filepath.ToSlash(relative))
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("security scan: walk repository: %w", err)
		}
	}

	scope := make([]string, 0, len(request.Paths))
	for _, requested := range request.Paths {
		normalized, err := SafeRelativePath(filepath.ToSlash(requested), true)
		if err != nil {
			return nil, err
		}
		scope = append(scope, strings.TrimSuffix(normalized, "/"))
	}
	seen := make(map[string]struct{})
	inventory := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(filepath.ToSlash(candidate))
		if candidate == "" {
			continue
		}
		normalized, err := SafeRelativePath(candidate, false)
		if err != nil {
			return nil, err
		}
		if len(scope) > 0 && !inScope(normalized, scope) {
			continue
		}
		metadata, exists, err := regularSnapshotSource(root, normalized)
		if err != nil {
			return nil, err
		}
		if !exists || !metadata.Mode().IsRegular() {
			continue
		}
		if _, exists := seen[normalized]; !exists {
			seen[normalized] = struct{}{}
			inventory = append(inventory, normalized)
		}
	}
	sort.Strings(inventory)
	return inventory, nil
}

func regularSnapshotSource(root, relative string) (os.FileInfo, bool, error) {
	current := root
	parts := strings.Split(filepath.FromSlash(relative), string(filepath.Separator))
	for index, part := range parts {
		current = filepath.Join(current, part)
		metadata, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		if metadata.Mode()&os.ModeSymlink != 0 {
			return nil, false, fmt.Errorf("security scan: refusing symlinked snapshot path: %s", relative)
		}
		if index < len(parts)-1 && !metadata.IsDir() {
			return nil, false, fmt.Errorf("security scan: non-directory snapshot path component: %s", relative)
		}
		if index == len(parts)-1 {
			return metadata, true, nil
		}
	}
	return nil, false, nil
}

func (s Snapshotter) diffScopePaths(ctx context.Context, git, root string, kind TargetKind, baseRevision, headRevision string) ([]string, error) {
	arguments := []string{"diff", "--name-only", "--diff-filter=ACDMRTUXB", "-z"}
	if kind == TargetGitRefs {
		arguments = append(arguments, baseRevision+".."+headRevision, "--")
	} else {
		arguments = append(arguments, baseRevision, "--")
	}
	output, err := s.gitRaw(ctx, git, root, arguments...)
	if err != nil {
		return nil, err
	}
	candidates := strings.Split(string(output), "\x00")
	if kind == TargetWorkingTree {
		untracked, untrackedErr := s.gitRaw(ctx, git, root, "ls-files", "--others", "--exclude-standard", "-z")
		if untrackedErr != nil {
			return nil, untrackedErr
		}
		candidates = append(candidates, strings.Split(string(untracked), "\x00")...)
	}
	seen := make(map[string]struct{}, len(candidates))
	paths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		normalized, pathErr := SafeRelativePath(filepath.ToSlash(candidate), false)
		if pathErr != nil {
			return nil, pathErr
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		paths = append(paths, normalized)
	}
	sort.Strings(paths)
	return paths, nil
}

func (s Snapshotter) diffEvidence(ctx context.Context, git, root string, kind TargetKind, baseRevision, headRevision string, scopePaths []string) ([]byte, error) {
	arguments := []string{"diff", "--no-ext-diff", "--no-textconv", "--binary", "--full-index", "--no-color", "--find-renames"}
	if kind == TargetGitRefs {
		arguments = append(arguments, baseRevision+".."+headRevision, "--")
	} else {
		arguments = append(arguments, baseRevision, "--")
	}
	patch, err := s.gitRaw(ctx, git, root, arguments...)
	if err != nil {
		return nil, err
	}
	var evidence strings.Builder
	fmt.Fprintf(&evidence, "# Azem immutable diff evidence\n# target=%s\n# base=%s\n# head=%s\n", kind, baseRevision, headRevision)
	evidence.Write(patch)
	if len(patch) > 0 && patch[len(patch)-1] != '\n' {
		evidence.WriteByte('\n')
	}
	if kind == TargetWorkingTree {
		evidence.WriteString("\n# Complete changed-path inventory (untracked files are read from the snapshot)\n")
		for _, path := range scopePaths {
			fmt.Fprintf(&evidence, "# %q\n", path)
		}
	}
	return []byte(evidence.String()), nil
}

func writeSnapshotEvidence(root, relative string, contents []byte) error {
	path, err := SafeRelativePath(relative, false)
	if err != nil {
		return err
	}
	destination := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("security scan: create immutable diff evidence: %w", err)
	}
	writeErr := func() error {
		_, err := output.Write(contents)
		return err
	}()
	closeErr := output.Close()
	if writeErr != nil || closeErr != nil {
		return fmt.Errorf("security scan: write immutable diff evidence: %w", errors.Join(writeErr, closeErr))
	}
	return nil
}

func (s Snapshotter) gitRaw(ctx context.Context, git, root string, arguments ...string) ([]byte, error) {
	args := append([]string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-C", root}, arguments...)
	command := exec.CommandContext(ctx, git, args...)
	command.Env = sanitizedGitEnvironment()
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("security scan: git %s: %w", strings.Join(arguments, " "), err)
	}
	return output, nil
}

func inScope(candidate string, scopes []string) bool {
	for _, scope := range scopes {
		if scope == "." || candidate == scope || strings.HasPrefix(candidate, scope+"/") {
			return true
		}
	}
	return false
}

func copySnapshot(ctx context.Context, sourceRoot, destinationRoot string, inventory []string) (string, error) {
	source, err := os.OpenRoot(sourceRoot)
	if err != nil {
		return "", err
	}
	defer source.Close()
	destination, err := os.OpenRoot(destinationRoot)
	if err != nil {
		return "", err
	}
	defer destination.Close()
	digest := sha256.New()
	writer := bufio.NewWriter(digest)
	for _, relative := range inventory {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		rootPath := filepath.FromSlash(relative)
		input, err := source.Open(rootPath)
		if err != nil {
			return "", fmt.Errorf("security scan: open bounded snapshot source %s: %w", relative, err)
		}
		metadata, statErr := input.Stat()
		if statErr != nil || !metadata.Mode().IsRegular() {
			_ = input.Close()
			return "", fmt.Errorf("security scan: source changed while snapshotting: %s", relative)
		}
		if err := destination.MkdirAll(filepath.Dir(rootPath), 0o700); err != nil {
			_ = input.Close()
			return "", err
		}
		output, err := destination.OpenFile(rootPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			_ = input.Close()
			return "", err
		}
		fileDigest := sha256.New()
		_, copyErr := io.Copy(io.MultiWriter(output, fileDigest), input)
		closeInputErr, closeOutputErr := input.Close(), output.Close()
		if copyErr != nil || closeInputErr != nil || closeOutputErr != nil {
			return "", errors.Join(copyErr, closeInputErr, closeOutputErr)
		}
		_, _ = writer.WriteString(relative)
		_ = writer.WriteByte(0)
		_, _ = writer.WriteString(hex.EncodeToString(fileDigest.Sum(nil)))
		_ = writer.WriteByte(0)
	}
	if err := writer.Flush(); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func protectSnapshot(root string) error {
	var directories []string
	err := filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			directories = append(directories, current)
			return nil
		}
		return os.Chmod(current, 0o400)
	})
	if err != nil {
		return fmt.Errorf("security scan: protect snapshot: %w", err)
	}
	sort.Slice(directories, func(i, j int) bool { return len(directories[i]) > len(directories[j]) })
	for _, directory := range directories {
		if err := os.Chmod(directory, 0o500); err != nil {
			return err
		}
	}
	return nil
}

func (s Snapshotter) Changed(ctx context.Context, target Target) (bool, error) {
	root, err := filepath.EvalSymlinks(target.Repository)
	if err != nil {
		return true, nil
	}
	git, _, gitErr := s.gitRepository(ctx, root)
	isGit := gitErr == nil
	request := StartRequest{
		Repository: root, TargetKind: target.Kind, Mode: ModeStandard,
		Route: Route{Provider: "snapshot", Model: "snapshot"},
		Base:  target.BaseRevision, Head: target.HeadRevision,
	}
	if target.Kind == TargetPaths {
		request.Paths = append([]string(nil), target.IncludePaths...)
	}
	sourcePaths := append([]string(nil), target.SnapshotPaths...)
	if target.DiffArtifact != "" {
		sourcePaths = slices.DeleteFunc(sourcePaths, func(path string) bool { return path == target.DiffArtifact })
	}
	inventoryRequest := request
	if target.Kind == TargetGitRefs || target.Kind == TargetWorkingTree {
		inventoryRequest.TargetKind = TargetRepository
	}
	inventory, err := s.inventory(ctx, git, root, isGit, inventoryRequest, target.BaseRevision, target.HeadRevision)
	if err != nil {
		return false, err
	}
	if !slices.Equal(inventory, sourcePaths) {
		return true, nil
	}
	digest, err := digestSnapshotFiles(ctx, root, inventory)
	if err != nil {
		return false, err
	}
	if SnapshotAlgorithm+":sha256:"+digest != target.SnapshotDigest {
		return true, nil
	}
	if target.Kind == TargetGitRefs || target.Kind == TargetWorkingTree {
		scopePaths, scopeErr := s.diffScopePaths(ctx, git, root, target.Kind, target.BaseRevision, target.HeadRevision)
		if scopeErr != nil {
			return false, scopeErr
		}
		if !slices.Equal(scopePaths, target.ScopePaths) {
			return true, nil
		}
		evidence, evidenceErr := s.diffEvidence(ctx, git, root, target.Kind, target.BaseRevision, target.HeadRevision, scopePaths)
		if evidenceErr != nil {
			return false, evidenceErr
		}
		sum := sha256.Sum256(evidence)
		if "sha256:"+hex.EncodeToString(sum[:]) != target.DiffDigest {
			return true, nil
		}
	}
	if isGit && target.Revision != "" {
		revision, err := s.gitOutput(ctx, git, root, "rev-parse", "HEAD")
		if err != nil {
			return false, err
		}
		if revision != target.Revision {
			return true, nil
		}
	}
	return false, nil
}

func digestSnapshotFiles(ctx context.Context, root string, inventory []string) (string, error) {
	bounded, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer bounded.Close()
	digest := sha256.New()
	writer := bufio.NewWriter(digest)
	for _, relative := range inventory {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		file, err := bounded.Open(filepath.FromSlash(relative))
		if err != nil {
			return "", fmt.Errorf("security scan: open bounded live target %s: %w", relative, err)
		}
		metadata, statErr := file.Stat()
		if statErr != nil || !metadata.Mode().IsRegular() {
			_ = file.Close()
			return "", fmt.Errorf("security scan: live target changed: %s", relative)
		}
		fileDigest := sha256.New()
		_, copyErr := io.Copy(fileDigest, file)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			return "", errors.Join(copyErr, closeErr)
		}
		_, _ = writer.WriteString(relative)
		_ = writer.WriteByte(0)
		_, _ = writer.WriteString(hex.EncodeToString(fileDigest.Sum(nil)))
		_ = writer.WriteByte(0)
	}
	if err := writer.Flush(); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func (s Snapshotter) Cleanup(target Target) error {
	root := target.SnapshotRoot
	if root == "" {
		return nil
	}
	_ = filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			_ = os.Chmod(current, 0o700)
		} else {
			_ = os.Chmod(current, 0o600)
		}
		return nil
	})
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("security scan: remove source snapshot: %w", err)
	}
	return nil
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && (relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)))
}
