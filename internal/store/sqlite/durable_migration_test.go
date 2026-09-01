package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/azem/internal/session"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/durable"
	"github.com/Viking602/venat/message"
)

func TestMigrationV27DurabilitySurvivesCurrentSchemaAndReopens(t *testing.T) {
	if len(migrations) != schemaVersion || schemaVersion != 28 {
		t.Fatalf("migration count=%d schema=%d", len(migrations), schemaVersion)
	}
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "durable-upgrade.db")
	blobRoot := filepath.Join(root, "blobs")
	db, err := sql.Open("sqlite", sqliteDSN(path, false))
	if err != nil {
		t.Fatal(err)
	}
	applyFixtureMigrations(t, ctx, db, 26)
	createdAt := time.Unix(42, 0).UTC()
	legacyRun := agentruntime.Run{
		ID: "legacy-run", Status: agentruntime.RunStatusCompleted, Request: "legacy request",
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	legacyApproval := agentruntime.ApprovalRequest{
		ApprovalID: "legacy-approval", RunID: legacyRun.ID, TaskID: "legacy-task",
		ActionID: "legacy-action", RequestedAction: "retain approval", Status: "pending",
	}
	legacyBlock := session.Block{
		Kind: "user", RunID: legacyRun.ID, Title: "You", Content: "legacy session content", State: "submitted",
	}
	runData, err := json.Marshal(legacyRun)
	if err != nil {
		t.Fatal(err)
	}
	approvalData, err := json.Marshal(legacyApproval)
	if err != nil {
		t.Fatal(err)
	}
	blockData, err := json.Marshal(legacyBlock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO sessions(id,title,provider_id,model_id,reasoning,agent_mode,created_at,updated_at)
		VALUES('legacy-session','Legacy Session','chatgpt','legacy-model','minimal','single',42,42);
		INSERT INTO session_projections(session_id,last_run_id,updated_at)
		VALUES('legacy-session','legacy-run',42);
		INSERT INTO session_tool_records(
			session_id,run_id,tool_call_id,anchor_sequence,name,arguments,state,content,structured,started_at,completed_at
		) VALUES('legacy-session','legacy-run','legacy-tool',1,'coding.read_file','{}','completed','legacy tool output','null',42,43);
		INSERT INTO github_webhook_deliveries(
			delivery_id,event_name,repository,pull_request_number,action,payload_sha256,status,received_at
		) VALUES('delivery-1','pull_request','owner/repo',7,'synchronize','digest','accepted',42);
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO session_blocks(session_id,sequence,kind,run_id,data)
		VALUES('legacy-session',1,'user','legacy-run',?)
	`, blockData); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO records(kind,key1,run_id,status,created_at,data)
		VALUES('run','legacy-run','legacy-run','completed',42,?)
	`, runData); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO records(kind,key1,run_id,task_id,status,created_at,data)
		VALUES('approval','legacy-approval','legacy-run','legacy-task','pending',42,?)
	`, approvalData); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	provider, err := Open(ctx, path, WithBlobRoot(blobRoot))
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range []string{
		"agent_executions", "agent_effect_attempts", "agent_execution_receipts", "agent_execution_bindings",
		"agent_executions_status", "agent_executions_lease_expiry", "agent_effect_attempts_status",
		"agent_execution_bindings_segment", "agent_execution_bindings_session",
	} {
		var found string
		if err := provider.DB().QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE name=?`, object).Scan(&found); err != nil {
			t.Fatalf("missing durable object %s: %v", object, err)
		}
	}
	var version int
	if err := provider.DB().QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("upgraded version=%d error=%v", version, err)
	}
	assertRetainedV26RuntimeData(t, ctx, provider)

	backend := provider.DurableBackend()
	spec := durable.ExecutionSpec{Request: hyagent.Request{Prompt: "upgraded schema v1 execution"}}
	specHash, err := durable.HashExecutionSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	started, err := backend.StartExecution(ctx, durable.StartExecutionRequest{
		ExecutionID: "upgraded-v1-execution", OwnerID: "owner", ClaimID: durable.ClaimID{15: 27},
		LeaseTTL: time.Minute, Spec: spec, SpecHash: specHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	continuation := hyagent.Continuation{
		SchemaVersion: hyagent.ContinuationSchemaVersion,
		Request:       hyagent.Request{Prompt: strings.Repeat("upgraded continuation ", 1_000)},
		Messages:      []message.Message{message.NewText(message.RoleUser, strings.Repeat("upgraded continuation ", 1_000))},
		Phase:         hyagent.ContinuationReady,
	}
	continuationHash, err := durable.HashContinuation(continuation)
	if err != nil {
		t.Fatal(err)
	}
	lease := durable.LeaseRef{OwnerID: started.Execution.Lease.OwnerID, Token: started.Execution.Lease.Token}
	if _, err := backend.SaveCheckpoint(ctx, durable.SaveCheckpointRequest{
		ExecutionID: "upgraded-v1-execution", Lease: lease, ExpectedVersion: started.Execution.Version,
		Checkpoint: durable.Checkpoint{Sequence: 1, Continuation: continuation, ContinuationHash: continuationHash},
	}); err != nil {
		t.Fatal(err)
	}
	var checkpointDigest string
	if err := provider.DB().QueryRowContext(ctx, `SELECT execution_digest FROM agent_executions WHERE execution_id='upgraded-v1-execution'`).Scan(&checkpointDigest); err != nil {
		t.Fatal(err)
	}
	if checkpointDigest == "" {
		t.Fatal("upgraded v1 continuation stayed inline")
	}
	if err := provider.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("upgrade backup: %v", err)
	}

	backup, err := sql.Open("sqlite", sqliteDSN(path+".bak", false))
	if err != nil {
		t.Fatal(err)
	}
	var backupVersion, backupApprovals int
	if err := backup.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&backupVersion); err != nil {
		t.Fatal(err)
	}
	if err := backup.QueryRowContext(ctx, `SELECT count(*) FROM records WHERE kind='approval' AND key1='legacy-approval'`).Scan(&backupApprovals); err != nil {
		t.Fatal(err)
	}
	if err := backup.Close(); err != nil {
		t.Fatal(err)
	}
	if backupVersion != 26 || backupApprovals != 1 {
		t.Fatalf("upgrade backup version=%d approvals=%d", backupVersion, backupApprovals)
	}

	reopened, err := Open(ctx, path, WithBlobRoot(blobRoot))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(ctx)
	if err := reopened.DB().QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("reopened version=%d error=%v", version, err)
	}
	assertRetainedV26RuntimeData(t, ctx, reopened)
	loaded, err := reopened.DurableBackend().LoadExecution(ctx, "upgraded-v1-execution")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Checkpoint == nil || loaded.Checkpoint.Sequence != 1 ||
		loaded.Checkpoint.Continuation.Request.Prompt != continuation.Request.Prompt {
		t.Fatalf("reopened upgraded execution=%+v", loaded)
	}
}

