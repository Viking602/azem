package agentruntime

import "testing"

func TestExecutionIDUsesVersionedStableNamespace(t *testing.T) {
	t.Parallel()

	got, err := ExecutionID("main", "run-123", 4)
	if err != nil {
		t.Fatal(err)
	}
	if want := "azem:v1:main:run-123:4"; got != want {
		t.Fatalf("ExecutionID() = %q, want %q", got, want)
	}

	for _, test := range []struct {
		name     string
		kind     string
		stableID string
		segment  int
	}{
		{name: "empty kind", stableID: "run", segment: 0},
		{name: "empty stable id", kind: "main", segment: 0},
		{name: "negative segment", kind: "main", stableID: "run", segment: -1},
		{name: "kind delimiter", kind: "main:child", stableID: "run", segment: 0},
		{name: "stable id delimiter", kind: "main", stableID: "run/child", segment: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ExecutionID(test.kind, test.stableID, test.segment); err == nil {
				t.Fatal("ExecutionID() error = nil")
			}
		})
	}
}
