package githubwebhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

const maxPayloadBytes = 2 << 20

type Trigger interface {
	Trigger(number int) error
}

type Options struct {
	DB           *sql.DB
	Secret       string
	Repositories []string
	Trigger      Trigger
	Version      string
}

type Server struct {
	db           *sql.DB
	secret       []byte
	repositories map[string]struct{}
	trigger      Trigger
	version      string
	now          func() time.Time
}

type Receipt struct {
	DeliveryID string    `json:"deliveryId"`
	Event      string    `json:"event"`
	Repository string    `json:"repository"`
	Number     int       `json:"pullRequestNumber,omitempty"`
	Action     string    `json:"action,omitempty"`
	Status     string    `json:"status"`
	ReceivedAt time.Time `json:"receivedAt"`
}

func New(options Options) (*Server, error) {
	if options.DB == nil || options.Trigger == nil {
		return nil, errors.New("GitHub webhook service requires database and repair trigger")
	}
	secret := strings.TrimSpace(options.Secret)
	if len(secret) < 32 || strings.ContainsAny(secret, "\r\n\x00") {
		return nil, errors.New("GitHub webhook secret must contain at least 32 safe characters")
	}
	repositories := make(map[string]struct{}, len(options.Repositories))
	for _, repository := range options.Repositories {
		repository = strings.ToLower(strings.TrimSpace(repository))
		if !repositoryPattern.MatchString(repository) {
			return nil, fmt.Errorf("invalid GitHub repository %q", repository)
		}
		repositories[repository] = struct{}{}
	}
	if len(repositories) == 0 {
		return nil, errors.New("GitHub webhook service requires a repository allowlist")
	}
	if options.Version == "" {
		options.Version = "dev"
	}
	return &Server{db: options.DB, secret: []byte(secret), repositories: repositories, trigger: options.Trigger, version: options.Version, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "version": server.version})
	})
	mux.HandleFunc("POST /webhook", server.webhook)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(writer, request)
	})
}

func (server *Server) webhook(writer http.ResponseWriter, request *http.Request) {
	payload, err := io.ReadAll(io.LimitReader(request.Body, maxPayloadBytes+1))
	request.Body.Close()
	if err != nil || len(payload) > maxPayloadBytes {
		writeError(writer, http.StatusRequestEntityTooLarge, "payload is oversized")
		return
	}
	if !server.verify(request.Header.Get("X-Hub-Signature-256"), payload) {
		writeError(writer, http.StatusUnauthorized, "invalid signature")
		return
	}
	deliveryID := strings.TrimSpace(request.Header.Get("X-GitHub-Delivery"))
	eventName := strings.ToLower(strings.TrimSpace(request.Header.Get("X-GitHub-Event")))
	if !deliveryPattern.MatchString(deliveryID) || !eventPattern.MatchString(eventName) {
		writeError(writer, http.StatusBadRequest, "delivery id and event are required")
		return
	}
	var value webhookPayload
	if json.Unmarshal(payload, &value) != nil || value.Repository.FullName == "" {
		writeError(writer, http.StatusBadRequest, "invalid webhook payload")
		return
	}
	repository := strings.ToLower(value.Repository.FullName)
	if _, allowed := server.repositories[repository]; !allowed {
		writeError(writer, http.StatusForbidden, "repository is not allowed")
		return
	}
	numbers := value.pullRequests()
	number := 0
	if len(numbers) > 0 {
		number = numbers[0]
	}
	conclusion := firstNonempty(value.Conclusion, value.CheckRun.Conclusion, value.CheckSuite.Conclusion, value.WorkflowRun.Conclusion)
	relevant := relevantEvent(eventName, value.Action, conclusion, value.Review.State, number)
	status := "ignored"
	if relevant {
		status = "queued"
	}
	digest := sha256.Sum256(payload)
	receivedAt := server.now()
	result, err := server.db.ExecContext(request.Context(), `
		INSERT INTO github_webhook_deliveries(delivery_id,event_name,repository,pull_request_number,action,payload_sha256,status,received_at)
		VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(delivery_id) DO NOTHING
	`, deliveryID, eventName, repository, number, value.Action, hex.EncodeToString(digest[:]), status, receivedAt.UnixNano())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, err.Error())
		return
	}
	inserted, _ := result.RowsAffected()
	if inserted == 0 {
		writeJSON(writer, http.StatusAccepted, map[string]any{"deliveryId": deliveryID, "duplicate": true})
		return
	}
	if relevant {
		if err := server.trigger.Trigger(number); err != nil {
			_, _ = server.db.ExecContext(request.Context(), `UPDATE github_webhook_deliveries SET status='rejected' WHERE delivery_id=?`, deliveryID)
			writeError(writer, http.StatusConflict, err.Error())
			return
		}
	}
	writeJSON(writer, http.StatusAccepted, map[string]any{"deliveryId": deliveryID, "queued": relevant, "pullRequestNumber": number})
}

