package session

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildArtifactPreviewV2IncludesBoundedDiagnostics(t *testing.T) {
	payload := []byte(strings.Repeat("prefix\n", 400) + "warning: inspect this\nerror: failed here\n" + strings.Repeat("tail\n", 400))
	var preview ArtifactPreviewV2
	if err := json.Unmarshal([]byte(BuildArtifactPreviewV2("tool_result", payload, "digest", "hint")), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Version != 2 || preview.Bytes != len(payload) || preview.Encoding != "utf-8" || !preview.Truncated || preview.SHA256 != "digest" {
		t.Fatalf("preview=%+v", preview)
	}
	if len(preview.Errors) != 1 || len(preview.Warnings) != 1 || len(preview.Head) > 2048 || len(preview.Tail) > 2048 {
		t.Fatalf("diagnostics/head/tail=%+v", preview)
	}
}
