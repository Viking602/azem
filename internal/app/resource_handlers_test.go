package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Viking602/azem/internal/resource"
	"github.com/Viking602/azem/internal/rules"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestArtifactResourceHandlerEnforcesSessionOwnership(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "azem.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(ctx) })
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "one", Title: "One"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Ensure(ctx, session.Session{ID: "two", Title: "Two"}); err != nil {
		t.Fatal(err)
	}
	artifact, err := sessions.PutArtifact(ctx, "one", "run", "tool_result", []byte("payload"), "preview")
	if err != nil {
		t.Fatal(err)
	}
	router, err := buildResourceRouter(sessions, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := router.Read(ctx, "artifact://"+artifact.ID, "", resource.Scope{SessionID: "one"})
	if err != nil || string(result.Data) != "payload" || result.Metadata["sha256"] != artifact.SHA256 {
		t.Fatalf("artifact resource = %#v, %v", result, err)
	}
	if _, err := router.Read(ctx, "artifact://"+artifact.ID, "", resource.Scope{SessionID: "two"}); err == nil {
		t.Fatal("cross-session artifact read succeeded")
	}
}

func TestSkillResourceHandlerRequiresActivation(t *testing.T) {
	router, err := buildResourceRouter(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := router.Read(
		context.Background(),
		"skill://review/references/guide.md",
		"",
		resource.Scope{},
	); err == nil {
		t.Fatal("inactive Skill resource read succeeded")
	}
}

func TestRuleResourceHandlerReadsActiveDiscoveredRule(t *testing.T) {
	catalog := rules.NewCatalog(rules.Result{Rules: []rules.Rule{{
		Name: "go-safety", Path: "/workspace/.cursor/rules/go-safety.mdc", Provider: "cursor",
		Content: "Use the safe API.", Description: "Go safety",
	}}})
	router, err := buildResourceRouter(nil, nil, catalog)
	if err != nil {
		t.Fatal(err)
	}
	result, err := router.Read(context.Background(), "rule://go-safety", "", resource.Scope{})
	if err != nil || string(result.Data) != "Use the safe API." || result.Metadata["provider"] != "cursor" {
		t.Fatalf("rule resource = %#v, %v", result, err)
	}
	if _, err := router.Read(context.Background(), "rule://missing", "", resource.Scope{}); err == nil {
		t.Fatal("unknown rule read succeeded")
	}
}
