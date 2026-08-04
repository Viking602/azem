package githubpr

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultReadTimeout  = 20 * time.Second
	defaultWriteTimeout = 90 * time.Second
	maxCommandOutput    = 8 << 20
)

const (
	summaryFields = "number,title,state,isDraft,author,headRefName,headRefOid,baseRefName,additions,deletions,changedFiles,reviewDecision,mergeable,mergeStateStatus,url,updatedAt,statusCheckRollup"
	detailFields  = summaryFields + ",body,createdAt,closedAt,mergedAt,maintainerCanModify,autoMergeRequest,reviewRequests,reviews,comments,commits,files"
)

// Runner is the process boundary used by Client. Implementations must execute
// argv directly without shell expansion.
type Runner interface {
	Run(context.Context, string, string, string, ...string) ([]byte, error)
}

type execRunner struct{}

type commandError struct {
	Name   string
	Args   []string
	Stderr string
	Err    error
}

func (e *commandError) Error() string {
	message := strings.TrimSpace(e.Stderr)
	if message == "" && e.Err != nil {
		message = e.Err.Error()
	}
	return fmt.Sprintf("%s %s: %s", e.Name, strings.Join(e.Args, " "), message)
}

func (e *commandError) Unwrap() error { return e.Err }

type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		return 0, fmt.Errorf("command output exceeds %d bytes", b.limit)
	}
	if len(data) > remaining {
		_, _ = b.Buffer.Write(data[:remaining])
		return remaining, fmt.Errorf("command output exceeds %d bytes", b.limit)
	}
	return b.Buffer.Write(data)
}

func (execRunner) Run(ctx context.Context, cwd, stdin, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = cwd
	if stdin != "" {
		command.Stdin = strings.NewReader(stdin)
	}
	stdout := &boundedBuffer{limit: maxCommandOutput}
	stderr := &boundedBuffer{limit: maxCommandOutput}
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return nil, &commandError{Name: name, Args: append([]string(nil), args...), Stderr: stderr.String(), Err: err}
	}
	return append([]byte(nil), stdout.Bytes()...), nil
}

// Client projects GitHub CLI data into stable desktop contracts.
type Client struct {
	workspace  string
	runner     Runner
	mu         sync.RWMutex
	repository *Repository
}

func NewClient(workspace string) *Client {
	return NewClientWithRunner(workspace, execRunner{})
}

func NewClientWithRunner(workspace string, runner Runner) *Client {
	if runner == nil {
		runner = execRunner{}
	}
	return &Client{workspace: strings.TrimSpace(workspace), runner: runner}
}

func (c *Client) Dashboard(ctx context.Context) (Dashboard, error) {
	dashboard := Dashboard{RefreshedAt: time.Now().UTC()}
	repository, err := c.resolveRepository(ctx)
	if err != nil {
		dashboard.Capability = capabilityFromError(err)
		return dashboard, nil
	}
	dashboard.Capability = Capability{Available: true}
	dashboard.Repository = repository

	branch, _, branchErr := c.workspaceState(ctx)
	if branchErr == nil {
		dashboard.CurrentBranch = branch
	}

	readCtx, cancel := context.WithTimeout(ctx, defaultReadTimeout)
	defer cancel()
	statusOutput, err := c.runner.Run(readCtx, c.workspace, "", "gh", "pr", "status", "--repo", repository.NameWithOwner, "--json", summaryFields)
	if err != nil {
		return Dashboard{Capability: capabilityFromError(err), Repository: repository, CurrentBranch: branch, RefreshedAt: dashboard.RefreshedAt}, nil
	}
	var status rawStatus
	if err := json.Unmarshal(statusOutput, &status); err != nil {
		return dashboard, fmt.Errorf("decode pull request status: %w", err)
	}
	if status.CurrentBranch != nil {
		current := normalizeSummary(*status.CurrentBranch)
		dashboard.Current = &current
		if dashboard.CurrentBranch == "" {
			dashboard.CurrentBranch = current.HeadRefName
		}
	}
	dashboard.CreatedByViewer = normalizeSummaries(status.CreatedBy)
	dashboard.NeedsReview = normalizeSummaries(status.NeedsReview)

	listCtx, listCancel := context.WithTimeout(ctx, defaultReadTimeout)
	defer listCancel()
	listOutput, err := c.runner.Run(listCtx, c.workspace, "", "gh", "pr", "list", "--repo", repository.NameWithOwner, "--state", "open", "--limit", "100", "--json", summaryFields)
	if err != nil {
		return dashboard, fmt.Errorf("list open pull requests: %w", err)
	}
	var open []rawPullRequest
	if err := json.Unmarshal(listOutput, &open); err != nil {
		return dashboard, fmt.Errorf("decode open pull requests: %w", err)
	}
	dashboard.Open = normalizeSummaries(open)
	if dashboard.Current == nil && dashboard.CurrentBranch != "" {
		for _, items := range [...][]PullRequestSummary{dashboard.CreatedByViewer, dashboard.NeedsReview, dashboard.Open} {
			for index := range items {
				if items[index].HeadRefName != dashboard.CurrentBranch {
					continue
				}
				current := items[index]
				dashboard.Current = &current
				break
			}
			if dashboard.Current != nil {
				break
			}
		}
	}
	return dashboard, nil
}

