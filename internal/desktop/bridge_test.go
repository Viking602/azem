package desktop

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	azemapp "github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/azem/internal/skills"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestCurrentGitBranch(t *testing.T) {
	root := t.TempDir()
	if output, err := exec.Command("git", "-C", root, "init", "--initial-branch=main").CombinedOutput(); err != nil {
		t.Fatalf("init git repository: %v: %s", err, output)
	}
	if got := currentGitBranch(context.Background(), root); got != "main" {
		t.Fatalf("currentGitBranch() = %q, want main", got)
	}
}

func TestBridgeSkillCatalogDirectReadback(t *testing.T) {
	home := t.TempDir()
	skillDir := filepath.Join(home, ".agents", "skills", "shared-review")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: shared-review\ndescription: Shared review\n---\nReview carefully.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, err := skills.Load(skills.LoadOptions{HomeDir: home, Config: config.SkillsConfig{Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	runtime := azemapp.NewService(context.Background(), config.Default())
	runtime.AttachSkills(catalog)
	snapshot, err := (&Bridge{runtime: runtime}).SkillCatalog()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range snapshot.Entries {
		if entry.Name == "shared-review" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("shared .agents skill missing from direct snapshot: %#v", snapshot.Entries)
	}
}

func TestBridgeSearchSessionsReturnsBoundedReadOnlyResults(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session-search", Title: "Indexed task"}); err != nil {
		t.Fatal(err)
	}
	sequence, err := sessions.AppendBlock(ctx, "session-search", session.Block{Kind: "user", Content: "bridge searchable content"})
	if err != nil {
		t.Fatal(err)
	}
	runtime := azemapp.NewService(ctx, config.Default())
	runtime.AttachDurable(sessions, nil)
	bridge := &Bridge{runtime: runtime, ctx: ctx}
	results, err := bridge.SearchSessions("searchable", 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Sequence != sequence || results[0].Preview == "" {
		t.Fatalf("search results = %+v", results)
	}
}

func TestBridgeResumeSessionReturnsDurableProjectionDirectly(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "resume.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session-resume", Title: "Resume target"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.AppendBlock(ctx, "session-resume", session.Block{Kind: "user", Content: "durable search target"}); err != nil {
		t.Fatal(err)
	}
	runtime := azemapp.NewService(ctx, config.Default())
	runtime.AttachDurable(sessions, nil)
	bridge := &Bridge{runtime: runtime, ctx: ctx}
	event, err := bridge.ResumeSession("session-resume")
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != string(azemapp.EventSessionLoaded) || event.SessionID != "session-resume" || !strings.Contains(event.Data["blocks"], "durable search target") {
		t.Fatalf("direct resume projection = %+v", event)
	}
}

func TestBridgeInitialiseAndEventProjection(t *testing.T) {
	cfg := config.Default()
	runtime := azemapp.NewService(context.Background(), cfg)
	runtime.Bootstrap()
	events := make(chan Event, 16)
	bridge := NewBridge(context.Background(), azemapp.BootstrapResult{
		Config: cfg, SessionID: "session-test", Service: runtime,
	}, func(_ string, data ...any) bool {
		events <- data[0].(Event)
		return false
	}, nil)
	t.Cleanup(bridge.Close)

	snapshot := bridge.Initialise()
	if snapshot.SessionID != "session-test" || snapshot.Model != cfg.Defaults.Model || snapshot.QueueMode != "queue" {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	select {
	case event := <-events:
		if event.Kind != string(azemapp.EventBootstrapDone) || event.Sequence == 0 {
			t.Fatalf("unexpected event: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for projected event")
	}
}

func TestDesktopAttachmentRoundTrip(t *testing.T) {
	input := []Attachment{{ID: "image-1", Name: "screen.png", MIMEType: "image/png", Path: "/tmp/screen.png", Size: 42}}
	converted := attachmentsToSession(input)
	if len(converted) != 1 || converted[0].MIME != "image/png" {
		t.Fatalf("unexpected session attachment: %#v", converted)
	}
	if got := attachmentFromSession(converted[0]); got != input[0] {
		t.Fatalf("unexpected desktop attachment: %#v", got)
	}
}

func TestImportClipboardImage(t *testing.T) {
	previous := readClipboardImage
	readClipboardImage = func() ([]byte, string, error) {
		return []byte{
			0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
			0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		}, "image/png", nil
	}
	t.Cleanup(func() { readClipboardImage = previous })

	runtime := azemapp.NewService(context.Background(), config.Default())
	runtime.AttachAttachments(filepath.Join(t.TempDir(), "attachments"))
	bridge := &Bridge{runtime: runtime}

	attachment, err := bridge.ImportClipboardImage("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if attachment == nil || attachment.MIMEType != "image/png" || !strings.HasPrefix(attachment.Name, "pasted-image-") {
		t.Fatalf("unexpected clipboard attachment: %#v", attachment)
	}
	if _, err := os.Stat(attachment.Path); err != nil {
		t.Fatalf("clipboard attachment was not stored: %v", err)
	}
}

func TestImportClipboardImageReturnsNilWhenClipboardHasNoImage(t *testing.T) {
	previous := readClipboardImage
	readClipboardImage = func() ([]byte, string, error) { return nil, "", nil }
	t.Cleanup(func() { readClipboardImage = previous })

	attachment, err := (&Bridge{}).ImportClipboardImage("session-1")
	if err != nil || attachment != nil {
		t.Fatalf("attachment = %#v, err = %v", attachment, err)
	}
}

func TestAttachmentDataURLReadsOnlySessionImage(t *testing.T) {
	runtime := azemapp.NewService(context.Background(), config.Default())
	runtime.AttachAttachments(filepath.Join(t.TempDir(), "attachments"))
	item, err := runtime.ImportImageBytes("session-1", "preview.png", "image/png", []byte{0x89, 0x50, 0x4e, 0x47})
	if err != nil {
		t.Fatal(err)
	}
	bridge := &Bridge{runtime: runtime}
	attachment := attachmentFromSession(item)
	got, err := bridge.AttachmentDataURL("session-1", attachment)
	if err != nil {
		t.Fatal(err)
	}
	if got != "data:image/png;base64,iVBORw==" {
		t.Fatalf("data URL = %q", got)
	}
	if _, err := bridge.AttachmentDataURL("session-2", attachment); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("cross-session preview error = %v", err)
	}
}

func TestAllowedDesktopActions(t *testing.T) {
	if !allowedAction(azemapp.ActionResolveApproval) {
		t.Fatal("approval resolution must be available to the desktop")
	}
	if !allowedAction(azemapp.ActionResolveUserInput) || !allowedAction(azemapp.ActionResolvePlan) {
		t.Fatal("planning interaction actions must be available to the desktop")
	}
	if !allowedAction(azemapp.ActionListModels) {
		t.Fatal("model catalog must be available to the desktop")
	}
	if !allowedAction(azemapp.ActionSetSkillEnabled) {
		t.Fatal("skill availability must be configurable from the desktop")
	}
	if !allowedAction(azemapp.ActionSetPluginImported) {
		t.Fatal("Codex plugin import selection must be configurable from the desktop")
	}
	if !allowedAction(azemapp.ActionSetMCPEnabled) || !allowedAction(azemapp.ActionUpsertMCPServer) || !allowedAction(azemapp.ActionDeleteMCPServer) {
		t.Fatal("MCP services must be configurable from the desktop")
	}
	if !allowedAction(azemapp.ActionListModelProviders) || !allowedAction(azemapp.ActionDiscoverProviderModels) || !allowedAction(azemapp.ActionSetModelProvider) || !allowedAction(azemapp.ActionSetModelEnabled) {
		t.Fatal("llmux model provider actions must be available to the desktop")
	}
	if !allowedAction(azemapp.ActionSetQueueMode) {
		t.Fatal("queue mode must be configurable from the desktop")
	}
	if !allowedAction(azemapp.ActionSetSessionPreferences) {
		t.Fatal("session preferences must be configurable from the desktop")
	}
	if !allowedAction(azemapp.ActionSetChatGPTFastMode) {
		t.Fatal("ChatGPT fast mode must be configurable from the desktop")
	}
	if !allowedAction(azemapp.ActionRefreshSession) {
		t.Fatal("session projection refresh must be available to the desktop")
	}
	if !allowedAction(azemapp.ActionCreateGitBranch) {
		t.Fatal("git branch creation must be available to the desktop")
	}
	if allowedAction(azemapp.ActionKind("arbitrary_shell")) {
		t.Fatal("unknown desktop actions must be rejected")
	}
}

func TestPullRequestMonitorStateIsScopedToWorkspace(t *testing.T) {
	stateDir := t.TempDir()
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	pathA := pullRequestMonitorStatePath(stateDir, workspaceA)
	pathB := pullRequestMonitorStatePath(stateDir, workspaceB)
	if pathA == pathB {
		t.Fatalf("workspace monitor paths collided: %q", pathA)
	}
	if filepath.Dir(pathA) != stateDir || filepath.Dir(pathB) != stateDir {
		t.Fatalf("monitor paths escaped state directory: %q %q", pathA, pathB)
	}
	if filepath.Base(pathA) == "pr-monitors.json" || filepath.Base(pathB) == "pr-monitors.json" {
		t.Fatalf("monitor path is not workspace-scoped: %q %q", pathA, pathB)
	}
}
