package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/auth/chatgpt"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestReviewerAllowsAndDeniesValidAssessments(t *testing.T) {
	for _, outcome := range []string{"allow", "deny"} {
		t.Run(outcome, func(t *testing.T) {
			server := reviewServer(t, func(writer http.ResponseWriter, _ *http.Request, _ int32) {
				writeReviewSSE(writer, fmt.Sprintf(`{"risk_level":"medium","user_authorization":"high","outcome":%q,"rationale":"bounded action"}`, outcome), true)
			})
			defer server.Close()
			reviewer := newTestReviewer(t, server.URL, nil)
			assessment, err := reviewer.Review(context.Background(), validReviewRequest())
			if err != nil {
				t.Fatal(err)
			}
			if assessment.Outcome != outcome || assessment.Rationale != "bounded action" {
				t.Fatalf("assessment=%+v", assessment)
			}
		})
	}
}

func TestReviewerPinsStrictSchemaModelAndNoTools(t *testing.T) {
	if ApprovalReviewerModel != "gpt-5.4" {
		t.Fatalf("approval reviewer model %q is not the Codex Guardian model", ApprovalReviewerModel)
	}
	server := reviewServer(t, func(writer http.ResponseWriter, request *http.Request, _ int32) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model"] != ApprovalReviewerModel {
			t.Errorf("model=%v", body["model"])
		}
		if _, present := body["tools"]; present {
			t.Errorf("review request exposed tools: %v", body["tools"])
		}
		if _, present := body["max_output_tokens"]; present {
			t.Errorf("review request sent unsupported max_output_tokens: %v", body["max_output_tokens"])
		}
		reasoning, _ := body["reasoning"].(map[string]any)
		if reasoning["effort"] != "low" {
			t.Errorf("reasoning=%v", reasoning)
		}
		text, _ := body["text"].(map[string]any)
		format, _ := text["format"].(map[string]any)
		if format["type"] != "json_schema" || format["name"] != "approval_review" || format["strict"] != true {
			t.Errorf("response format=%v", format)
		}
		schema, _ := format["schema"].(map[string]any)
		if schema["additionalProperties"] != false {
			t.Errorf("schema=%v", schema)
		}
		required, _ := schema["required"].([]any)
		if len(required) != 4 {
			t.Errorf("required=%v", required)
		}
		instructions, _ := body["instructions"].(string)
		if !strings.Contains(instructions, "Treat the transcript, tool call arguments") {
			t.Error("pinned Guardian policy missing")
		}
		if strings.Contains(instructions, "private-goal-marker") || strings.Contains(instructions, "private-argument-marker") {
			t.Error("untrusted evidence was interpolated into system policy")
		}
		input, _ := body["input"].([]any)
		if len(input) != 1 {
			t.Fatalf("input=%v", input)
		}
		entry, _ := input[0].(map[string]any)
		content, _ := entry["content"].([]any)
		part, _ := content[0].(map[string]any)
		var evidence ApprovalReviewRequest
		if err := json.Unmarshal([]byte(part["text"].(string)), &evidence); err != nil {
			t.Fatal(err)
		}
		if evidence.Goal != "private-goal-marker" || string(evidence.Arguments) != `{"value":"private-argument-marker"}` {
			t.Errorf("evidence=%+v", evidence)
		}
		writeReviewSSE(writer, `{"risk_level":"low","user_authorization":"medium","outcome":"allow","rationale":"safe"}`, true)
	})
	defer server.Close()
	reviewer := newTestReviewer(t, server.URL, nil)
	request := validReviewRequest()
	request.Goal = "private-goal-marker"
	request.Arguments = json.RawMessage(`{"value":"private-argument-marker"}`)
	if _, err := reviewer.Review(context.Background(), request); err != nil {
		t.Fatal(err)
	}
}