func (c *Client) Detail(ctx context.Context, number int) (PullRequest, error) {
	if number <= 0 {
		return PullRequest{}, fmt.Errorf("pull request number must be positive")
	}
	repository, err := c.resolveRepository(ctx)
	if err != nil {
		return PullRequest{}, err
	}
	readCtx, cancel := context.WithTimeout(ctx, defaultReadTimeout)
	defer cancel()
	output, err := c.runner.Run(readCtx, c.workspace, "", "gh", "pr", "view", strconv.Itoa(number), "--repo", repository.NameWithOwner, "--json", detailFields)
	if err != nil {
		return PullRequest{}, fmt.Errorf("read pull request #%d: %w", number, err)
	}
	var raw rawPullRequest
	if err := json.Unmarshal(output, &raw); err != nil {
		return PullRequest{}, fmt.Errorf("decode pull request #%d: %w", number, err)
	}
	return normalizePullRequest(raw, repository.AllowedMergeMethods), nil
}

func (c *Client) Mutate(ctx context.Context, request MutationRequest) (PullRequest, error) {
	if request.Number <= 0 {
		return PullRequest{}, fmt.Errorf("pull request number must be positive")
	}
	repository, err := c.resolveRepository(ctx)
	if err != nil {
		return PullRequest{}, err
	}
	number := strconv.Itoa(request.Number)
	args := []string{"pr"}
	stdin := ""

	switch request.Kind {
	case MutationEdit:
		title := strings.TrimSpace(request.Title)
		if title == "" {
			return PullRequest{}, fmt.Errorf("pull request title is required")
		}
		args = append(args, "edit", number, "--repo", repository.NameWithOwner, "--title", title, "--body-file", "-")
		stdin = request.Body
	case MutationAddReviewer, MutationRemoveReviewer:
		login := strings.TrimSpace(request.Login)
		if !validReviewer(login) {
			return PullRequest{}, fmt.Errorf("invalid GitHub reviewer %q", login)
		}
		flag := "--add-reviewer"
		if request.Kind == MutationRemoveReviewer {
			flag = "--remove-reviewer"
		}
		args = append(args, "edit", number, "--repo", repository.NameWithOwner, flag, login)
	case MutationComment:
		if strings.TrimSpace(request.Body) == "" {
			return PullRequest{}, fmt.Errorf("comment is empty")
		}
		args = append(args, "comment", number, "--repo", repository.NameWithOwner, "--body-file", "-")
		stdin = request.Body
	case MutationReview:
		reviewKind := strings.TrimSpace(request.ReviewKind)
		flag := ""
		switch reviewKind {
		case "approve":
			flag = "--approve"
		case "comment":
			flag = "--comment"
		case "request_changes":
			flag = "--request-changes"
		default:
			return PullRequest{}, fmt.Errorf("unsupported review kind %q", reviewKind)
		}
		if reviewKind != "approve" && strings.TrimSpace(request.Body) == "" {
			return PullRequest{}, fmt.Errorf("review body is required")
		}
		args = append(args, "review", number, "--repo", repository.NameWithOwner, flag)
		if request.Body != "" {
			args = append(args, "--body-file", "-")
			stdin = request.Body
		}
	case MutationReady:
		args = append(args, "ready", number, "--repo", repository.NameWithOwner)
	case MutationDraft:
		args = append(args, "ready", number, "--repo", repository.NameWithOwner, "--undo")
	case MutationClose:
		args = append(args, "close", number, "--repo", repository.NameWithOwner)
	case MutationReopen:
		args = append(args, "reopen", number, "--repo", repository.NameWithOwner)
	case MutationMerge, MutationEnableAutoMerge:
		method := strings.ToLower(strings.TrimSpace(request.MergeMethod))
		if !slices.Contains(repository.AllowedMergeMethods, method) {
			return PullRequest{}, fmt.Errorf("merge method %q is not enabled for %s", method, repository.NameWithOwner)
		}
		headOID := strings.TrimSpace(request.ExpectedHeadOID)
		if !validOID(headOID) {
			return PullRequest{}, fmt.Errorf("a valid expected head commit is required")
		}
		args = append(args, "merge", number, "--repo", repository.NameWithOwner, "--"+method, "--match-head-commit", headOID)
		if request.Kind == MutationEnableAutoMerge {
			args = append(args, "--auto")
		}
	case MutationDisableAutoMerge:
		args = append(args, "merge", number, "--repo", repository.NameWithOwner, "--disable-auto")
	default:
		return PullRequest{}, fmt.Errorf("unsupported pull request mutation %q", request.Kind)
	}

	writeCtx, cancel := context.WithTimeout(ctx, defaultWriteTimeout)
	defer cancel()
	if _, err := c.runner.Run(writeCtx, c.workspace, stdin, "gh", args...); err != nil {
		return PullRequest{}, fmt.Errorf("mutate pull request #%d: %w", request.Number, err)
	}
	return c.Detail(ctx, request.Number)
}

