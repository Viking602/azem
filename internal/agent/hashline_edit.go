package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Viking602/venat/tool"
)

const (
	maxHashlineRegistersPerScope = 64
	maxHashlineRegisterBytes     = 64 << 20
)

var (
	hashlineSectionHeader = regexp.MustCompile(`^\[([^#\r\n]+)#([0-9A-F]{4})\]$`)
	hashlinePutBody       = regexp.MustCompile(`^PUT (.+):$`)
	hashlinePutRegister   = regexp.MustCompile(`^PUT (.+?)(?: @([A-Za-z0-9_-]+))?$`)
	hashlineCut           = regexp.MustCompile(`^CUT (.+?)(?: @([A-Za-z0-9_-]+))?$`)
)

type hashlineClipboard struct {
	editMu sync.Mutex
	mu     sync.Mutex
	values map[string]map[string][]string
	order  map[string][]string
}

func newHashlineClipboard() *hashlineClipboard {
	return &hashlineClipboard{values: make(map[string]map[string][]string), order: make(map[string][]string)}
}

func (clipboard *hashlineClipboard) load(scope, name string) ([]string, bool) {
	clipboard.mu.Lock()
	defer clipboard.mu.Unlock()
	value, ok := clipboard.values[scope][name]
	return append([]string(nil), value...), ok
}

func (clipboard *hashlineClipboard) commit(scope string, values map[string][]string) {
	if clipboard == nil || len(values) == 0 {
		return
	}
	clipboard.mu.Lock()
	defer clipboard.mu.Unlock()
	if clipboard.values[scope] == nil {
		clipboard.values[scope] = make(map[string][]string)
	}
	for name, value := range values {
		if _, exists := clipboard.values[scope][name]; !exists {
			clipboard.order[scope] = append(clipboard.order[scope], name)
		}
		clipboard.values[scope][name] = append([]string(nil), value...)
	}
	for len(clipboard.order[scope]) > maxHashlineRegistersPerScope || hashlineRegisterBytes(clipboard.values[scope]) > maxHashlineRegisterBytes {
		oldest := clipboard.order[scope][0]
		clipboard.order[scope] = clipboard.order[scope][1:]
		delete(clipboard.values[scope], oldest)
	}
}

func hashlineRegisterBytes(values map[string][]string) int {
	total := 0
	for _, lines := range values {
		for _, line := range lines {
			total += len(line) + 1
		}
	}
	return total
}

type hashlineDriver struct {
	root         string
	snapshotRead tool.Driver
	clipboard    *hashlineClipboard
	broker       *fileMutationBrokerRef
}

type hashlineInput struct {
	Input string `json:"input"`
}

type hashlinePatch struct {
	sections []hashlinePatchSection
}

type hashlinePatchSection struct {
	path string
	tag  string
	ops  []hashlinePatchOp
}

type hashlinePatchOp struct {
	kind     string
	locator  hashlineLocator
	body     []string
	register string
	dest     string
	sequence int
}

type hashlineLocator struct {
	kind       string
	start, end int
}

type hashlineEditAction struct {
	start, end int
	gap        int
	body       []string
	sequence   int
	replace    bool
}

type hashlinePreparedFile struct {
	source        string
	destination   string
	tag           string
	rawOriginal   []byte
	original      string
	final         string
	originalLines []string
	actions       []hashlineEditAction
	mode          os.FileMode
	remove        bool
	move          bool
	firstChanged  int
	diff          string
}

type originalFileState struct {
	data []byte
	mode os.FileMode
}

func newHashlineDriver(root string, snapshotRead tool.Driver, clipboard *hashlineClipboard, broker *fileMutationBrokerRef) tool.Driver {
	if clipboard == nil {
		clipboard = newHashlineClipboard()
	}
	return &hashlineDriver{root: root, snapshotRead: snapshotRead, clipboard: clipboard, broker: broker}
}

func (driver *hashlineDriver) Definition() tool.Definition {
	additional := true
	return tool.Definition{
		Name:        ToolEditHashline,
		Description: hashlineEditToolDescription,
		InputSchema: tool.Schema{
			Type: "object", Required: []string{"input"}, AdditionalProperties: &additional,
			Properties: map[string]tool.Schema{
				"input": {Type: "string", Description: "Complete *** Begin Patch / *** End Patch Hashline patch."},
			},
		},
		Concurrency: tool.ConcurrencyExclusive, ConcurrencyGroup: "workspace-files",
	}
}