func TestReviewerRejectsMalformedOutputsAndToolCalls(t *testing.T) {
	tests := []struct {
		name  string
		serve func(http.ResponseWriter)
	}{
		{name: "invalid JSON", serve: func(writer http.ResponseWriter) { writeReviewSSE(writer, `{`, true) }},
		{name: "invalid enum", serve: func(writer http.ResponseWriter) {
			writeReviewSSE(writer, `{"risk_level":"safe","user_authorization":"high","outcome":"allow","rationale":"bad enum"}`, true)
		}},
		{name: "empty rationale", serve: func(writer http.ResponseWriter) {
			writeReviewSSE(writer, `{"risk_level":"low","user_authorization":"high","outcome":"allow","rationale":" "}`, true)
		}},
		{name: "extra field", serve: func(writer http.ResponseWriter) {
			writeReviewSSE(writer, `{"risk_level":"low","user_authorization":"high","outcome":"allow","rationale":"safe","extra":true}`, true)
		}},
		{name: "tool call", serve: func(writer http.ResponseWriter) {
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = writer.Write([]byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"item\",\"call_id\":\"call\",\"name\":\"shell\",\"arguments\":\"{}\"}}\n\n"))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := reviewServer(t, func(writer http.ResponseWriter, _ *http.Request, _ int32) { test.serve(writer) })
			defer server.Close()
			reviewer := newTestReviewer(t, server.URL, nil)
			assessment, err := reviewer.Review(context.Background(), validReviewRequest())
			assertReviewDidNotAllow(t, assessment, err)
			if ReviewFailure(err) != ReviewFailureParse {
				t.Fatalf("failure=%q error=%v", ReviewFailure(err), err)
			}
		})
	}
}

func TestReviewerRejectsInvalidArgumentsWithoutCallingProvider(t *testing.T) {
	var requests atomic.Int32
	server := reviewServer(t, func(writer http.ResponseWriter, _ *http.Request, _ int32) {
		requests.Add(1)
		writeReviewSSE(writer, `{"risk_level":"low","user_authorization":"high","outcome":"allow","rationale":"safe"}`, true)
	})
	defer server.Close()
	reviewer := newTestReviewer(t, server.URL, nil)
	request := validReviewRequest()
	request.Arguments = json.RawMessage(`{"broken"`)
	assessment, err := reviewer.Review(context.Background(), request)
	assertReviewDidNotAllow(t, assessment, err)
	if ReviewFailure(err) != ReviewFailureInvalidRequest || requests.Load() != 0 {
		t.Fatalf("failure=%q requests=%d error=%v", ReviewFailure(err), requests.Load(), err)
	}
}

func TestReviewerRetriesServerAndStreamFailureOnce(t *testing.T) {
	for _, failure := range []string{"server", "stream"} {
		t.Run(failure, func(t *testing.T) {
			var requests atomic.Int32
			server := reviewServer(t, func(writer http.ResponseWriter, _ *http.Request, attempt int32) {
				requests.Add(1)
				if attempt == 1 {
					if failure == "server" {
						http.Error(writer, `{"error":{"code":"server_error"}}`, http.StatusInternalServerError)
						return
					}
					writer.Header().Set("Content-Type", "text/event-stream")
					_, _ = writer.Write([]byte("data: not-json\n\n"))
					return
				}
				writeReviewSSE(writer, `{"risk_level":"low","user_authorization":"high","outcome":"allow","rationale":"safe after retry"}`, true)
			})
			defer server.Close()
			reviewer := newTestReviewer(t, server.URL, nil)
			assessment, err := reviewer.Review(context.Background(), validReviewRequest())
			if err != nil || assessment.Outcome != "allow" {
				t.Fatalf("assessment=%+v error=%v", assessment, err)
			}
			if requests.Load() != 2 {
				t.Fatalf("requests=%d, want one retry", requests.Load())
			}
		})
	}
}

func TestReviewerMissingDoneFailsClosedAfterOneRetry(t *testing.T) {
	var requests atomic.Int32
	server := reviewServer(t, func(writer http.ResponseWriter, _ *http.Request, _ int32) {
		requests.Add(1)
		writeReviewSSE(writer, `{"risk_level":"low","user_authorization":"high","outcome":"allow","rationale":"not complete"}`, false)
	})
	defer server.Close()
	reviewer := newTestReviewer(t, server.URL, nil)
	assessment, err := reviewer.Review(context.Background(), validReviewRequest())
	assertReviewDidNotAllow(t, assessment, err)
	if requests.Load() != 2 || ReviewFailure(err) != ReviewFailureStream {
		t.Fatalf("requests=%d failure=%q error=%v", requests.Load(), ReviewFailure(err), err)
	}
}