func (c *Client) resolveRepository(ctx context.Context) (*Repository, error) {
	c.mu.RLock()
	cached := c.repository
	c.mu.RUnlock()
	if cached != nil {
		copy := *cached
		copy.AllowedMergeMethods = append([]string(nil), cached.AllowedMergeMethods...)
		return &copy, nil
	}

	readCtx, cancel := context.WithTimeout(ctx, defaultReadTimeout)
	defer cancel()
	output, err := c.runner.Run(readCtx, c.workspace, "", "gh", "repo", "view", "--json", "nameWithOwner,url,defaultBranchRef,viewerPermission,mergeCommitAllowed,rebaseMergeAllowed,squashMergeAllowed")
	if err != nil {
		return nil, fmt.Errorf("resolve GitHub repository: %w", err)
	}
	var raw rawRepository
	if err := json.Unmarshal(output, &raw); err != nil {
		return nil, fmt.Errorf("decode GitHub repository: %w", err)
	}
	if strings.TrimSpace(raw.NameWithOwner) == "" {
		return nil, fmt.Errorf("GitHub repository has no owner/name")
	}
	repository := &Repository{
		NameWithOwner:    raw.NameWithOwner,
		URL:              raw.URL,
		DefaultBranch:    raw.DefaultBranchRef.Name,
		ViewerPermission: raw.ViewerPermission,
	}
	if raw.MergeCommitAllowed {
		repository.AllowedMergeMethods = append(repository.AllowedMergeMethods, "merge")
	}
	if raw.SquashMergeAllowed {
		repository.AllowedMergeMethods = append(repository.AllowedMergeMethods, "squash")
	}
	if raw.RebaseMergeAllowed {
		repository.AllowedMergeMethods = append(repository.AllowedMergeMethods, "rebase")
	}
	viewerCtx, viewerCancel := context.WithTimeout(ctx, defaultReadTimeout)
	viewerOutput, viewerErr := c.runner.Run(viewerCtx, c.workspace, "", "gh", "api", "user", "--jq", ".login")
	viewerCancel()
	if viewerErr == nil {
		repository.ViewerLogin = strings.TrimSpace(string(viewerOutput))
	}
	c.mu.Lock()
	c.repository = repository
	c.mu.Unlock()
	copy := *repository
	copy.AllowedMergeMethods = append([]string(nil), repository.AllowedMergeMethods...)
	return &copy, nil
}

func (c *Client) workspaceState(ctx context.Context) (string, bool, error) {
	readCtx, cancel := context.WithTimeout(ctx, defaultReadTimeout)
	defer cancel()
	branchOutput, err := c.runner.Run(readCtx, c.workspace, "", "git", "branch", "--show-current")
	if err != nil {
		return "", false, fmt.Errorf("read current git branch: %w", err)
	}
	statusOutput, err := c.runner.Run(readCtx, c.workspace, "", "git", "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return "", false, fmt.Errorf("read git workspace status: %w", err)
	}
	return strings.TrimSpace(string(branchOutput)), len(statusOutput) > 0, nil
}