func (driver *hashlineDriver) Execute(ctx context.Context, call tool.Call, sink tool.UpdateSink) (tool.Result, error) {
	driver.clipboard.editMu.Lock()
	defer driver.clipboard.editMu.Unlock()
	var input hashlineInput
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return hashlineError(call, fmt.Errorf("decode arguments: %w", err)), nil
	}
	patch, err := parseHashlinePatch(input.Input)
	if err != nil {
		return hashlineError(call, err), nil
	}
	root, err := os.OpenRoot(driver.root)
	if err != nil {
		return hashlineError(call, err), nil
	}
	defer root.Close()
	scope := hashlineClipboardScope(ctx, driver.root)
	prepared, pendingRegisters, err := driver.preparePatch(ctx, root, patch, scope)
	if err != nil {
		return hashlineError(call, err), nil
	}
	if err := driver.commitPatch(ctx, root, prepared); err != nil {
		return hashlineError(call, err), nil
	}
	driver.clipboard.commit(scope, pendingRegisters)
	result := driver.buildHashlineResult(ctx, prepared)
	if sink != nil {
		_ = sink(tool.Update{Kind: tool.UpdateProgress, Message: "applied hashline edit", Data: map[string]string{
			"phase": "applied", "oldTags": strings.Join(result.OldTags, ","), "newTags": strings.Join(result.NewTags, ","),
			"firstChangedLines": joinHashlineInts(result.FirstChangedLines), "diffHash": result.DiffHash,
		}})
	}
	structured, _ := json.Marshal(result)
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: result.Content, Structured: structured}, nil
}

