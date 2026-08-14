package app

import (
	"context"
	"strings"
	"testing"
)

// TestActionRegistryCoversEveryActionKind pins the handler registry to the
// contract list: every declared ActionKind has exactly one handler and the
// registry contains no unknown kinds, so the dispatch table can never drift
// from the Go contract source (and therefore from the generated TypeScript).
func TestActionRegistryCoversEveryActionKind(t *testing.T) {
	declared := make(map[ActionKind]bool, len(AllActionKinds()))
	for _, kind := range AllActionKinds() {
		declared[kind] = true
	}
	for kind := range actionRegistry {
		if !declared[kind] {
			t.Errorf("action registry handles %q which is not part of AllActionKinds", kind)
		}
	}
	for kind := range declared {
		if _, ok := actionRegistry[kind]; !ok {
			t.Errorf("ActionKind %q has no registered handler", kind)
		}
	}
}

func TestExecuteActionRejectsUnknownKind(t *testing.T) {
	service := &Service{}
	err := service.ExecuteAction(context.Background(), Action{Kind: ActionKind("bogus_action")})
	if err == nil || !strings.Contains(err.Error(), `unsupported action "bogus_action"`) {
		t.Fatalf("expected unsupported action error, got %v", err)
	}
}
