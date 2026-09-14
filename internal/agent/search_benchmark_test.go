package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Viking602/venat/tool"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func BenchmarkSearchManySourceFiles(b *testing.B) {
	root := b.TempDir()
	for i := 0; i < 100; i++ {
		content := "package source\n// SearchBenchmarkNeedle\n" + strings.Repeat("// ordinary source line with no match\n", 2000)
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("source%03d.go", i)), []byte(content), 0600); err != nil {
			b.Fatal(err)
		}
	}
	driver := newReliableSearchDriver(root, snapshotReadDriver{workspace: NewLocalWorkspace(root)}, nil)
	call := tool.Call{ID: "bench", Name: ToolSearch, Arguments: json.RawMessage(`{"query":"SearchBenchmarkNeedle"}`)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := driver.Execute(context.Background(), call, nil)
		if err != nil || result.IsError {
			b.Fatalf("search: %v %s", err, result.Content)
		}
	}
}