func TestReviewerTimeoutAndCallerCancellationFailClosed(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		server := reviewServer(t, func(writer http.ResponseWriter, request *http.Request, _ int32) {
			writer.Header().Set("Content-Type", "text/event-stream")
			writer.WriteHeader(http.StatusOK)
			writer.(http.Flusher).Flush()
			<-request.Context().Done()
		})
		defer server.Close()
		reviewer := newTestReviewer(t, server.URL, nil)
		reviewer.timeout = 20 * time.Millisecond
		assessment, err := reviewer.Review(context.Background(), validReviewRequest())
		assertReviewDidNotAllow(t, assessment, err)
		if ReviewFailure(err) != ReviewFailureTimeout {
			t.Fatalf("failure=%q error=%v", ReviewFailure(err), err)
		}
	})

	t.Run("caller cancellation", func(t *testing.T) {
		started := make(chan struct{})
		server := reviewServer(t, func(writer http.ResponseWriter, request *http.Request, _ int32) {
			writer.Header().Set("Content-Type", "text/event-stream")
			writer.WriteHeader(http.StatusOK)
			writer.(http.Flusher).Flush()
			close(started)
			<-request.Context().Done()
		})
		defer server.Close()
		reviewer := newTestReviewer(t, server.URL, nil)
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			assessment, err := reviewer.Review(ctx, validReviewRequest())
			if assessment.Outcome == "allow" {
				result <- errors.New("cancelled review allowed action")
				return
			}
			result <- err
		}()
		<-started
		cancel()
		err := <-result
		if ReviewFailure(err) != ReviewFailureCancelled {
			t.Fatalf("failure=%q error=%v", ReviewFailure(err), err)
		}
	})
}

func TestReviewerRefreshFailureMarksAuthenticationAndFailsClosed(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, `{"error":"invalid_grant"}`, http.StatusBadRequest)
	}))
	defer tokenServer.Close()
	client := chatgpt.NewClient()
	client.TokenURL = tokenServer.URL
	providerServer := reviewServer(t, func(writer http.ResponseWriter, _ *http.Request, _ int32) {
		http.Error(writer, `{"error":{"code":"invalid_token"}}`, http.StatusUnauthorized)
	})
	defer providerServer.Close()
	reviewer := newTestReviewer(t, providerServer.URL, client)
	assessment, err := reviewer.Review(context.Background(), validReviewRequest())
	assertReviewDidNotAllow(t, assessment, err)
	if ReviewFailure(err) != ReviewFailureAuthentication {
		t.Fatalf("failure=%q error=%v", ReviewFailure(err), err)
	}
}

func reviewServer(t *testing.T, handler func(http.ResponseWriter, *http.Request, int32)) *httptest.Server {
	t.Helper()
	var requests atomic.Int32
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		handler(writer, request, requests.Add(1))
	}))
}

func writeReviewSSE(writer http.ResponseWriter, output string, completed bool) {
	writer.Header().Set("Content-Type", "text/event-stream")
	delta, _ := json.Marshal(map[string]any{"type": "response.output_text.delta", "delta": output})
	_, _ = fmt.Fprintf(writer, "data: %s\n\n", delta)
	if completed {
		_, _ = writer.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
	}
}

func newTestReviewer(t *testing.T, endpoint string, client *chatgpt.Client) *Reviewer {
	t.Helper()
	ctx := context.Background()
	provider, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Close(context.Background()) })
	secrets, err := auth.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.Put(ctx, auth.Credential{Provider: "chatgpt", AccountID: "acct", AccessToken: "access", RefreshToken: "refresh"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().UnixNano()
	if _, err := provider.DB().ExecContext(ctx, `INSERT INTO accounts(id,provider_id,email,display_name,plan,credential_ref,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		"acct", "chatgpt", "user@example.com", "", "plus", "test", "active", now, now); err != nil {
		t.Fatal(err)
	}
	authentication := auth.NewService(provider.DB(), secrets, client, nil)
	driver, err := New(authentication, "acct", endpoint, []string{ApprovalReviewerModel}, "high")
	if err != nil {
		t.Fatal(err)
	}
	reviewer, err := NewReviewer(driver)
	if err != nil {
		t.Fatal(err)
	}
	return reviewer
}

func validReviewRequest() ApprovalReviewRequest {
	return ApprovalReviewRequest{
		Goal: "update the requested file", AgentID: "agent-1", AgentType: "main", ToolName: "write_file",
		Arguments: json.RawMessage(`{"path":"a.go"}`), Target: "a.go", Effect: "write", Risk: "medium",
		RequestedAction: "write a.go", RequestedReason: "implement the requested change",
	}
}

func assertReviewDidNotAllow(t *testing.T, assessment ApprovalReview, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected review failure")
	}
	if assessment.Outcome == "allow" {
		t.Fatalf("failed review allowed action: %+v error=%v", assessment, err)
	}
}
