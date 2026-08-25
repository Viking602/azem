package maintenance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestSetupDryRunCreatesPrivateIdempotentConfiguration(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "workspace #1")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AZEM_HOME", home)
	dry, err := Setup(ctx, SetupOptions{Workspace: workspace, DryRun: true})
	if err != nil || len(dry.Created) == 0 {
		t.Fatalf("dry setup=%#v error=%v", dry, err)
	}
	if _, err := os.Stat(dry.Paths.ConfigFile); !os.IsNotExist(err) {
		t.Fatalf("dry run created config: %v", err)
	}
	report, err := Setup(ctx, SetupOptions{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(report.Paths.ConfigFile)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode=%v error=%v", info.Mode(), err)
	}
	payload, _ := os.ReadFile(report.Paths.ConfigFile)
	if !strings.Contains(string(payload), `"`+report.Paths.Workspace+`"`) {
		t.Fatalf("workspace was not safely quoted:\n%s", payload)
	}
	original := append([]byte(nil), payload...)
	if _, err := Setup(ctx, SetupOptions{Workspace: workspace}); err != nil {
		t.Fatal(err)
	}
	payload, _ = os.ReadFile(report.Paths.ConfigFile)
	if string(payload) != string(original) {
		t.Fatal("idempotent setup rewrote existing config")
	}
}

func TestGarbageCollectionDryRunAndApplyPreserveReferencedState(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	blobRoot := filepath.Join(root, "blobs")
	store, err := sqlitestore.Open(ctx, filepath.Join(root, "azem.db"), sqlitestore.WithBlobRoot(blobRoot))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "active", Title: "Active"}); err != nil {
		t.Fatal(err)
	}
	referenced, err := store.Blobs().Put(ctx, []byte("referenced"))
	if err != nil {
		t.Fatal(err)
	}
	unreferenced, err := store.Blobs().Put(ctx, []byte("unreferenced"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO context_artifacts(id,session_id,run_id,kind,sha256,preview,created_at) VALUES('artifact','active','','test',?,'',1)`, referenced); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	for _, digest := range []string{referenced, unreferenced} {
		path := filepath.Join(blobRoot, digest[:2], digest)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	attachments := filepath.Join(root, "attachments")
	for _, id := range []string{"active", "orphan"} {
		directory := filepath.Join(attachments, id)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "file.txt"), []byte(id), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(directory, old, old)
	}
	logs := filepath.Join(root, "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	oldLog := filepath.Join(logs, "old.log")
	if err := os.WriteFile(oldLog, []byte("log"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(oldLog, old, old)
	options := GCOptions{DB: store.DB(), BlobRoot: blobRoot, AttachmentRoot: attachments, LogRoot: logs, MinimumAge: 24 * time.Hour, LogRetention: 24 * time.Hour, Now: time.Now}
	dry, err := Collect(ctx, options)
	if err != nil || dry.Removed != 0 || len(dry.Candidates) != 3 {
		t.Fatalf("dry GC=%#v error=%v", dry, err)
	}
	for _, path := range []string{filepath.Join(blobRoot, unreferenced[:2], unreferenced), filepath.Join(attachments, "orphan"), oldLog} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("dry GC removed %s: %v", path, err)
		}
	}
	options.Apply = true
	report, err := Collect(ctx, options)
	if err != nil || report.Removed != 3 {
		t.Fatalf("apply GC=%#v error=%v", report, err)
	}
	if _, err := os.Stat(filepath.Join(blobRoot, unreferenced[:2], unreferenced)); !os.IsNotExist(err) {
		t.Fatalf("unreferenced blob remained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(blobRoot, referenced[:2], referenced)); err != nil {
		t.Fatalf("referenced blob removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(attachments, "active")); err != nil {
		t.Fatalf("active attachments removed: %v", err)
	}
}

func TestUpdaterChecksDigestVerifiesCandidateAndAtomicallyReplaces(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	executable := filepath.Join(root, "azem")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\necho 'azem 1.0.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	candidate := []byte("#!/bin/sh\necho 'azem 1.2.0'\n")
	digest := sha256.Sum256(candidate)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/latest":
			_ = json.NewEncoder(writer).Encode(Release{Tag: "v1.2.0", URL: server.URL + "/release", Assets: []ReleaseAsset{{Name: "azem-darwin-arm64", URL: server.URL + "/asset", Digest: "sha256:" + hex.EncodeToString(digest[:]), Size: int64(len(candidate))}}})
		case "/asset":
			_, _ = writer.Write(candidate)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	updater, err := NewUpdater(UpdateOptions{CurrentVersion: "v1.0.0", APIURL: server.URL + "/latest", Executable: executable, GOOS: "darwin", GOARCH: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	check, err := updater.Check(ctx)
	if err != nil || !check.Available || check.Latest != "v1.2.0" {
		t.Fatalf("check=%#v error=%v", check, err)
	}
	if err := updater.Apply(ctx, check); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable, "--version").CombinedOutput()
	if err != nil || !strings.Contains(string(output), "1.2.0") {
		t.Fatalf("updated output=%q error=%v", output, err)
	}
	if _, err := os.Stat(executable + ".previous"); !os.IsNotExist(err) {
		t.Fatalf("update backup remained: %v", err)
	}
	bad := check
	bad.Asset.Digest = "sha256:" + strings.Repeat("0", 64)
	if err := updater.Apply(ctx, bad); err == nil {
		t.Fatal("accepted update with mismatched digest")
	}
}

func TestSafeUpdateRedirectRejectsUntrustedHost(t *testing.T) {
	request, _ := http.NewRequest(http.MethodGet, "https://evil.example/asset", nil)
	if err := safeUpdateRedirect(request, []*http.Request{{}}); err == nil {
		t.Fatal("accepted untrusted update redirect")
	}
}