func (server *Server) verify(header string, payload []byte) bool {
	if !strings.HasPrefix(header, "sha256=") {
		return false
	}
	presented, err := hex.DecodeString(strings.TrimPrefix(header, "sha256="))
	if err != nil || len(presented) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, server.secret)
	_, _ = mac.Write(payload)
	return hmac.Equal(presented, mac.Sum(nil))
}

func (server *Server) Receipts(ctx context.Context, limit int) ([]Receipt, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := server.db.QueryContext(ctx, `SELECT delivery_id,event_name,repository,pull_request_number,action,status,received_at FROM github_webhook_deliveries ORDER BY received_at DESC,delivery_id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]Receipt, 0)
	for rows.Next() {
		var value Receipt
		var received int64
		if err := rows.Scan(&value.DeliveryID, &value.Event, &value.Repository, &value.Number, &value.Action, &value.Status, &received); err != nil {
			return nil, err
		}
		value.ReceivedAt = time.Unix(0, received).UTC()
		values = append(values, value)
	}
	return values, rows.Err()
}

type webhookPayload struct {
	Action     string `json:"action"`
	Conclusion string `json:"conclusion"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	PullRequest struct {
		Number int `json:"number"`
	} `json:"pull_request"`
	CheckRun struct {
		Conclusion   string `json:"conclusion"`
		PullRequests []struct {
			Number int `json:"number"`
		} `json:"pull_requests"`
	} `json:"check_run"`
	CheckSuite struct {
		Conclusion   string `json:"conclusion"`
		PullRequests []struct {
			Number int `json:"number"`
		} `json:"pull_requests"`
	} `json:"check_suite"`
	WorkflowRun struct {
		Conclusion   string `json:"conclusion"`
		PullRequests []struct {
			Number int `json:"number"`
		} `json:"pull_requests"`
	} `json:"workflow_run"`
	Review struct {
		State string `json:"state"`
	} `json:"review"`
}

func (value webhookPayload) pullRequests() []int {
	set := make(map[int]struct{})
	if value.PullRequest.Number > 0 {
		set[value.PullRequest.Number] = struct{}{}
	}
	for _, values := range [][]struct {
		Number int `json:"number"`
	}{value.CheckRun.PullRequests, value.CheckSuite.PullRequests, value.WorkflowRun.PullRequests} {
		for _, pullRequest := range values {
			if pullRequest.Number > 0 {
				set[pullRequest.Number] = struct{}{}
			}
		}
	}
	numbers := make([]int, 0, len(set))
	for number := range set {
		numbers = append(numbers, number)
	}
	sort.Ints(numbers)
	return numbers
}

func relevantEvent(event, action, conclusion, reviewState string, number int) bool {
	if number <= 0 {
		return false
	}
	conclusion = firstNonempty(conclusion)
	switch event {
	case "check_run", "check_suite", "workflow_run":
		return action == "completed" && conclusion != "" && conclusion != "success" && conclusion != "neutral" && conclusion != "skipped"
	case "pull_request":
		return action == "synchronize" || action == "reopened" || action == "ready_for_review"
	case "pull_request_review":
		return action == "submitted" && strings.EqualFold(reviewState, "changes_requested")
	}
	return false
}

var (
	deliveryPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	eventPattern      = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
	repositoryPattern = regexp.MustCompile(`^[a-z0-9_.-]+/[a-z0-9_.-]+$`)
)

func firstNonempty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.ToLower(strings.TrimSpace(value))
		}
	}
	return ""
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, status int, message string) {
	writeJSON(writer, status, map[string]string{"error": message})
}