func parseHashlinePatch(input string) (hashlinePatch, error) {
	input = strings.ReplaceAll(strings.ReplaceAll(input, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(input, "\n")
	if len(lines) < 3 || lines[0] != "*** Begin Patch" {
		return hashlinePatch{}, errors.New("patch must begin with *** Begin Patch")
	}
	end := len(lines) - 1
	if lines[end] == "" {
		end--
	}
	if end <= 0 || lines[end] != "*** End Patch" {
		return hashlinePatch{}, errors.New("patch must end with *** End Patch")
	}
	var patch hashlinePatch
	sequence := 0
	for index := 1; index < end; {
		header := hashlineSectionHeader.FindStringSubmatch(lines[index])
		if header == nil {
			return hashlinePatch{}, fmt.Errorf("line %d: expected [PATH#TAG] section header", index+1)
		}
		section := hashlinePatchSection{path: header[1], tag: header[2]}
		index++
		for index < end && hashlineSectionHeader.FindStringSubmatch(lines[index]) == nil {
			line := lines[index]
			if line == "" {
				return hashlinePatch{}, fmt.Errorf("line %d: blank lines are not valid between hunks", index+1)
			}
			operation, consumesBody, err := parseHashlinePatchOp(line, sequence)
			if err != nil {
				return hashlinePatch{}, fmt.Errorf("line %d: %w", index+1, err)
			}
			sequence++
			index++
			if consumesBody {
				for index < end && strings.HasPrefix(lines[index], "+") {
					operation.body = append(operation.body, strings.TrimPrefix(lines[index], "+"))
					index++
				}
				if len(operation.body) == 0 {
					return hashlinePatch{}, fmt.Errorf("line %d: PUT body requires at least one + row", index+1)
				}
			}
			section.ops = append(section.ops, operation)
		}
		if len(section.ops) == 0 {
			return hashlinePatch{}, fmt.Errorf("section %s has no hunks", section.path)
		}
		patch.sections = append(patch.sections, section)
	}
	if len(patch.sections) == 0 {
		return hashlinePatch{}, errors.New("patch has no file sections")
	}
	return patch, nil
}

func parseHashlinePatchOp(line string, sequence int) (hashlinePatchOp, bool, error) {
	if match := hashlinePutBody.FindStringSubmatch(line); match != nil {
		locator, err := parseHashlineLocator(match[1], true)
		if err != nil {
			return hashlinePatchOp{}, false, err
		}
		return hashlinePatchOp{kind: "put", locator: locator, sequence: sequence}, true, nil
	}
	if match := hashlinePutRegister.FindStringSubmatch(line); match != nil {
		locator, err := parseHashlineLocator(match[1], true)
		if err != nil {
			return hashlinePatchOp{}, false, err
		}
		if (locator.kind == "range" || locator.kind == "block") && match[2] == "" {
			return hashlinePatchOp{}, false, errors.New("range/block register PUT requires @name")
		}
		return hashlinePatchOp{kind: "paste", locator: locator, register: match[2], sequence: sequence}, false, nil
	}
	if match := hashlineCut.FindStringSubmatch(line); match != nil {
		locator, err := parseHashlineLocator(match[1], false)
		if err != nil || locator.kind != "range" && locator.kind != "block" {
			return hashlinePatchOp{}, false, errors.New("CUT requires N.=M or N*")
		}
		return hashlinePatchOp{kind: "cut", locator: locator, register: match[2], sequence: sequence}, false, nil
	}
	if line == "REM" {
		return hashlinePatchOp{kind: "remove", sequence: sequence}, false, nil
	}
	if strings.HasPrefix(line, "MV ") {
		destination, err := parseMoveDestination(strings.TrimSpace(strings.TrimPrefix(line, "MV ")))
		if err != nil {
			return hashlinePatchOp{}, false, err
		}
		return hashlinePatchOp{kind: "move", dest: destination, sequence: sequence}, false, nil
	}
	return hashlinePatchOp{}, false, errors.New("unrecognized hunk")
}

func parseHashlineLocator(value string, allowGap bool) (hashlineLocator, error) {
	value = strings.TrimSpace(value)
	if value == ">$" && allowGap {
		return hashlineLocator{kind: "tail"}, nil
	}
	if strings.HasPrefix(value, ">") && strings.HasSuffix(value, "*") && allowGap {
		line, err := positiveLine(strings.TrimSuffix(strings.TrimPrefix(value, ">"), "*"))
		return hashlineLocator{kind: "after_block", start: line}, err
	}
	if strings.HasPrefix(value, "<") && allowGap {
		line, err := positiveLine(strings.TrimPrefix(value, "<"))
		return hashlineLocator{kind: "before", start: line}, err
	}
	if strings.HasPrefix(value, ">") && allowGap {
		line, err := positiveLine(strings.TrimPrefix(value, ">"))
		return hashlineLocator{kind: "after", start: line}, err
	}
	if strings.HasSuffix(value, "*") {
		line, err := positiveLine(strings.TrimSuffix(value, "*"))
		return hashlineLocator{kind: "block", start: line}, err
	}
	if left, right, found := strings.Cut(value, ".="); found {
		start, err := positiveLine(left)
		if err != nil {
			return hashlineLocator{}, err
		}
		end, err := positiveLine(right)
		if err != nil || end < start {
			return hashlineLocator{}, errors.New("range end must be at or after its start")
		}
		return hashlineLocator{kind: "range", start: start, end: end}, nil
	}
	return hashlineLocator{}, fmt.Errorf("invalid locator %q", value)
}

func positiveLine(value string) (int, error) {
	line, err := strconv.Atoi(value)
	if err != nil || line <= 0 {
		return 0, errors.New("line identifiers are positive integers")
	}
	return line, nil
}

func parseMoveDestination(value string) (string, error) {
	if value == "" {
		return "", errors.New("MV requires a destination")
	}
	if value[0] == '"' {
		decoded, err := strconv.Unquote(value)
		if err != nil {
			return "", errors.New("invalid quoted MV destination")
		}
		return decoded, nil
	}
	if value[0] == '\'' {
		if len(value) < 2 || value[len(value)-1] != '\'' {
			return "", errors.New("invalid quoted MV destination")
		}
		return value[1 : len(value)-1], nil
	}
	return value, nil
}

func (driver *hashlineDriver) preparePatch(ctx context.Context, root *os.Root, patch hashlinePatch, scope string) ([]*hashlinePreparedFile, map[string][]string, error) {
	byPath := make(map[string]*hashlinePreparedFile)
	var order []string
	pendingRegisters := make(map[string][]string)
	var anonymous []string
	for _, section := range patch.sections {
		relative, err := workspaceRelativePath(driver.root, section.path)
		if err != nil {
			return nil, nil, err
		}
		file := byPath[relative]
		if file == nil {
			raw, err := root.ReadFile(filepath.FromSlash(relative))
			if err != nil {
				return nil, nil, fmt.Errorf("read %s: %w", relative, err)
			}
			info, err := root.Stat(filepath.FromSlash(relative))
			if err != nil || !info.Mode().IsRegular() {
				return nil, nil, fmt.Errorf("edit target %s is not a regular file", relative)
			}
			normalized := normalizeHashlineText(raw)
			if actual := computeHashlineTag(normalized); actual != section.tag {
				return nil, nil, fmt.Errorf("[%s#%s] is stale; current tag is %s", relative, section.tag, actual)
			}
			file = &hashlinePreparedFile{source: relative, destination: relative, tag: section.tag, rawOriginal: append([]byte(nil), raw...), original: normalized, originalLines: strings.Split(normalized, "\n"), mode: info.Mode().Perm()}
			byPath[relative] = file
			order = append(order, relative)
		} else if file.tag != section.tag {
			return nil, nil, fmt.Errorf("duplicate section %s uses a different tag", relative)
		}
		for _, operation := range section.ops {
			switch operation.kind {
			case "put":
				action, err := resolveHashlineAction(relative, file.original, file.originalLines, operation.locator, operation.body, operation.sequence)
				if err != nil {
					return nil, nil, err
				}
				file.actions = append(file.actions, action)
			case "cut":
				start, end, err := resolveHashlineRange(relative, file.original, file.originalLines, operation.locator)
				if err != nil {
					return nil, nil, err
				}
				captured := append([]string(nil), file.originalLines[start-1:end]...)
				if operation.register == "" {
					anonymous = captured
				} else {
					pendingRegisters[operation.register] = captured
				}
				file.actions = append(file.actions, hashlineEditAction{start: start, end: end, replace: true, sequence: operation.sequence})
			case "paste":
				var value []string
				var ok bool
				if operation.register == "" {
					value, ok = anonymous, anonymous != nil
				} else if value, ok = pendingRegisters[operation.register]; !ok {
					value, ok = driver.clipboard.load(scope, operation.register)
				}
				if !ok {
					name := "anonymous register"
					if operation.register != "" {
						name = "@" + operation.register
					}
					return nil, nil, fmt.Errorf("%s is empty", name)
				}
				action, err := resolveHashlineAction(relative, file.original, file.originalLines, operation.locator, value, operation.sequence)
				if err != nil {
					return nil, nil, err
				}
				file.actions = append(file.actions, action)
			case "remove":
				if file.move || file.remove || len(file.actions) > 0 || len(section.ops) != 1 {
					return nil, nil, fmt.Errorf("REM must be the only hunk for %s", relative)
				}
				file.remove = true
			case "move":
				if file.move || file.remove {
					return nil, nil, fmt.Errorf("%s has conflicting MV/REM hunks", relative)
				}
				destination, err := workspaceRelativePath(driver.root, operation.dest)
				if err != nil {
					return nil, nil, err
				}
				file.destination, file.move = destination, destination != relative
			}
		}
	}
	prepared := make([]*hashlinePreparedFile, 0, len(order))
	mutated := false
	for _, path := range order {
		file := byPath[path]
		if file.remove {
			file.final = ""
			file.firstChanged = 1
			file.diff = compactHashlineDiff(file.original, "")
			mutated = true
		} else {
			finalLines, err := applyHashlineActions(file.originalLines, file.actions)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %w", path, err)
			}
			file.final = strings.Join(finalLines, "\n")
			file.firstChanged = firstChangedHashlineLine(file.original, file.final)
			file.diff = compactHashlineDiff(file.original, file.final)
			if file.final != file.original || file.move {
				mutated = true
			}
		}
		prepared = append(prepared, file)
	}
	if !mutated && len(pendingRegisters) == 0 {
		return nil, nil, errors.New("patch produced no file or register change")
	}
	if err := validateHashlineDestinations(root, prepared); err != nil {
		return nil, nil, err
	}
	return prepared, pendingRegisters, nil
}

func resolveHashlineAction(path, text string, lines []string, locator hashlineLocator, body []string, sequence int) (hashlineEditAction, error) {
	switch locator.kind {
	case "range", "block":
		start, end, err := resolveHashlineRange(path, text, lines, locator)
		return hashlineEditAction{start: start, end: end, body: append([]string(nil), body...), replace: true, sequence: sequence}, err
	case "before":
		if locator.start > len(lines) {
			return hashlineEditAction{}, fmt.Errorf("line %d is outside %s", locator.start, path)
		}
		return hashlineEditAction{gap: locator.start - 1, body: append([]string(nil), body...), sequence: sequence}, nil
	case "after":
		if locator.start > len(lines) {
			return hashlineEditAction{}, fmt.Errorf("line %d is outside %s", locator.start, path)
		}
		return hashlineEditAction{gap: locator.start, body: append([]string(nil), body...), sequence: sequence}, nil
	case "tail":
		return hashlineEditAction{gap: len(lines), body: append([]string(nil), body...), sequence: sequence}, nil
	case "after_block":
		_, end, err := resolveHashlineBlock(path, text, locator.start)
		return hashlineEditAction{gap: end, body: append([]string(nil), body...), sequence: sequence}, err
	default:
		return hashlineEditAction{}, errors.New("unsupported locator")
	}
}

func resolveHashlineRange(path, text string, lines []string, locator hashlineLocator) (int, int, error) {
	if locator.kind == "range" {
		if locator.start > len(lines) || locator.end > len(lines) {
			return 0, 0, fmt.Errorf("range %d–%d is outside %s", locator.start, locator.end, path)
		}
		return locator.start, locator.end, nil
	}
	if locator.kind == "block" {
		return resolveHashlineBlock(path, text, locator.start)
	}
	return 0, 0, errors.New("locator is not a range or block")
}

func applyHashlineActions(lines []string, actions []hashlineEditAction) ([]string, error) {
	replacements := make(map[int]hashlineEditAction)
	insertions := make(map[int][]hashlineEditAction)
	var ranges []hashlineEditAction
	for _, action := range actions {
		if !action.replace {
			continue
		}
		for _, existing := range ranges {
			if action.start <= existing.end && existing.start <= action.end {
				return nil, fmt.Errorf("overlapping ranges %d–%d and %d–%d", existing.start, existing.end, action.start, action.end)
			}
		}
		ranges = append(ranges, action)
		replacements[action.start] = action
	}
	for _, action := range actions {
		if action.replace {
			continue
		}
		for _, existing := range ranges {
			if action.gap >= existing.start && action.gap < existing.end {
				return nil, fmt.Errorf("insertion gap after line %d lies inside replacement %d–%d", action.gap, existing.start, existing.end)
			}
		}
		insertions[action.gap] = append(insertions[action.gap], action)
	}
	for gap := range insertions {
		sort.SliceStable(insertions[gap], func(left, right int) bool { return insertions[gap][left].sequence < insertions[gap][right].sequence })
	}
	result := make([]string, 0, len(lines))
	appendGap := func(gap int) {
		for _, insertion := range insertions[gap] {
			result = append(result, insertion.body...)
		}
	}
	appendGap(0)
	for line := 1; line <= len(lines); {
		if replacement, ok := replacements[line]; ok {
			result = append(result, replacement.body...)
			line = replacement.end + 1
			appendGap(replacement.end)
			continue
		}
		result = append(result, lines[line-1])
		appendGap(line)
		line++
	}
	return result, nil
}

func validateHashlineDestinations(root *os.Root, prepared []*hashlinePreparedFile) error {
	sources := make(map[string]bool, len(prepared))
	outputs := make(map[string]string)
	for _, file := range prepared {
		sources[file.source] = true
	}
	for _, file := range prepared {
		if file.remove {
			continue
		}
		if source, duplicate := outputs[file.destination]; duplicate {
			return fmt.Errorf("%s and %s both write %s", source, file.source, file.destination)
		}
		outputs[file.destination] = file.source
		if file.destination != file.source {
			if _, err := root.Lstat(filepath.FromSlash(file.destination)); err == nil && !sources[file.destination] {
				return fmt.Errorf("MV destination %s already exists", file.destination)
			} else if err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func (driver *hashlineDriver) commitPatch(ctx context.Context, root *os.Root, prepared []*hashlinePreparedFile) error {
	err := driver.commitPatchLocal(ctx, root, prepared)
	if err == nil || !permissionMutationError(err) {
		return err
	}
	broker := driver.broker.get()
	if broker == nil {
		return err
	}
	return driver.commitPatchBroker(ctx, root, prepared, broker, err)
}

func (driver *hashlineDriver) commitPatchLocal(ctx context.Context, root *os.Root, prepared []*hashlinePreparedFile) error {
	originals := make(map[string]originalFileState, len(prepared))
	outputs := make(map[string]*hashlinePreparedFile)
	for _, file := range prepared {
		current, err := root.ReadFile(filepath.FromSlash(file.source))
		if err != nil || string(current) != string(file.rawOriginal) {
			return fmt.Errorf("%s changed while the patch was prepared", file.source)
		}
		originals[file.source] = originalFileState{data: append([]byte(nil), file.rawOriginal...), mode: file.mode}
		if !file.remove {
			outputs[file.destination] = file
		}
	}
	temporary := make(map[string]string, len(outputs))
	for destination, file := range outputs {
		if err := ctx.Err(); err != nil {
			cleanupHashlineTemps(root, temporary)
			return err
		}
		parent := filepath.Dir(filepath.FromSlash(destination))
		if parent != "." {
			if err := root.MkdirAll(parent, 0o755); err != nil {
				cleanupHashlineTemps(root, temporary)
				return err
			}
		}
		writer, tempPath, err := openRootTemp(root, parent, ".azem-edit", file.mode)
		if err != nil {
			cleanupHashlineTemps(root, temporary)
			return err
		}
		if err := writer.Chmod(file.mode.Perm()); err == nil {
			_, err = writer.Write([]byte(file.final))
		}
		if err == nil {
			err = writer.Sync()
		}
		closeErr := writer.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			_ = root.Remove(tempPath)
			cleanupHashlineTemps(root, temporary)
			return err
		}
		temporary[destination] = tempPath
	}
	var mutated []string
	destinations := make([]string, 0, len(temporary))
	for destination := range temporary {
		destinations = append(destinations, destination)
	}
	sort.Strings(destinations)
	for _, destination := range destinations {
		if err := root.Rename(temporary[destination], filepath.FromSlash(destination)); err != nil {
			rollbackErr := rollbackHashlineFiles(ctx, root, originals, mutated)
			cleanupHashlineTemps(root, temporary)
			return combineRollbackError(err, rollbackErr)
		}
		mutated = append(mutated, destination)
		delete(temporary, destination)
	}
	for _, file := range prepared {
		if outputs[file.source] != nil {
			continue
		}
		if err := root.Remove(filepath.FromSlash(file.source)); err != nil {
			rollbackErr := rollbackHashlineFiles(ctx, root, originals, mutated)
			return combineRollbackError(err, rollbackErr)
		}
		mutated = append(mutated, file.source)
	}
	return nil
}

func (driver *hashlineDriver) commitPatchBroker(ctx context.Context, root *os.Root, prepared []*hashlinePreparedFile, broker FileMutationBroker, originalCause error) error {
	originals := make(map[string]originalFileState, len(prepared))
	outputs := make(map[string]*hashlinePreparedFile)
	for _, file := range prepared {
		current, err := root.ReadFile(filepath.FromSlash(file.source))
		if err != nil || string(current) != string(file.rawOriginal) {
			return fmt.Errorf("%s changed while the brokered patch was prepared", file.source)
		}
		originals[file.source] = originalFileState{data: append([]byte(nil), file.rawOriginal...), mode: file.mode}
		if !file.remove {
			outputs[file.destination] = file
		}
	}
	writeTargets := make(map[string]string, len(outputs))
	for destination := range outputs {
		resolved, err := brokerDestination(driver.root, destination, true)
		if err != nil {
			return originalCause
		}
		writeTargets[destination] = resolved
	}
	deleteTargets := make(map[string]string)
	for _, file := range prepared {
		if outputs[file.source] != nil {
			continue
		}
		resolved, err := brokerDestination(driver.root, file.source, false)
		if err != nil {
			return originalCause
		}
		deleteTargets[file.source] = resolved
	}
	destinations := make([]string, 0, len(outputs))
	for destination := range outputs {
		destinations = append(destinations, destination)
	}
	sort.Strings(destinations)
	mutated := make([]string, 0, len(destinations)+len(deleteTargets))
	sessionID := brokerCallerSession(ctx)
	fail := func() error {
		if rollbackErr := rollbackBrokeredHashlineFiles(ctx, broker, driver.root, originals, mutated, originalCause, sessionID); rollbackErr != nil {
			return combineRollbackError(originalCause, rollbackErr)
		}
		return originalCause
	}
	for _, destination := range destinations {
		if err := ctx.Err(); err != nil {
			return err
		}
		handled, _ := broker.BrokerWrite(ctx, writeTargets[destination], []byte(outputs[destination].final), originalCause, sessionID)
		if !handled {
			return fail()
		}
		mutated = append(mutated, destination)
	}
	sources := make([]string, 0, len(deleteTargets))
	for source := range deleteTargets {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	for _, source := range sources {
		handled, _ := broker.BrokerDelete(ctx, deleteTargets[source], originalCause, sessionID, true)
		if !handled {
			return fail()
		}
		mutated = append(mutated, source)
	}
	return nil
}

func rollbackBrokeredHashlineFiles(ctx context.Context, broker FileMutationBroker, rootPath string, originals map[string]originalFileState, mutated []string, cause error, sessionID string) error {
	var failures []string
	seen := make(map[string]bool, len(mutated))
	for index := len(mutated) - 1; index >= 0; index-- {
		path := mutated[index]
		if seen[path] {
			continue
		}
		seen[path] = true
		original, existed := originals[path]
		if existed {
			destination, err := brokerDestination(rootPath, path, true)
			if err == nil {
				handled, brokerErr := broker.BrokerWrite(ctx, destination, original.data, cause, sessionID)
				if handled && brokerErr == nil {
					continue
				}
			}
		} else {
			destination, err := brokerDestination(rootPath, path, false)
			if err == nil {
				handled, brokerErr := broker.BrokerDelete(ctx, destination, cause, sessionID, true)
				if handled && brokerErr == nil {
					continue
				}
			}
		}
		failures = append(failures, path)
	}
	if len(failures) > 0 {
		return fmt.Errorf("extension broker could not restore %s", strings.Join(failures, ", "))
	}
	return nil
}

func cleanupHashlineTemps(root *os.Root, temporary map[string]string) {
	for _, path := range temporary {
		_ = root.Remove(path)
	}
}

func rollbackHashlineFiles(ctx context.Context, root *os.Root, originals map[string]originalFileState, mutated []string) error {
	var failures []string
	seen := make(map[string]bool)
	for _, path := range mutated {
		if seen[path] {
			continue
		}
		seen[path] = true
		original, existed := originals[path]
		if !existed {
			if err := root.Remove(filepath.FromSlash(path)); err != nil && !os.IsNotExist(err) {
				failures = append(failures, path+": "+err.Error())
			}
			continue
		}
		_, statErr := root.Stat(filepath.FromSlash(path))
		exists := statErr == nil
		if statErr != nil && !os.IsNotExist(statErr) {
			failures = append(failures, path+": "+statErr.Error())
			continue
		}
		if _, err := writeRootedFile(ctx, root, filepath.FromSlash(path), original.data, original.mode, exists, false); err != nil {
			failures = append(failures, path+": "+err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func combineRollbackError(commit, rollback error) error {
	if rollback == nil {
		return commit
	}
	return fmt.Errorf("commit failed: %v; rollback failed: %w", commit, rollback)
}

func (driver *hashlineDriver) buildHashlineResult(ctx context.Context, prepared []*hashlinePreparedFile) EditHashlineResult {
	sections := make([]EditSectionResult, 0, len(prepared))
	var content strings.Builder
	for index, file := range prepared {
		operation := "put"
		path := file.destination
		newTag, header := "", ""
		if file.remove {
			operation, path = "remove", file.source
		} else {
			newTag, header = driver.recordHashlineSnapshot(ctx, path, file.final)
			if file.move {
				operation = "move"
			}
		}
		if index > 0 {
			content.WriteString("\n\n")
		}
		if header != "" {
			content.WriteString(header + "\n")
		}
		switch operation {
		case "remove":
			content.WriteString("removed " + path)
		case "move":
			content.WriteString("moved " + file.source + " to " + path)
		default:
			content.WriteString("updated " + path)
		}
		if file.firstChanged > 0 {
			content.WriteString("\nfirstChangedLine: " + strconv.Itoa(file.firstChanged))
		}
		if file.diff != "" {
			content.WriteString("\n\n--- compact diff ---\n" + file.diff)
		}
		sections = append(sections, EditSectionResult{Path: path, Op: operation, OldTag: file.tag, NewTag: newTag, Header: header, FirstChangedLine: file.firstChanged, Diff: file.diff})
	}
	result := EditHashlineResult{Sections: sections, Content: content.String()}
	for _, section := range sections {
		result.OldTags = append(result.OldTags, section.OldTag)
		result.NewTags = append(result.NewTags, section.NewTag)
		result.FirstChangedLines = append(result.FirstChangedLines, section.FirstChangedLine)
	}
	hash := sha256.New()
	for _, section := range sections {
		_, _ = hash.Write([]byte(section.Path + "\x00" + section.Diff + "\x00"))
	}
	result.DiffHash = hex.EncodeToString(hash.Sum(nil))
	return result
}

func (driver *hashlineDriver) recordHashlineSnapshot(ctx context.Context, path, content string) (string, string) {
	tag := computeHashlineTag(content)
	if driver.snapshotRead != nil {
		arguments, _ := json.Marshal(map[string]any{"path": path, "startLine": 1, "endLine": 1})
		result, err := driver.snapshotRead.Execute(ctx, tool.Call{ID: "edit-snapshot", Name: ToolReadFile, Arguments: arguments}, nil)
		if err == nil && !result.IsError {
			var observed ReadFileToolResult
			if json.Unmarshal(result.Structured, &observed) == nil && observed.Tag != "" {
				tag = observed.Tag
			}
		}
	}
	return tag, "[" + path + "#" + tag + "]"
}

func normalizeHashlineText(payload []byte) string {
	text := string(payload)
	text = strings.TrimPrefix(text, "\ufeff")
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
}

func computeHashlineTag(text string) string {
	lines := strings.Split(text, "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], " \t\r")
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(strings.Join(lines, "\n")))
	return fmt.Sprintf("%04X", uint16(hash.Sum32()&0xffff))
}

func compactHashlineDiff(before, after string) string {
	if before == after {
		return ""
	}
	oldLines := strings.Split(before, "\n")
	newLines := strings.Split(after, "\n")
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(oldLines)-prefix && suffix < len(newLines)-prefix && oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
		suffix++
	}
	var output strings.Builder
	for _, line := range oldLines[prefix : len(oldLines)-suffix] {
		output.WriteString("-" + line + "\n")
	}
	for _, line := range newLines[prefix : len(newLines)-suffix] {
		output.WriteString("+" + line + "\n")
	}
	return strings.TrimSuffix(output.String(), "\n")
}

func firstChangedHashlineLine(before, after string) int {
	if before == after {
		return 0
	}
	oldLines := strings.Split(before, "\n")
	newLines := strings.Split(after, "\n")
	limit := min(len(oldLines), len(newLines))
	for index := 0; index < limit; index++ {
		if oldLines[index] != newLines[index] {
			return index + 1
		}
	}
	return limit + 1
}

func hashlineClipboardScope(ctx context.Context, root string) string {
	caller, _ := InvocationFromContext(ctx)
	for _, value := range []string{caller.SessionID, caller.TeamRunID, caller.AgentID} {
		if value != "" {
			return value
		}
	}
	return root
}

func hashlineError(call tool.Call, err error) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "edit_hashline rejected: " + err.Error() + ". Re-read affected lines and use their current [PATH#TAG].", IsError: true}
}

func joinHashlineInts(values []int) string {
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = strconv.Itoa(value)
	}
	return strings.Join(parts, ",")
}

func resolveHashlineBlock(path, text string, line int) (int, int, error) {
	lines := strings.Split(text, "\n")
	if line <= 0 || line > len(lines) {
		return 0, 0, fmt.Errorf("block line %d is outside %s", line, path)
	}
	extension := strings.ToLower(filepath.Ext(path))
	if extension == ".go" {
		if start, end, ok := resolveGoHashlineBlock(path, text, line); ok {
			return start, end, nil
		}
	}
	if extension == ".md" || extension == ".markdown" {
		if start, end, ok := resolveMarkdownHashlineBlock(lines, line); ok {
			return start, end, nil
		}
	}
	if extension == ".py" || extension == ".yaml" || extension == ".yml" {
		if start, end, ok := resolveIndentedHashlineBlock(lines, line); ok {
			return start, end, nil
		}
	}
	if start, end, ok := resolveBraceHashlineBlock(lines, line); ok {
		return start, end, nil
	}
	if start, end, ok := resolveIndentedHashlineBlock(lines, line); ok {
		return start, end, nil
	}
	return 0, 0, fmt.Errorf("line %d does not begin a multi-line syntactic block in %s", line, path)
}

func resolveGoHashlineBlock(path, text string, line int) (int, int, bool) {
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, path, text, parser.ParseComments|parser.AllErrors)
	if parsed == nil || err != nil && len(parsed.Decls) == 0 {
		return 0, 0, false
	}
	bestStart, bestEnd, bestSpan := 0, 0, int(^uint(0)>>1)
	ast.Inspect(parsed, func(node ast.Node) bool {
		if node == nil || !hashlineBlockNode(node) {
			return true
		}
		start := files.Position(node.Pos()).Line
		end := files.Position(node.End()).Line
		if start == line && end > start && end-start < bestSpan {
			bestStart, bestEnd, bestSpan = start, end, end-start
		}
		return true
	})
	return bestStart, bestEnd, bestStart > 0
}

func hashlineBlockNode(node ast.Node) bool {
	switch node.(type) {
	case *ast.FuncDecl, *ast.GenDecl, *ast.TypeSpec, *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt, *ast.BlockStmt, *ast.CompositeLit:
		return true
	default:
		return false
	}
}

func resolveMarkdownHashlineBlock(lines []string, line int) (int, int, bool) {
	trimmed := strings.TrimSpace(lines[line-1])
	if !strings.HasPrefix(trimmed, "#") {
		return 0, 0, false
	}
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level == len(trimmed) || trimmed[level] != ' ' {
		return 0, 0, false
	}
	end := len(lines)
	for index := line; index < len(lines); index++ {
		candidate := strings.TrimSpace(lines[index])
		candidateLevel := 0
		for candidateLevel < len(candidate) && candidate[candidateLevel] == '#' {
			candidateLevel++
		}
		if candidateLevel > 0 && candidateLevel <= level && candidateLevel < len(candidate) && candidate[candidateLevel] == ' ' {
			end = index
			break
		}
	}
	return line, end, end > line
}

func resolveIndentedHashlineBlock(lines []string, line int) (int, int, bool) {
	start := line - 1
	construct := start
	for construct < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[construct]), "@") {
		construct++
	}
	if construct >= len(lines) {
		return 0, 0, false
	}
	trimmed := strings.TrimSpace(lines[construct])
	if !strings.HasSuffix(trimmed, ":") && !strings.HasSuffix(trimmed, "|") && !strings.HasSuffix(trimmed, ">") {
		return 0, 0, false
	}
	indent := leadingIndent(lines[construct])
	end := construct + 1
	for end < len(lines) {
		candidate := strings.TrimSpace(lines[end])
		if candidate == "" {
			end++
			continue
		}
		if leadingIndent(lines[end]) <= indent {
			break
		}
		end++
	}
	for end > construct+1 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	if end <= construct+1 {
		return 0, 0, false
	}
	return line, end, true
}

func leadingIndent(line string) int {
	count := 0
	for _, current := range line {
		if current == ' ' {
			count++
		} else if current == '\t' {
			count += 4
		} else {
			break
		}
	}
	return count
}

func resolveBraceHashlineBlock(lines []string, line int) (int, int, bool) {
	depth := 0
	opened := false
	quote := rune(0)
	escaped := false
	blockComment := false
	for lineIndex := line - 1; lineIndex < len(lines); lineIndex++ {
		current := []rune(lines[lineIndex])
		for index := 0; index < len(current); index++ {
			character := current[index]
			if blockComment {
				if character == '*' && index+1 < len(current) && current[index+1] == '/' {
					blockComment = false
					index++
				}
				continue
			}
			if quote != 0 {
				if escaped {
					escaped = false
				} else if character == '\\' {
					escaped = true
				} else if character == quote {
					quote = 0
				}
				continue
			}
			if character == '/' && index+1 < len(current) && current[index+1] == '/' {
				break
			}
			if character == '/' && index+1 < len(current) && current[index+1] == '*' {
				blockComment = true
				index++
				continue
			}
			if character == '\'' || character == '"' || character == '`' {
				quote = character
				continue
			}
			if !opened && (character == '}' || character == ';') {
				return 0, 0, false
			}
			if character == '{' {
				opened = true
				depth++
			} else if character == '}' && opened {
				depth--
				if depth == 0 {
					return line, lineIndex + 1, lineIndex+1 > line
				}
			}
		}
		if !opened && lineIndex-line >= 8 {
			break
		}
	}
	return 0, 0, false
}
