package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/venat/tool"
)

func TestOMPHashlineAppliesBlocksRegistersMovesAndRemovals(t *testing.T) {
	ctx := WithInvocation(context.Background(), Invocation{SessionID: "hashline-session"})
	root := t.TempDir()
	service := newWriteTestService(t, ctx, root)
	edit := findWorkspaceTool(t, service, root, ToolEditHashline)

	writeTestFile(t, filepath.Join(root, "source.py"), "def greet():\n    return 'hi'\n\nprint(greet())\n")
	writeTestFile(t, filepath.Join(root, "target.py"), "# target\n")
	writeTestFile(t, filepath.Join(root, "move.txt"), "old\n")
	writeTestFile(t, filepath.Join(root, "remove.txt"), "remove\n")

	patch := "*** Begin Patch\n" +
		sectionHeader("source.py", "def greet():\n    return 'hi'\n\nprint(greet())\n") + "\n" +
		"CUT 1* @greet\n" +
		sectionHeader("target.py", "# target\n") + "\n" +
		"PUT >1 @greet\n" +
		sectionHeader("move.txt", "old\n") + "\n" +
		"PUT 1.=1:\n+new\nMV \"moved file.txt\"\n" +
		sectionHeader("remove.txt", "remove\n") + "\nREM\n" +
		"*** End Patch\n"
	result := executeHashline(t, ctx, edit, patch)
	if !strings.Contains(result.Content, "[target.py#") || !strings.Contains(result.Content, "moved move.txt to moved file.txt") || !strings.Contains(result.Content, "removed remove.txt") {
		t.Fatalf("edit result = %#v", result)
	}
	assertFileContent(t, filepath.Join(root, "source.py"), "\nprint(greet())\n")
	assertFileContent(t, filepath.Join(root, "target.py"), "# target\ndef greet():\n    return 'hi'\n")
	assertFileContent(t, filepath.Join(root, "moved file.txt"), "new\n")
	if _, err := os.Stat(filepath.Join(root, "move.txt")); !os.IsNotExist(err) {
		t.Fatalf("move source remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "remove.txt")); !os.IsNotExist(err) {
		t.Fatalf("removed file remains: %v", err)
	}

	writeTestFile(t, filepath.Join(root, "later.py"), "# later\n")
	persistent := "*** Begin Patch\n" + sectionHeader("later.py", "# later\n") + "\nPUT 1.=1 @greet\n*** End Patch\n"
	executeHashline(t, ctx, edit, persistent)
	assertFileContent(t, filepath.Join(root, "later.py"), "def greet():\n    return 'hi'\n")
}

func TestOMPHashlineSupportsOriginalLineGapsAndMarkdownBlocks(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	service := newWriteTestService(t, ctx, root)
	edit := findWorkspaceTool(t, service, root, ToolEditHashline)
	content := "# One\na\n## Child\nb\n# Two\nc\n"
	writeTestFile(t, filepath.Join(root, "notes.md"), content)
	patch := "*** Begin Patch\n" + sectionHeader("notes.md", content) + "\n" +
		"PUT 1*:\n+# First\n+x\n+\n" +
		"PUT >5:\n+between\n" +
		"*** End Patch\n"
	executeHashline(t, ctx, edit, patch)
	assertFileContent(t, filepath.Join(root, "notes.md"), "# First\nx\n\n# Two\nbetween\nc\n")
}

func TestOMPHashlineSupportsAnonymousMovesAndAfterBlockInsertion(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	service := newWriteTestService(t, ctx, root)
	edit := findWorkspaceTool(t, service, root, ToolEditHashline)
	source := "function one() {\n  return 1;\n}\nconst tail = 2;\n"
	writeTestFile(t, filepath.Join(root, "move.ts"), source)
	executeHashline(t, ctx, edit, "*** Begin Patch\n"+sectionHeader("move.ts", source)+"\nCUT 1*\nPUT >4\n*** End Patch\n")
	assertFileContent(t, filepath.Join(root, "move.ts"), "const tail = 2;\nfunction one() {\n  return 1;\n}\n")

	markdown := "# A\na\n# B\nb\n"
	writeTestFile(t, filepath.Join(root, "after.md"), markdown)
	executeHashline(t, ctx, edit, "*** Begin Patch\n"+sectionHeader("after.md", markdown)+"\nPUT >1*:\n+between\n+\n*** End Patch\n")
	assertFileContent(t, filepath.Join(root, "after.md"), "# A\na\nbetween\n\n# B\nb\n")
}

func TestOMPHashlineRejectsStaleTagsOverlapAndExistingMoveDestinationWithoutMutation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	service := newWriteTestService(t, ctx, root)
	edit := findWorkspaceTool(t, service, root, ToolEditHashline)
	writeTestFile(t, filepath.Join(root, "a.txt"), "one\ntwo\n")
	writeTestFile(t, filepath.Join(root, "occupied.txt"), "occupied\n")

	stale := callHashline(ctx, edit, "*** Begin Patch\n[a.txt#0000]\nPUT 1.=1:\n+ONE\n*** End Patch\n")
	if !stale.IsError || !strings.Contains(stale.Content, "stale") {
		t.Fatalf("stale edit = %#v", stale)
	}
	overlap := callHashline(ctx, edit, "*** Begin Patch\n"+sectionHeader("a.txt", "one\ntwo\n")+"\nPUT 1.=2:\n+all\nPUT 2.=2:\n+TWO\n*** End Patch\n")
	if !overlap.IsError || !strings.Contains(overlap.Content, "overlapping ranges") {
		t.Fatalf("overlap edit = %#v", overlap)
	}
	move := callHashline(ctx, edit, "*** Begin Patch\n"+sectionHeader("a.txt", "one\ntwo\n")+"\nMV occupied.txt\n*** End Patch\n")
	if !move.IsError || !strings.Contains(move.Content, "already exists") {
		t.Fatalf("occupied move = %#v", move)
	}
	assertFileContent(t, filepath.Join(root, "a.txt"), "one\ntwo\n")
	assertFileContent(t, filepath.Join(root, "occupied.txt"), "occupied\n")
}

func sectionHeader(path, content string) string {
	return "[" + path + "#" + computeHashlineTag(content) + "]"
}

func executeHashline(t *testing.T, ctx context.Context, driver tool.Driver, patch string) tool.Result {
	t.Helper()
	result := callHashline(ctx, driver, patch)
	if result.IsError {
		t.Fatalf("hashline edit failed: %s", result.Content)
	}
	return result
}

func callHashline(ctx context.Context, driver tool.Driver, patch string) tool.Result {
	arguments, _ := json.Marshal(map[string]string{"input": patch})
	result, err := driver.Execute(ctx, tool.Call{ID: "edit", Name: ToolEditHashline, Arguments: arguments}, nil)
	if err != nil {
		return tool.Result{IsError: true, Content: err.Error()}
	}
	return result
}

func ompTestPatch(header, hunks string) string {
	return "*** Begin Patch\n" + header + "\n" + hunks + "\n*** End Patch\n"
}
