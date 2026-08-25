package toolview

import "testing"

func TestCompletedFileChangesFromStructuredSections(t *testing.T) {
	structured := `{"sections":[{"path":"a.go","firstChangedLine":10,"diff":"-old\n+new\n+more"},{"path":"b.go","diff":"+only"}]}`
	summary, ok := CompletedFileChanges("coding.edit_hashline", "", structured, "")
	if !ok {
		t.Fatal("expected a projection for structured hashline sections")
	}
	if len(summary.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(summary.Files))
	}
	first := summary.Files[0]
	if first.Path != "a.go" || first.FirstChangedLine != 10 || first.Additions != 2 || first.Deletions != 1 {
		t.Fatalf("unexpected first file projection: %+v", first)
	}
	if summary.Files[1].FirstChangedLine != 1 {
		t.Fatalf("missing firstChangedLine must default to 1, got %d", summary.Files[1].FirstChangedLine)
	}
	if summary.Additions != 3 || summary.Deletions != 1 {
		t.Fatalf("unexpected totals: +%d/-%d", summary.Additions, summary.Deletions)
	}
}

func TestCompletedFileChangesFallsBackToCompactOutput(t *testing.T) {
	output := "[main.go#a1b2]\nfirstChangedLine: 7\n--- compact diff ---\n-return nil\n+return err\n"
	summary, ok := CompletedFileChanges("coding.edit_hashline", "", "not json", output)
	if !ok || len(summary.Files) != 1 {
		t.Fatalf("expected compact fallback projection, got ok=%v files=%d", ok, len(summary.Files))
	}
	file := summary.Files[0]
	if file.Path != "main.go" || file.FirstChangedLine != 7 || file.Additions != 1 || file.Deletions != 1 {
		t.Fatalf("unexpected compact projection: %+v", file)
	}
}

func TestCompletedFileChangesFromWriteArguments(t *testing.T) {
	summary, ok := CompletedFileChanges("coding.write_file", `{"path":"notes.md","content":"one\ntwo\n"}`, "", "")
	if !ok || len(summary.Files) != 1 {
		t.Fatalf("expected write projection, got ok=%v", ok)
	}
	file := summary.Files[0]
	if file.Diff != "+one\n+two" || file.Additions != 2 || file.Deletions != 0 {
		t.Fatalf("unexpected write projection: %+v", file)
	}
}

func TestCompletedFileChangesPreservesEmptyWrite(t *testing.T) {
	summary, ok := CompletedFileChanges("coding.write_file", `{"path":"empty.txt","content":""}`, "", "")
	if !ok || len(summary.Files) != 1 || summary.Files[0].Diff != "" || summary.Additions != 0 {
		t.Fatalf("empty writes must keep an empty-diff file entry: ok=%v %+v", ok, summary)
	}
}

func TestCompletedFileChangesIncludesReplaceAndDelete(t *testing.T) {
	replace, ok := CompletedFileChanges("coding.replace", `{"path":"a.go"}`, `{"sections":[{"path":"a.go","diff":"-old\n+new"}]}`, "")
	if !ok || len(replace.Files) != 1 || replace.Files[0].Path != "a.go" || replace.Additions != 1 || replace.Deletions != 1 {
		t.Fatalf("replace summary = %+v, ok=%v", replace, ok)
	}
	deleted, ok := CompletedFileChanges("coding.delete_file", `{"path":"gone.txt"}`, "", `{"path":"gone.txt","size":4}`)
	if !ok || len(deleted.Files) != 1 || deleted.Files[0].Path != "gone.txt" {
		t.Fatalf("delete summary = %+v, ok=%v", deleted, ok)
	}
	if !IsFileChangeTool("coding.replace") || !IsFileChangeTool("coding.delete_file") {
		t.Fatal("replace/delete were not classified as file-change tools")
	}
}

func TestCompletedFileChangesIgnoresOtherTools(t *testing.T) {
	if _, ok := CompletedFileChanges("shell.execute", `{"command":"ls"}`, "", ""); ok {
		t.Fatal("non file-change tools must not project file changes")
	}
	if _, ok := CompletedFileChanges("coding.write_file", `{"content":"x"}`, "", ""); ok {
		t.Fatal("a write without a path must not project a change")
	}
}

func TestSummaryWireRoundTrip(t *testing.T) {
	summary, _ := CompletedFileChanges("coding.write_file", `{"path":"a","content":"x\n"}`, "", "")
	decoded, ok := DecodeSummary(EncodeSummary(summary))
	if !ok || len(decoded.Files) != 1 || decoded.Files[0].Path != "a" {
		t.Fatalf("wire round trip failed: ok=%v %+v", ok, decoded)
	}
	if _, ok := DecodeSummary(""); ok {
		t.Fatal("empty payload must not decode")
	}
	if _, ok := DecodeSummary("{"); ok {
		t.Fatal("malformed payload must not decode")
	}
}