func (c *Client) repairIssue(pr PullRequest) RepairIssue {
	issue := RepairIssue{Conflict: strings.EqualFold(pr.Mergeable, "CONFLICTING") || strings.EqualFold(pr.MergeStateStatus, "DIRTY")}
	for _, check := range pr.ChecksDetail {
		if check.Category == "failing" {
			issue.FailingChecks = append(issue.FailingChecks, check.Name)
		}
	}
	sort.Strings(issue.FailingChecks)
	payload := strings.Join([]string{pr.HeadRefOID, strconv.FormatBool(issue.Conflict), strings.Join(issue.FailingChecks, "\x00")}, "\x01")
	digest := sha256.Sum256([]byte(payload))
	issue.Fingerprint = hex.EncodeToString(digest[:])
	return issue
}

func capabilityFromError(err error) Capability {
	message := strings.TrimSpace(err.Error())
	lower := strings.ToLower(message)
	capability := Capability{Available: false, Code: "error", Message: message}
	switch {
	case errors.Is(err, exec.ErrNotFound) || strings.Contains(lower, "executable file not found") || strings.Contains(lower, `executable "gh" not found`):
		capability.Code = "not_installed"
	case strings.Contains(lower, "not logged into") || strings.Contains(lower, "authentication") || strings.Contains(lower, "http 401") || strings.Contains(lower, "bad credentials"):
		capability.Code = "unauthenticated"
	case strings.Contains(lower, "not a git repository") || strings.Contains(lower, "no git remotes") || strings.Contains(lower, "could not determine base repo") || strings.Contains(lower, "none of the git remotes"):
		capability.Code = "no_repository"
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "tls handshake") || strings.Contains(lower, "connection") || strings.Contains(lower, "network is unreachable"):
		capability.Code = "offline"
	}
	return capability
}

var (
	reviewerPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})(?:/[A-Za-z0-9](?:[A-Za-z0-9-]{0,99}))?$`)
	oidPattern      = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)
)

func validReviewer(value string) bool { return reviewerPattern.MatchString(value) }
func validOID(value string) bool      { return oidPattern.MatchString(value) }

type rawRepository struct {
	NameWithOwner      string `json:"nameWithOwner"`
	URL                string `json:"url"`
	ViewerPermission   string `json:"viewerPermission"`
	MergeCommitAllowed bool   `json:"mergeCommitAllowed"`
	RebaseMergeAllowed bool   `json:"rebaseMergeAllowed"`
	SquashMergeAllowed bool   `json:"squashMergeAllowed"`
	DefaultBranchRef   struct {
		Name string `json:"name"`
	} `json:"defaultBranchRef"`
}

type rawActor struct {
	Login     string `json:"login"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	AvatarURL string `json:"avatarUrl"`
	IsBot     bool   `json:"is_bot"`
}

type rawPullRequest struct {
	Number              int              `json:"number"`
	Title               string           `json:"title"`
	Body                string           `json:"body"`
	State               string           `json:"state"`
	IsDraft             bool             `json:"isDraft"`
	Author              rawActor         `json:"author"`
	HeadRefName         string           `json:"headRefName"`
	HeadRefOID          string           `json:"headRefOid"`
	BaseRefName         string           `json:"baseRefName"`
	Additions           int              `json:"additions"`
	Deletions           int              `json:"deletions"`
	ChangedFiles        int              `json:"changedFiles"`
	ReviewDecision      string           `json:"reviewDecision"`
	Mergeable           string           `json:"mergeable"`
	MergeStateStatus    string           `json:"mergeStateStatus"`
	URL                 string           `json:"url"`
	CreatedAt           time.Time        `json:"createdAt"`
	UpdatedAt           time.Time        `json:"updatedAt"`
	ClosedAt            time.Time        `json:"closedAt"`
	MergedAt            time.Time        `json:"mergedAt"`
	MaintainerCanModify bool             `json:"maintainerCanModify"`
	AutoMergeRequest    *rawAutoMerge    `json:"autoMergeRequest"`
	ReviewRequests      []rawActor       `json:"reviewRequests"`
	Reviews             []rawReview      `json:"reviews"`
	Comments            []rawComment     `json:"comments"`
	Commits             []rawCommit      `json:"commits"`
	Files               []rawFile        `json:"files"`
	StatusCheckRollup   []map[string]any `json:"statusCheckRollup"`
}

type rawStatus struct {
	CurrentBranch *rawPullRequest  `json:"currentBranch"`
	CreatedBy     []rawPullRequest `json:"createdBy"`
	NeedsReview   []rawPullRequest `json:"needsReview"`
}

type rawAutoMerge struct {
	MergeMethod string `json:"mergeMethod"`
}

