//go:build live

package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/venat/message"
)

// Opt-in: isolated workspace, database and config; source credentials are read
// without refreshing or changing the user's account store.
func TestLiveFusionCrossProviderHandoffs(t *testing.T) {
	if os.Getenv("AZEM_LIVE_FUSION") != "1" {
		t.Skip("set AZEM_LIVE_FUSION=1 to use local subscription credentials")
	}
	started := time.Now()
	scenario := firstNonempty(os.Getenv("AZEM_LIVE_FUSION_SCENARIO"), "transfer")
	if !slices.Contains([]string{"transfer", "repair", "restart", "long"}, scenario) {
		t.Fatalf("unknown Fusion scenario %q", scenario)
	}
	report := map[string]any{"scenario": scenario, "startedAt": started.UTC()}
	var wireMu sync.Mutex
	wireRequests := []map[string]any{}
	observe := func(request *http.Request, response *http.Response, requestErr error) {
		row := map[string]any{"at": time.Now().UTC(), "endpoint": request.URL.Scheme + "://" + request.URL.Host + request.URL.Path}
		headerHashes := map[string]string{}
		for _, name := range []string{"session-id", "thread-id", "session_id", "conversation_id", "x-codex-turn-state"} {
			if value := request.Header.Get(name); value != "" {
				headerHashes[name] = fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
			}
		}
		row["headerHashes"] = headerHashes
		if response != nil {
			row["status"] = response.StatusCode
			row["serverTurnStatePresent"] = response.Header.Get("x-codex-turn-state") != ""
		}
		if requestErr != nil {
			row["transportError"] = true
		}
		if request.GetBody != nil {
			body, err := request.GetBody()
			if err == nil {
				encoded, readErr := io.ReadAll(body)
				_ = body.Close()
				var fields map[string]json.RawMessage
				if readErr == nil && json.Unmarshal(encoded, &fields) == nil {
					hashes := map[string]string{}
					for name, value := range fields {
						hashes[name] = fmt.Sprintf("%x", sha256.Sum256(value))
					}
					row["fieldHashes"] = hashes
					var input []json.RawMessage
					if json.Unmarshal(fields["input"], &input) == nil {
						items := []string{}
						for _, item := range input {
							items = append(items, fmt.Sprintf("%x", sha256.Sum256(item)))
						}
						row["inputHashes"] = items
					}
					var model string
					_ = json.Unmarshal(fields["model"], &model)
					row["model"] = model
				}
			}
		}
		if response != nil && response.StatusCode >= 400 && response.Body != nil {
			original := response.Body
			encoded, _ := io.ReadAll(io.LimitReader(original, 64<<10))
			response.Body = struct {
				io.Reader
				io.Closer
			}{io.MultiReader(bytes.NewReader(encoded), original), original}
			if response.Header.Get("Content-Encoding") == "gzip" {
				if compressed, err := gzip.NewReader(bytes.NewReader(encoded)); err == nil {
					encoded, _ = io.ReadAll(io.LimitReader(compressed, 64<<10))
					_ = compressed.Close()
				}
			}
			detail := string(encoded)
			if token := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "); token != "" {
				detail = strings.ReplaceAll(detail, token, "[redacted]")
			}
			detail = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+|(?:Bearer|sk-)\s*[A-Za-z0-9_-]+`).ReplaceAllString(detail, "[redacted]")
			row["errorBody"] = string([]rune(detail)[:min(1024, len([]rune(detail)))])
			t.Logf("provider rejection status=%d body=%s", response.StatusCode, row["errorBody"])
		}
		wireMu.Lock()
		wireRequests = append(wireRequests, row)
		wireMu.Unlock()
	}
	defer func() {
		wireMu.Lock()
		report["wireRequests"] = append([]map[string]any(nil), wireRequests...)
		wireMu.Unlock()
		report["passed"], report["durationSeconds"] = !t.Failed(), time.Since(started).Seconds()
		if directory := os.Getenv("AZEM_LIVE_FUSION_EVIDENCE_DIR"); directory != "" {
			if !filepath.IsAbs(directory) {
				t.Error("evidence directory must be absolute")
				return
			}
			if err := os.MkdirAll(directory, 0700); err != nil {
				t.Error(err)
				return
			}
			encoded, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				t.Error(err)
				return
			}
			path := filepath.Join(directory, fmt.Sprintf("%s-%d.json", scenario, started.UnixNano()))
			if err := os.WriteFile(path, encoded, 0600); err != nil {
				t.Error(err)
				return
			}
			t.Logf("evidence=%s", path)
		}
	}()

	limit := 10 * time.Minute
	if scenario == "long" {
		limit = 20 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	workspace, root := t.TempDir(), t.TempDir()
	t.Setenv("AZEM_HOME", root)
	configPath := filepath.Join(root, "config.yaml")
	body := fmt.Sprintf("version: 1\nworkspace:\n  root: %s\n  allow_write: true\nauth:\n  store: file\n  import_codex: false\n  import_grok: false\nagents:\n  subagents:\n    max_depth: 1\n    max_concurrency: 1\nmcp:\n  servers: {}\n", workspace)
	if err := os.WriteFile(configPath, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	boot, err := Bootstrap(ctx, workspace, configPath)
	if err != nil {
		t.Fatal(err)
	}
	boot.Service.Authentication().ObserveLiveStream(observe)
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := boot.Service.Shutdown(cleanup); err != nil {
			t.Error(err)
		}
		report["activeWorkAfterShutdown"] = boot.Service.HasActiveWork()
	}()
	defer func() {
		// Read the actual metering ledger, including unreported and failed calls.
		ledger, err := sql.Open("sqlite", "file:"+boot.Paths.Database+"?mode=ro")
		if err != nil {
			t.Error(err)
			return
		}
		defer ledger.Close()
		rows, err := ledger.Query(`SELECT request_id, provider_request_id, session_id, run_id, request_kind, provider, model, transport, input_tokens, cached_tokens, cache_write_tokens, cache_reported, cache_write_reported, output_tokens, reasoning_tokens, total_tokens, status, started_at, completed_at, cache_epoch, checkpoint_generation FROM provider_requests ORDER BY started_at, request_id`)
		if err != nil {
			t.Error(err)
			return
		}
		defer rows.Close()
		facts := []session.ProviderRequestFact{}
		for rows.Next() {
			var fact session.ProviderRequestFact
			var start, end int64
			if err := rows.Scan(&fact.RequestID, &fact.ProviderRequestID, &fact.SessionID, &fact.RunID, &fact.RequestKind, &fact.Provider, &fact.Model, &fact.Transport, &fact.InputTokens, &fact.CachedTokens, &fact.CacheWriteTokens, &fact.CacheReported, &fact.CacheWriteReported, &fact.OutputTokens, &fact.ReasoningTokens, &fact.TotalTokens, &fact.Status, &start, &end, &fact.CacheEpoch, &fact.CheckpointGeneration); err != nil {
				t.Error(err)
				return
			}
			fact.StartedAt = time.Unix(0, start).UTC()
			if end != 0 {
				fact.CompletedAt = time.Unix(0, end).UTC()
			}
			facts = append(facts, fact)
		}
		if err := rows.Err(); err != nil {
			t.Error(err)
		}
		report["providerRequests"] = facts
		failures, err := ledger.Query(`SELECT a.kind, a.status, json_extract(CAST(a.attempt_inline AS TEXT), '$.attempt.failure.message') FROM agent_effect_attempts a WHERE a.status IN ('unknown', 'failed')`)
		if err != nil {
			t.Error(err)
			return
		}
		defer failures.Close()
		failed := []map[string]any{}
		for failures.Next() {
			var kind, status string
			var reason sql.NullString
			if err := failures.Scan(&kind, &status, &reason); err != nil {
				t.Error(err)
				return
			}
			failed = append(failed, map[string]any{"kind": kind, "status": status, "reason": reason.String})
		}
		if err := failures.Err(); err != nil {
			t.Error(err)
		}
		report["failedAttempts"] = failed
	}()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(home, ".azem", "azem.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source := auth.NewService(db, auth.NewSQLiteStore(db), nil, nil)
	for _, providerID := range []string{"chatgpt", "grok"} {
		accounts, err := source.Accounts(ctx, providerID)
		if err != nil {
			t.Fatal(err)
		}
		imported := false
		for _, account := range accounts {
			if account.Status != "active" {
				continue
			}
			credential, err := source.StoredCredential(ctx, providerID, account.ID)
			if err != nil {
				t.Fatal(err)
			}
			credential.SourcePath, credential.SourceKey = "", ""
			if _, err := boot.Service.Authentication().StoreCredential(ctx, credential); err != nil {
				t.Fatal(err)
			}
			imported = true
			break
		}
		if !imported {
			t.Fatalf("no active %s account", providerID)
		}
	}
	lead := liveFusionRoute(t, ctx, boot.Service, "chatgpt", firstNonempty(os.Getenv("AZEM_LIVE_FUSION_LEAD_MODEL"), "gpt-6-astra"))
	sidekick := liveFusionRoute(t, ctx, boot.Service, "grok", firstNonempty(os.Getenv("AZEM_LIVE_FUSION_SIDEKICK_MODEL"), "grok-4.6"))
	report["lead"], report["sidekick"] = lead, sidekick
	if err := boot.Service.updateModelRoute(ctx, &ModelRouteEntry{Scope: "fusion", Route: sidekick}, false); err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	marker := fmt.Sprintf("FUSION_%x_OK", nonce)
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("seed.txt", marker+"\n")
	rootRuns := []map[string]any{}
	report["sessionId"] = boot.SessionID
	turn := func(prompt, expected string) {
		t.Helper()
		runID, err := boot.Service.StartConfiguredTurn(TurnRequest{SessionID: boot.SessionID, Prompt: prompt, Provider: lead.Provider, Model: lead.Model, Reasoning: lead.Reasoning, AgentMode: "fusion"})
		if err != nil {
			t.Fatal(err)
		}
		entry := map[string]any{"runId": runID, "prompt": prompt}
		rootRuns = append(rootRuns, entry)
		report["rootRuns"] = rootRuns
		t.Logf("scenario=%s lead=%s/%s sidekick=%s/%s run=%s", scenario, lead.Provider, lead.Model, sidekick.Provider, sidekick.Model, runID)
		states := map[string]string{}
		inlineTools := map[string]string{}
		childProse, handoffs := 0, 0
		samples := []map[string]any{}
		sample := func(terminal bool) {
			sampleStart := time.Now()
			sampleCtx, stop := context.WithTimeout(ctx, 3*time.Second)
			defer stop()
			projection, err := boot.Service.RuntimeProjection(sampleCtx, boot.SessionID)
			row := map[string]any{"at": sampleStart.UTC(), "latencyMs": time.Since(sampleStart).Milliseconds(), "terminal": terminal, "runs": projection.Runs, "pendingControls": len(projection.PendingControls), "hasActiveWork": boot.Service.HasActiveWork(), "hasActiveChildren": boot.Service.HasActiveForegroundChildren()}
			row["recovery"] = projection.Recovery
			if projection.Session != nil {
				row["agents"] = projection.Session.AgentSnapshots
				if len(projection.Session.AgentSnapshots) != 0 {
					t.Error("Fusion appeared in the visible subagent roster")
				}
			}
			if err != nil {
				row["error"] = err.Error()
				t.Errorf("runtime projection: %v", err)
			}
			samples = append(samples, row)
			entry["stateSamples"] = samples
			for _, snapshot := range boot.Service.providers.ListSubagents(sampleCtx, boot.SessionID) {
				if strings.Contains(snapshot.Run.Summary, "waiting for reconciliation") {
					entry["failure"] = snapshot.Run.Summary
					t.Fatalf("Sidekick requires reconciliation: %s", snapshot.Run.ID)
				}
			}
		}
		nextSample := time.Now()
		for {
			if !time.Now().Before(nextSample) {
				sample(false)
				nextSample = time.Now().Add(5 * time.Second)
			}
			waitCtx, stop := context.WithDeadline(ctx, nextSample)
			event, err := boot.Service.NextEvent(waitCtx)
			stop()
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if event.Kind == EventApprovalRequested {
				if err := boot.Service.ExecuteAction(ctx, Action{Kind: ActionResolveApproval, Target: event.ApprovalID, Decision: "once"}); err != nil {
					t.Fatal(err)
				}
			}
			if event.Kind == EventAgentState && states[event.AgentID] != event.State {
				states[event.AgentID] = event.State
				t.Error("Fusion emitted a visible subagent lifecycle event")
			}
			if event.Kind == EventToolFinished && event.Data["name"] == "sidekick" {
				handoffs++
			}
			if (event.Kind == EventTextDelta || event.Kind == EventThinkingDelta) && event.Data["fusionRole"] == "sidekick" {
				childProse++
				if event.AgentID != "" || event.RunID != runID || event.TextPhase != "commentary" {
					t.Error("child prose lost its source or final ownership")
				}
			}
			if event.Kind == EventToolFinished && event.Data["executionRunId"] != "" {
				if event.AgentID != "" || event.RunID != runID {
					t.Error("Fusion tool did not join the main conversation")
				}
				inlineTools[event.ToolCallID] = event.Data["name"]
			}
			if event.RunID != runID {
				continue
			}
			if event.Kind == EventRunFailed {
				entry["failure"] = event.Text
				t.Fatalf("Fusion failed: %s", event.Text)
			}
			if event.Kind == EventRunFinished {
				break
			}
		}
		// RunFinished may precede the final goroutine cleanup by a few milliseconds.
		for deadline := time.Now().Add(3 * time.Second); boot.Service.HasActiveWork() && time.Now().Before(deadline); {
			time.Sleep(10 * time.Millisecond)
		}
		sample(true)
		entry["inlineTools"] = inlineTools
		entry["childProseEvents"], entry["handoffs"] = childProse, handoffs
		if childProse == 0 || handoffs == 0 {
			t.Error("handoff or child prose was hidden")
		}
		if len(inlineTools) == 0 {
			t.Error("no actual Sidekick tool was visible inline")
		}
		restored, err := boot.Service.RuntimeProjection(ctx, boot.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		for id, name := range inlineTools {
			if !slices.ContainsFunc(restored.Session.ToolRecords, func(record session.ToolRecord) bool {
				return record.ToolCallID == id && record.Name == name && record.RunID == runID && record.State != session.ToolRunning
			}) {
				t.Errorf("inline tool %s did not survive restore", name)
			}
		}
		if boot.Service.HasActiveWork() {
			t.Error("work remained active after the completed run settled")
		}
		persisted, err := boot.Service.coding.LoadRunProjection(ctx, runID)
		if err != nil {
			t.Fatal(err)
		}
		entry["state"] = persisted.Run.Status
		if persisted.Run.Status != agentruntime.RunStatusCompleted || persisted.Binding == nil {
			t.Fatalf("run did not persist completion: %s", persisted.Run.Status)
		}
		binding := persisted.Binding.Manifest
		entry["provider"], entry["model"], entry["reasoning"] = binding.Provider, binding.Model, binding.Reasoning
		if binding.Provider != lead.Provider || binding.Model != lead.Model {
			t.Fatal("lead model route changed")
		}
		projection, err := boot.Service.sessions.LoadProjection(ctx, boot.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		final := ""
		for _, block := range projection.Blocks {
			if block.RunID == runID && block.Kind == "assistant" {
				final = block.Content
			}
		}
		entry["final"] = final
		if strings.TrimSpace(final) != expected {
			t.Fatalf("final mismatch: got %q, want %q", final, expected)
		}
	}
	expectedHandoffs := 2
	if scenario == "long" {
		expectedHandoffs = 6
		write("ledger.py", "def summarize(rows):\n    return {}\n")
		write("test_ledger.py", `import json, subprocess, sys, tempfile, unittest
from pathlib import Path
from ledger import summarize

class CoreTests(unittest.TestCase):
    def test_totals(self):
        rows = [{'account':' A ', 'cents':'12'}, {'account':'a','cents':'-2'}, {'account':' B','cents':'0'}]
        before = json.dumps(rows)
        self.assertEqual(summarize(rows), {'a':10,'b':0})
        self.assertEqual(json.dumps(rows), before)
    def test_empty(self):
        self.assertEqual(summarize([]), {})
    def test_invalid(self):
        for row in [{'account':' ', 'cents':'1'}, {'account':'x','cents':'1.5'}, {'account':'x'}, {'cents':'1'}]:
            with self.subTest(row=row), self.assertRaises(ValueError):
                summarize([row])

class CLITests(unittest.TestCase):
    def test_cli(self):
        with tempfile.TemporaryDirectory() as tmp:
            source, output = Path(tmp)/'in.csv', Path(tmp)/'out.json'
            source.write_text('account,cents\n Z ,7\nz,-3\na,2\n')
            subprocess.run([sys.executable,'ledger.py','--input',str(source),'--output',str(output)],check=True)
            self.assertEqual(json.loads(output.read_text()), {'a':2,'z':4})
    def test_invalid_keeps_output(self):
        with tempfile.TemporaryDirectory() as tmp:
            source, output = Path(tmp)/'in.csv', Path(tmp)/'out.json'
            source.write_text('account,cents\nx,1.2\n')
            output.write_text('KEEP')
            result = subprocess.run([sys.executable,'ledger.py','--input',str(source),'--output',str(output)],capture_output=True)
            self.assertNotEqual(result.returncode,0)
            self.assertEqual(output.read_text(),'KEEP')
`)
		data := "account,cents\n"
		want := map[string]int{}
		for i := 0; i < 5000; i++ {
			account, amount := fmt.Sprintf("account_%02d", i%37), i%101-50
			data += fmt.Sprintf(" %s ,%d\n", strings.ToUpper(account), amount)
			want[account] += amount
		}
		write("input.csv", data)
		tests, _ := os.ReadFile(filepath.Join(workspace, "test_ledger.py"))
		turn("Complete a six-stage Fusion task with exactly SIX sequential handoffs to the SAME Sidekick, waiting for and reviewing each result. Keep its conversation continuous. Stage 1: inspect ledger.py and test_ledger.py, run the existing suite and diagnose expected failures without edits. Stage 2: implement summarize(rows) in ledger.py and run CoreTests. It returns normalized lowercase/trimmed account keys and exact integer cent totals, retains zero totals, leaves input unchanged, and raises ValueError on blank/missing accounts or missing/noninteger cents. Stage 3: implement ledger.py CLI --input CSV --output JSON using stdlib; run CLITests. Validate all input before touching output; invalid input exits nonzero without altering an existing output. Stage 4: run and verify additional deterministic randomized cases, signed large integer totals and invalid inputs; fix defects if any, but do not change test_ledger.py. Stage 5: process all 5000 rows in input.csv into summary.json and independently verify counts/totals with a shell command. Stage 6: final audit, rerun the entire unchanged suite, review the complete implementation and output, and report evidence. You, the lead, must read ledger.py and summary.json before accepting the result. No new dependencies and do not modify test_ledger.py or input.csv. Final answer exactly FUSION_LONG_OK.", "FUSION_LONG_OK")
		after, _ := os.ReadFile(filepath.Join(workspace, "test_ledger.py"))
		inputAfter, _ := os.ReadFile(filepath.Join(workspace, "input.csv"))
		if string(tests) != string(after) || string(inputAfter) != data {
			t.Fatal("long task modified its acceptance inputs")
		}
		output, err := os.ReadFile(filepath.Join(workspace, "summary.json"))
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]int
		if err := json.Unmarshal(output, &got); err != nil {
			t.Fatal(err)
		}
		wantJSON, _ := json.Marshal(want)
		gotJSON, _ := json.Marshal(got)
		if string(wantJSON) != string(gotJSON) {
			t.Fatal("5000-row aggregate differs from independent Go calculation")
		}
		command := exec.CommandContext(ctx, "python3", "-m", "unittest", "discover", "-v")
		command.Dir = workspace
		verified, err := command.CombinedOutput()
		report["independentVerification"] = string(verified)
		report["independentAggregate"] = got
		if err != nil {
			t.Fatalf("independent long task tests: %v\n%s", err, verified)
		}
	} else if scenario == "repair" {
		write("tags.py", "def normalize_tags(values):\n    return list(values)\n")
		write("test_tags.py", "import unittest\nfrom tags import normalize_tags\n\nclass TagsTests(unittest.TestCase):\n    def test_normalize(self):\n        self.assertEqual(normalize_tags([' B ', 'a', 'A', 'b']), ['a', 'b'])\n    def test_empty(self):\n        self.assertEqual(normalize_tags(['', '  ']), [])\n    def test_input_unchanged(self):\n        values = [' X ', 'x']\n        self.assertEqual(normalize_tags(values), ['x'])\n        self.assertEqual(values, [' X ', 'x'])\n")
		tests, _ := os.ReadFile(filepath.Join(workspace, "test_tags.py"))
		turn("Use exactly two sequential sidekick handoffs. First ask Sidekick to inspect tags.py and test_tags.py, run python3 -m unittest discover -v, and diagnose the expected failures without editing. Then ask the SAME Sidekick to fix ONLY tags.py and run the full existing suite again. normalize_tags must strip whitespace, lowercase, remove empty strings and duplicates, sort ascending, and leave input unchanged. Do not modify tests or create other source files. Review its changes and verification evidence. Final answer exactly FUSION_REPAIR_OK.", "FUSION_REPAIR_OK")
		after, _ := os.ReadFile(filepath.Join(workspace, "test_tags.py"))
		if string(after) != string(tests) {
			t.Fatal("agent changed the acceptance tests")
		}
		command := exec.CommandContext(ctx, "python3", "-m", "unittest", "discover", "-v")
		command.Dir = workspace
		output, err := command.CombinedOutput()
		report["independentVerification"] = string(output)
		if err != nil {
			t.Fatalf("independent tests failed: %v\n%s", err, output)
		}
		command = exec.CommandContext(ctx, "python3", "-c", "from tags import normalize_tags; x=['  Q','q ','Z','',' z ', ' A ']; assert normalize_tags(x)==['a','q','z']; assert x==['  Q','q ','Z','',' z ', ' A ']; assert normalize_tags([])==[]; print('HIDDEN_CHECK_OK')")
		command.Dir = workspace
		output, err = command.CombinedOutput()
		report["hiddenVerification"] = string(output)
		if err != nil {
			t.Fatalf("hidden verification failed: %v\n%s", err, output)
		}
	} else if scenario == "restart" {
		turn("Use Sidekick exactly once to read seed.txt and remember its exact contents in its own context. It must not repeat the contents in its answer: ask it to reply only REMEMBERED. You must not read the file yourself. Make no edits yet. Final answer exactly FIRST_HANDOFF_OK.", "FIRST_HANDOFF_OK")
		originalSession := boot.SessionID
		accounts, err := boot.Service.Authentication().Accounts(ctx, "grok")
		if err != nil || len(accounts) != 1 {
			t.Fatalf("Grok account before restart: %v", err)
		}
		before, err := boot.Service.Authentication().StoredCredential(ctx, "grok", accounts[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		report["transportBeforeRestart"] = boot.Service.cfg.Providers.Grok.Transport
		if err := os.Remove(filepath.Join(workspace, "seed.txt")); err != nil {
			t.Fatal(err)
		}
		if err := boot.Service.Shutdown(ctx); err != nil {
			t.Fatal(err)
		}
		boot, err = Bootstrap(ctx, workspace, configPath)
		if err != nil {
			t.Fatal(err)
		}
		boot.Service.Authentication().ObserveLiveStream(observe)
		if _, err := boot.Service.SelectSession(ctx, originalSession); err != nil {
			t.Fatal(err)
		}
		boot.SessionID = originalSession
		report["restoredSessionId"] = originalSession
		report["runtimeRestarted"] = true
		after, err := boot.Service.Authentication().StoredCredential(ctx, "grok", accounts[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		report["restartCredentialUnchanged"] = before.AccessToken == after.AccessToken && before.RefreshToken == after.RefreshToken && before.AccountID == after.AccountID
		report["transportAfterRestart"] = boot.Service.cfg.Providers.Grok.Transport
		if report["restartCredentialUnchanged"] != true {
			t.Fatal("Grok credential changed across restart")
		}
		turn("Use the SAME Sidekick exactly once. Ask it to recover the exact seed.txt contents from its own earlier conversation (the file has been removed), write those contents followed by one newline to result.txt, and verify with a shell command that the file has one line and its remembered content. Do not restate or guess the contents in your handoff. Review its evidence and read result.txt yourself. Final answer exactly FUSION_RESTART_OK.", "FUSION_RESTART_OK")
	} else {
		turn("Use Fusion with exactly two sequential sidekick handoffs. First ask Sidekick to read seed.txt and remember its exact contents; make no edits yet. After that call completes, ask the SAME Sidekick to use its previous context to create result.txt with the remembered contents, and verify result.txt equals seed.txt using a shell command. Give this second task without repeating the contents. Do not create other files. Review its evidence and read result.txt yourself before finishing. Report only the marker from result.txt.", marker)
	}
	files := map[string]string{}
	for _, name := range []string{"seed.txt", "result.txt", "tags.py", "test_tags.py", "ledger.py", "test_ledger.py", "input.csv", "summary.json"} {
		content, err := os.ReadFile(filepath.Join(workspace, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		files[name] = fmt.Sprintf("%x", sha256.Sum256(content))
		if name == "result.txt" && string(content) != marker+"\n" {
			t.Fatalf("result contents do not match the original random marker: %q", content)
		}
	}
	report["fileSHA256"] = files
	if (scenario == "transfer" || scenario == "restart") && files["result.txt"] == "" {
		t.Fatal("result.txt was not created")
	}
	runs, err := boot.Service.providers.subagents.store.List(ctx, boot.SessionID)
	if err != nil || len(runs) != expectedHandoffs {
		t.Fatalf("expected %d handoffs, got %d: %v", expectedHandoffs, len(runs), err)
	}
	children := []map[string]any{}
	slices.SortFunc(runs, func(a, b agentservice.SubagentRun) int { return a.StartedAt.Compare(b.StartedAt) })
	for _, run := range runs {
		if string(run.State) != "completed" || run.Provider != sidekick.Provider || run.Model != sidekick.Model || run.ToolCalls == 0 {
			t.Fatalf("invalid Sidekick handoff: state=%s provider=%s tools=%d", run.State, run.Provider, run.ToolCalls)
		}
		transcript, err := agentruntime.UnmarshalMessages(run.Transcript)
		if err != nil {
			t.Fatal(err)
		}
		if err := message.ValidateCompleteTurns(transcript); err != nil {
			t.Fatal(err)
		}
		calls, results := []message.ToolCall{}, []message.ToolResult{}
		for _, item := range transcript {
			calls = append(calls, item.ToolCalls...)
			if item.ToolResult != nil {
				results = append(results, *item.ToolResult)
			}
		}
		children = append(children, map[string]any{"id": run.ID, "childRunId": run.ChildRunID, "parentRunId": run.ParentRunID, "state": run.State, "provider": run.Provider, "model": run.Model, "reasoning": run.Reasoning, "toolCalls": run.ToolCalls, "turns": run.Turns, "tokens": run.TokensUsed, "output": run.Output, "transcriptCalls": calls, "transcriptResults": results})
		t.Logf("completed handoff=%s tools=%d turns=%d tokens=%d", run.ID, run.ToolCalls, run.Turns, run.TokensUsed)
	}
	report["sidekickRuns"] = children
	for i := 1; i < len(runs); i++ {
		if runs[0].Description != runs[i].Description {
			t.Fatal("Sidekick identity changed")
		}
		previous, current := children[i-1]["transcriptCalls"].([]message.ToolCall), children[i]["transcriptCalls"].([]message.ToolCall)
		if len(current) <= len(previous) {
			t.Fatal("handoff did not retain and extend the previous tool history")
		}
		for j, call := range previous {
			if call.ID != current[j].ID || call.Name != current[j].Name || string(call.Arguments) != string(current[j].Arguments) {
				t.Fatal("handoff changed the previous tool history")
			}
		}
	}
	if scenario == "restart" && strings.Contains(runs[0].Output, marker) {
		t.Fatal("first handoff disclosed the marker to the lead")
	}
	records, err := boot.Service.sessions.ListToolRecords(ctx, boot.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	report["toolRecords"] = records
	usage, err := boot.Service.sessions.UsageReport(ctx, session.UsageReportQuery{Scope: session.UsageScopeAll})
	if err != nil {
		t.Fatal(err)
	}
	report["usage"] = usage
	for _, route := range []config.ModelRouteConfig{lead, sidekick} {
		if !slices.ContainsFunc(usage.Models, func(row session.UsageModelRow) bool {
			return row.Provider == route.Provider && row.Model == route.Model && row.Requests > 0
		}) {
			t.Fatalf("missing usage for %s/%s", route.Provider, route.Model)
		}
	}
}

func liveFusionRoute(t *testing.T, ctx context.Context, service *Service, provider, modelID string) config.ModelRouteConfig {
	t.Helper()
	accounts, err := service.Authentication().Accounts(ctx, provider)
	if err != nil || len(accounts) != 1 {
		t.Fatalf("isolated %s account: %v", provider, err)
	}
	available, err := service.Catalog().List(ctx, provider, accounts[0].ID, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range available.Models {
		if model.ID != modelID {
			continue
		}
		depth := model.DefaultReasoning
		if slices.Contains(model.ReasoningLevels, "high") {
			depth = "high"
		}
		if err := service.setSubscriptionModelsEnabled(ctx, provider, []string{modelID}, true); err != nil {
			t.Fatal(err)
		}
		return config.ModelRouteConfig{Provider: provider, Model: modelID, Reasoning: depth}
	}
	t.Fatalf("requested model %s/%s is not available; no fallback permitted", provider, modelID)
	return config.ModelRouteConfig{}
}