func TestOpenRejectsFutureSchemaWithoutMutation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "future.db")
	db, err := sql.Open("sqlite", sqliteDSN(path, false))
	if err != nil {
		t.Fatal(err)
	}
	applyFixtureMigrations(t, ctx, db, len(migrations))
	if _, err := db.ExecContext(ctx, `
		INSERT INTO sessions(id,title,created_at,updated_at) VALUES('future-session','Future',42,42);
		PRAGMA user_version=29;
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if provider, err := Open(ctx, path); err == nil {
		_ = provider.Close(ctx)
		t.Fatal("future schema opened successfully")
	} else if !strings.Contains(err.Error(), "database schema 29 is newer than supported schema 28") {
		t.Fatalf("future schema error=%v", err)
	}

	db, err = sql.Open("sqlite", sqliteDSN(path, false))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	var title string
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT title FROM sessions WHERE id='future-session'`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if version != 29 || title != "Future" {
		t.Fatalf("future database mutated version=%d title=%q", version, title)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("future schema created backup: %v", err)
	}
}

func applyFixtureMigrations(t *testing.T, ctx context.Context, db *sql.DB, through int) {
	t.Helper()
	if through < 0 || through > len(migrations) {
		t.Fatalf("fixture migration target=%d", through)
	}
	for version := 1; version <= through; version++ {
		if _, err := db.ExecContext(ctx, migrations[version-1]); err != nil {
			t.Fatalf("apply fixture migration %d: %v", version, err)
		}
		if version == 21 {
			for _, statement := range []string{
				`ALTER TABLE events ADD COLUMN data_sha256 TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE records ADD COLUMN data_sha256 TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE context_artifacts DROP COLUMN payload`,
				`ALTER TABLE session_projections DROP COLUMN blocks`,
			} {
				if _, err := db.ExecContext(ctx, statement); err != nil {
					t.Fatalf("apply fixture migration %d compatibility step: %v", version, err)
				}
			}
		}
		if _, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version) VALUES (?)`, version); err != nil {
			t.Fatalf("record fixture migration %d: %v", version, err)
		}
		if _, err := db.ExecContext(ctx, `PRAGMA user_version = `+strconv.Itoa(version)); err != nil {
			t.Fatalf("set fixture schema version %d: %v", version, err)
		}
	}
}

func assertRetainedV26RuntimeData(t *testing.T, ctx context.Context, provider *Provider) {
	t.Helper()
	work, err := provider.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	run, err := work.Runs().LoadRun(ctx, "legacy-run")
	if err != nil {
		t.Fatal(err)
	}
	approval, err := work.Approvals().LoadApproval(ctx, "legacy-approval")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != agentruntime.RunStatusCompleted || approval.ActionID != "legacy-action" || approval.Status != "pending" {
		t.Fatalf("retained run=%+v approval=%+v", run, approval)
	}
	if err := work.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	sessions := session.NewService(provider.DB(), provider.Blobs())
	projection, err := sessions.LoadProjection(ctx, "legacy-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Blocks) != 1 || projection.Blocks[0].Content != "legacy session content" {
		t.Fatalf("retained projection=%+v", projection.Blocks)
	}
	record, err := sessions.LoadToolRecord(ctx, "legacy-session", "legacy-run", "legacy-tool")
	if err != nil {
		t.Fatal(err)
	}
	if record.State != "completed" || record.Content != "legacy tool output" {
		t.Fatalf("retained tool record=%+v", record)
	}
	var repository string
	if err := provider.DB().QueryRowContext(ctx, `SELECT repository FROM github_webhook_deliveries WHERE delivery_id='delivery-1'`).Scan(&repository); err != nil || repository != "owner/repo" {
		t.Fatalf("retained webhook repository=%q error=%v", repository, err)
	}
}
