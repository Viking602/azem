//go:build live

package devin_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptrace"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/provider/catalog"
	"github.com/Viking602/azem/internal/provider/devin"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
	_ "modernc.org/sqlite"
)

// Opt-in read-only credential/catalog access; no workspace tools or user state writes.
func TestLivePersonalGreeting(t *testing.T) {
	if os.Getenv("AZEM_LIVE_DEVIN") != "1" {
		t.Skip("set AZEM_LIVE_DEVIN=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
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
	accounts, err := source.Accounts(ctx, "devin")
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range accounts {
		if account.Status != "active" {
			continue
		}
		credential, err := source.StoredCredential(ctx, "devin", account.ID)
		if err != nil {
			t.Fatal(err)
		}
		modelID := os.Getenv("AZEM_LIVE_DEVIN_MODEL")
		if modelID == "" {
			modelID = "swe-2-max"
		}
		var raw []byte
		if err := db.QueryRowContext(ctx, "SELECT data FROM model_catalog WHERE provider_id='devin' AND account_id=? AND model_id=?", account.ID, modelID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var model catalog.Model
		if err := json.Unmarshal(raw, &model); err != nil {
			t.Fatal(err)
		}
		driver, err := devin.New(func(context.Context) (string, error) { return credential.AccessToken, nil }, account.ID, model)
		if err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		request := hyprovider.Request{Model: modelID, MaxTokens: model.MaxOutputTokens, PromptCacheKey: fmt.Sprintf("devin-probe-%d", started.UnixNano()), Messages: []message.Message{message.NewText(message.RoleUser, "你好")}}
		if prompt := os.Getenv("AZEM_LIVE_DEVIN_PROMPT"); prompt != "" {
			request.Messages = []message.Message{message.NewText(message.RoleUser, prompt)}
		}
		if os.Getenv("AZEM_LIVE_DEVIN_LATE_SYSTEM") == "1" {
			request.Messages = append(request.Messages, message.NewText(message.RoleSystem, "Trusted host context: the workspace is ready."))
		}
		if limit, _ := strconv.Atoi(os.Getenv("AZEM_LIVE_DEVIN_MAX_OUTPUT")); limit > 0 {
			request.MaxTokens = limit
		}
		if name := os.Getenv("AZEM_LIVE_DEVIN_TOOL"); name != "" {
			request.Tools = []message.ToolDefinition{{Name: name, Description: "Read a file", InputSchema: message.JSONSchema{Type: "object", Properties: map[string]message.JSONSchema{"path": {Type: "string"}}, Required: []string{"path"}}}}
		}
		if path := os.Getenv("AZEM_LIVE_DEVIN_TOOL_DEFS"); path != "" {
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(body, &request.Tools); err != nil {
				t.Fatal(err)
			}
			if selected := os.Getenv("AZEM_LIVE_DEVIN_TOOL_FILTER"); selected != "" {
				filtered := []message.ToolDefinition{}
				for _, tool := range request.Tools {
					if strings.Contains(","+selected+",", ","+tool.Name+",") {
						filtered = append(filtered, tool)
					}
				}
				request.Tools = filtered
			}
			t.Logf("tools=%d", len(request.Tools))
		}
		if path := os.Getenv("AZEM_LIVE_DEVIN_CHECKPOINT"); path != "" {
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var saved struct {
				Execution struct {
					Checkpoint struct {
						Continuation struct{ Messages []message.Message }
					}
				}
			}
			if err := json.Unmarshal(body, &saved); err != nil {
				t.Fatal(err)
			}
			request.Messages = saved.Execution.Checkpoint.Continuation.Messages
			if len(request.Messages) == 0 {
				t.Fatal("empty saved messages")
			}
		}
		rounds, _ := strconv.Atoi(os.Getenv("AZEM_LIVE_DEVIN_ROUNDS"))
		for round := 0; round < max(1, min(3, rounds)); round++ {
			started = time.Now()
			var traceMu sync.Mutex
			trace := &httptrace.ClientTrace{
				GotConn: func(info httptrace.GotConnInfo) {
					traceMu.Lock()
					defer traceMu.Unlock()
					t.Logf("connection=%s reused=%v", time.Since(started), info.Reused)
				},
				WroteRequest: func(info httptrace.WroteRequestInfo) {
					traceMu.Lock()
					defer traceMu.Unlock()
					t.Logf("request_written=%s error=%v", time.Since(started), info.Err)
				},
				GotFirstResponseByte: func() { traceMu.Lock(); defer traceMu.Unlock(); t.Logf("response_byte=%s", time.Since(started)) },
			}
			stream, err := driver.Stream(httptrace.WithClientTrace(ctx, trace), request)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			t.Logf("stream_open=%s model=%s max_output=%d", time.Since(started), modelID, request.MaxTokens)
			text, done, first := "", false, false
			calls := 0
			for {
				event, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("after %s: %v", time.Since(started), err)
				}
				if !first && (event.Text != "" || event.Thinking != "") {
					t.Logf("first_output=%s", time.Since(started))
					first = true
				}
				text += event.Text
				if event.Kind == hyprovider.EventToolCall {
					calls++
					t.Logf("tool=%s", event.ToolCall.Name)
				}
				if event.Kind == hyprovider.EventError {
					t.Fatalf("after %s: %v", time.Since(started), event.Err)
				}
				if event.Kind == hyprovider.EventDone {
					done = true
					t.Logf("round=%d completed=%s input=%d cached=%d output=%d", round+1, time.Since(started), event.Usage.InputTokens, event.Usage.CachedInputTokens, event.Usage.OutputTokens)
				}
			}
			if !done || text == "" && calls == 0 {
				t.Fatalf("missing completion or answer: completed=%v text_bytes=%d", done, len(text))
			}
			t.Logf("answer=%s", text)
			stream.Close()
		}
		return
	}
	t.Fatal("no active Devin account")
}