type rawReview struct {
	ID          string    `json:"id"`
	Author      rawActor  `json:"author"`
	State       string    `json:"state"`
	Body        string    `json:"body"`
	SubmittedAt time.Time `json:"submittedAt"`
	URL         string    `json:"url"`
}

type rawComment struct {
	ID              string    `json:"id"`
	Author          rawActor  `json:"author"`
	Body            string    `json:"body"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
	URL             string    `json:"url"`
	ViewerCanEdit   bool      `json:"viewerCanEdit"`
	ViewerCanDelete bool      `json:"viewerCanDelete"`
}

type rawCommit struct {
	OID             string     `json:"oid"`
	MessageHeadline string     `json:"messageHeadline"`
	MessageBody     string     `json:"messageBody"`
	AuthoredDate    time.Time  `json:"authoredDate"`
	CommittedDate   time.Time  `json:"committedDate"`
	Authors         []rawActor `json:"authors"`
}

type rawFile struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

func normalizeSummaries(items []rawPullRequest) []PullRequestSummary {
	result := make([]PullRequestSummary, 0, len(items))
	for _, item := range items {
		result = append(result, normalizeSummary(item))
	}
	return result
}

func normalizeSummary(raw rawPullRequest) PullRequestSummary {
	checks := normalizeChecks(raw.StatusCheckRollup)
	return PullRequestSummary{
		Number: raw.Number, Title: raw.Title, State: strings.ToUpper(raw.State), Draft: raw.IsDraft,
		Author: normalizeActor(raw.Author), HeadRefName: raw.HeadRefName, HeadRefOID: raw.HeadRefOID,
		BaseRefName: raw.BaseRefName, Additions: raw.Additions, Deletions: raw.Deletions,
		ChangedFiles: raw.ChangedFiles, ReviewDecision: strings.ToUpper(raw.ReviewDecision),
		Mergeable: strings.ToUpper(raw.Mergeable), MergeStateStatus: strings.ToUpper(raw.MergeStateStatus),
		URL: raw.URL, UpdatedAt: raw.UpdatedAt, Checks: summarizeChecks(checks),
	}
}

func normalizePullRequest(raw rawPullRequest, allowedMergeMethods []string) PullRequest {
	checks := normalizeChecks(raw.StatusCheckRollup)
	pr := PullRequest{
		PullRequestSummary: normalizeSummary(raw), Body: raw.Body, CreatedAt: raw.CreatedAt,
		ClosedAt: raw.ClosedAt, MergedAt: raw.MergedAt, MaintainerCanModify: raw.MaintainerCanModify,
		ReviewRequests: make([]Actor, 0, len(raw.ReviewRequests)), Reviews: make([]Review, 0, len(raw.Reviews)),
		Comments: make([]Comment, 0, len(raw.Comments)), Commits: make([]Commit, 0, len(raw.Commits)),
		Files: make([]File, 0, len(raw.Files)), ChecksDetail: checks,
		AllowedMergeMethods: append([]string(nil), allowedMergeMethods...),
	}
	if raw.AutoMergeRequest != nil {
		pr.AutoMergeEnabled = true
		pr.AutoMergeMethod = strings.ToLower(raw.AutoMergeRequest.MergeMethod)
	}
	for _, actor := range raw.ReviewRequests {
		pr.ReviewRequests = append(pr.ReviewRequests, normalizeActor(actor))
	}
	for _, review := range raw.Reviews {
		pr.Reviews = append(pr.Reviews, Review{ID: review.ID, Author: normalizeActor(review.Author), State: strings.ToUpper(review.State), Body: review.Body, SubmittedAt: review.SubmittedAt, URL: review.URL})
	}
	for _, comment := range raw.Comments {
		pr.Comments = append(pr.Comments, Comment{ID: comment.ID, Author: normalizeActor(comment.Author), Body: comment.Body, CreatedAt: comment.CreatedAt, UpdatedAt: comment.UpdatedAt, URL: comment.URL, ViewerCanEdit: comment.ViewerCanEdit, ViewerCanDelete: comment.ViewerCanDelete})
	}
	for _, commit := range raw.Commits {
		authors := make([]Actor, 0, len(commit.Authors))
		for _, author := range commit.Authors {
			authors = append(authors, normalizeActor(author))
		}
		pr.Commits = append(pr.Commits, Commit{OID: commit.OID, Headline: commit.MessageHeadline, Body: commit.MessageBody, AuthoredAt: commit.AuthoredDate, CommittedAt: commit.CommittedDate, Authors: authors})
	}
	for _, file := range raw.Files {
		pr.Files = append(pr.Files, File{Path: file.Path, Additions: file.Additions, Deletions: file.Deletions})
	}
	pr.Activity = buildActivity(pr)
	return pr
}

func normalizeActor(raw rawActor) Actor {
	login := strings.TrimSpace(raw.Login)
	avatarURL := strings.TrimSpace(raw.AvatarURL)
	if avatarURL == "" && login != "" {
		avatarURL = "https://avatars.githubusercontent.com/" + url.PathEscape(login) + "?size=64"
	}
	return Actor{Login: login, Name: raw.Name, URL: raw.URL, AvatarURL: avatarURL, Bot: raw.IsBot}
}

func normalizeChecks(items []map[string]any) []Check {
	checks := make([]Check, 0, len(items))
	for _, item := range items {
		name := firstString(item, "name", "context")
		if name == "" {
			continue
		}
		status := strings.ToUpper(firstString(item, "status", "state"))
		conclusion := strings.ToUpper(firstString(item, "conclusion", "state"))
		check := Check{
			Name: name, Workflow: firstString(item, "workflowName"), Status: status,
			Conclusion: conclusion, URL: firstString(item, "detailsUrl", "targetUrl"),
			StartedAt: parseTime(firstString(item, "startedAt")), CompletedAt: parseTime(firstString(item, "completedAt")),
		}
		check.Category = checkCategory(status, conclusion)
		checks = append(checks, check)
	}
	sort.SliceStable(checks, func(left, right int) bool {
		if checks[left].Category != checks[right].Category {
			return checkRank(checks[left].Category) < checkRank(checks[right].Category)
		}
		return checks[left].Name < checks[right].Name
	})
	return checks
}

func summarizeChecks(checks []Check) CheckSummary {
	summary := CheckSummary{Total: len(checks)}
	for _, check := range checks {
		switch check.Category {
		case "passing":
			summary.Passing++
		case "failing":
			summary.Failing++
		case "neutral":
			summary.Neutral++
		case "skipped":
			summary.Skipped++
		default:
			summary.Pending++
		}
	}
	return summary
}

func checkCategory(status, conclusion string) string {
	value := strings.ToUpper(strings.TrimSpace(conclusion))
	if value == "" {
		value = strings.ToUpper(strings.TrimSpace(status))
	}
	switch value {
	case "SUCCESS":
		return "passing"
	case "FAILURE", "ERROR", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE", "STALE":
		return "failing"
	case "NEUTRAL", "CANCELLED":
		return "neutral"
	case "SKIPPED":
		return "skipped"
	default:
		return "pending"
	}
}

func checkRank(category string) int {
	switch category {
	case "failing":
		return 0
	case "pending":
		return 1
	case "passing":
		return 2
	default:
		return 3
	}
}

func buildActivity(pr PullRequest) []Activity {
	activity := []Activity{{Kind: "created", Actor: pr.Author, Title: "opened pull request", At: pr.CreatedAt, URL: pr.URL}}
	for _, commit := range pr.Commits {
		actor := Actor{}
		if len(commit.Authors) > 0 {
			actor = commit.Authors[0]
		}
		activity = append(activity, Activity{Kind: "commit", Actor: actor, Title: commit.Headline, Body: commit.Body, OID: commit.OID, At: firstTime(commit.CommittedAt, commit.AuthoredAt)})
	}
	for _, review := range pr.Reviews {
		activity = append(activity, Activity{Kind: "review", Actor: review.Author, Title: "submitted a review", Body: review.Body, State: review.State, URL: review.URL, At: review.SubmittedAt})
	}
	for _, comment := range pr.Comments {
		activity = append(activity, Activity{Kind: "comment", Actor: comment.Author, Title: "commented", Body: comment.Body, URL: comment.URL, At: comment.CreatedAt})
	}
	if !pr.MergedAt.IsZero() {
		activity = append(activity, Activity{Kind: "merged", Title: "merged pull request", At: pr.MergedAt, URL: pr.URL})
	} else if !pr.ClosedAt.IsZero() {
		activity = append(activity, Activity{Kind: "closed", Title: "closed pull request", At: pr.ClosedAt, URL: pr.URL})
	}
	sort.SliceStable(activity, func(left, right int) bool { return activity[left].At.Before(activity[right].At) })
	return activity
}

func firstString(item map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := item[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func parseTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339, value)
	return parsed
}

func firstTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

var _ io.Writer = (*boundedBuffer)(nil)
