package githubpr

import "time"

// Capability describes whether the current workspace can use GitHub pull requests.
type Capability struct {
	Available bool   `json:"available"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
}

// Repository is the GitHub repository resolved from the workspace remote.
type Repository struct {
	NameWithOwner       string   `json:"nameWithOwner"`
	URL                 string   `json:"url"`
	DefaultBranch       string   `json:"defaultBranch"`
	ViewerPermission    string   `json:"viewerPermission"`
	ViewerLogin         string   `json:"viewerLogin"`
	AllowedMergeMethods []string `json:"allowedMergeMethods"`
}

// Actor is a GitHub user or bot shown in PR metadata and activity.
type Actor struct {
	Login     string `json:"login"`
	Name      string `json:"name,omitempty"`
	URL       string `json:"url,omitempty"`
	AvatarURL string `json:"avatarUrl,omitempty"`
	Bot       bool   `json:"bot,omitempty"`
}

// Check is a normalized check run or commit status context.
type Check struct {
	Name        string    `json:"name"`
	Workflow    string    `json:"workflow,omitempty"`
	Status      string    `json:"status,omitempty"`
	Conclusion  string    `json:"conclusion,omitempty"`
	Category    string    `json:"category"`
	URL         string    `json:"url,omitempty"`
	StartedAt   time.Time `json:"startedAt,omitempty"`
	CompletedAt time.Time `json:"completedAt,omitempty"`
}

// CheckSummary is the aggregate state rendered in PR rows and details.
type CheckSummary struct {
	Total   int `json:"total"`
	Pending int `json:"pending"`
	Passing int `json:"passing"`
	Failing int `json:"failing"`
	Neutral int `json:"neutral"`
	Skipped int `json:"skipped"`
}

// PullRequestSummary is the compact PR representation used by lists and detection.
type PullRequestSummary struct {
	Number           int          `json:"number"`
	Title            string       `json:"title"`
	State            string       `json:"state"`
	Draft            bool         `json:"draft"`
	Author           Actor        `json:"author"`
	HeadRefName      string       `json:"headRefName"`
	HeadRefOID       string       `json:"headRefOid,omitempty"`
	HeadRepository   string       `json:"headRepository,omitempty"`
	BaseRefName      string       `json:"baseRefName"`
	Additions        int          `json:"additions"`
	Deletions        int          `json:"deletions"`
	ChangedFiles     int          `json:"changedFiles"`
	ReviewDecision   string       `json:"reviewDecision,omitempty"`
	Mergeable        string       `json:"mergeable,omitempty"`
	MergeStateStatus string       `json:"mergeStateStatus,omitempty"`
	URL              string       `json:"url"`
	UpdatedAt        time.Time    `json:"updatedAt"`
	Checks           CheckSummary `json:"checks"`
}

// Review is a submitted pull request review.
type Review struct {
	ID          string    `json:"id,omitempty"`
	Author      Actor     `json:"author"`
	State       string    `json:"state"`
	Body        string    `json:"body,omitempty"`
	SubmittedAt time.Time `json:"submittedAt,omitempty"`
	URL         string    `json:"url,omitempty"`
}

// Comment is a top-level pull request conversation comment.
type Comment struct {
	ID              string    `json:"id,omitempty"`
	Author          Actor     `json:"author"`
	Body            string    `json:"body"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt,omitempty"`
	URL             string    `json:"url,omitempty"`
	ViewerCanEdit   bool      `json:"viewerCanEdit,omitempty"`
	ViewerCanDelete bool      `json:"viewerCanDelete,omitempty"`
}

// Commit is a commit included in a pull request.
type Commit struct {
	OID         string    `json:"oid"`
	Headline    string    `json:"headline"`
	Body        string    `json:"body,omitempty"`
	AuthoredAt  time.Time `json:"authoredAt,omitempty"`
	CommittedAt time.Time `json:"committedAt,omitempty"`
	Authors     []Actor   `json:"authors,omitempty"`
}

// File is a changed file summary.
type File struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// Activity is a chronological item derived from GitHub PR metadata.
type Activity struct {
	Kind  string    `json:"kind"`
	Actor Actor     `json:"actor"`
	Title string    `json:"title"`
	Body  string    `json:"body,omitempty"`
	State string    `json:"state,omitempty"`
	URL   string    `json:"url,omitempty"`
	OID   string    `json:"oid,omitempty"`
	At    time.Time `json:"at"`
}

// PullRequest is the complete detail surface returned to the desktop.
type PullRequest struct {
	PullRequestSummary
	Body                string     `json:"body"`
	CreatedAt           time.Time  `json:"createdAt"`
	ClosedAt            time.Time  `json:"closedAt,omitempty"`
	MergedAt            time.Time  `json:"mergedAt,omitempty"`
	MaintainerCanModify bool       `json:"maintainerCanModify"`
	AutoMergeEnabled    bool       `json:"autoMergeEnabled"`
	AutoMergeMethod     string     `json:"autoMergeMethod,omitempty"`
	ReviewRequests      []Actor    `json:"reviewRequests"`
	Reviews             []Review   `json:"reviews"`
	Comments            []Comment  `json:"comments"`
	Commits             []Commit   `json:"commits"`
	Files               []File     `json:"files"`
	ChecksDetail        []Check    `json:"checksDetail"`
	Activity            []Activity `json:"activity"`
	AllowedMergeMethods []string   `json:"allowedMergeMethods"`
}

// Dashboard is the pull request index for the current workspace.
type Dashboard struct {
	Capability      Capability           `json:"capability"`
	Repository      *Repository          `json:"repository,omitempty"`
	CurrentBranch   string               `json:"currentBranch,omitempty"`
	Current         *PullRequestSummary  `json:"current,omitempty"`
	CreatedByViewer []PullRequestSummary `json:"createdByViewer"`
	NeedsReview     []PullRequestSummary `json:"needsReview"`
	Open            []PullRequestSummary `json:"open"`
	RefreshedAt     time.Time            `json:"refreshedAt"`
}

// MutationRequest is the closed set of GitHub mutations exposed to the WebView.
type MutationRequest struct {
	Number             int    `json:"number"`
	Kind               string `json:"kind"`
	Title              string `json:"title,omitempty"`
	Body               string `json:"body,omitempty"`
	Login              string `json:"login,omitempty"`
	ReviewKind         string `json:"reviewKind,omitempty"`
	MergeMethod        string `json:"mergeMethod,omitempty"`
	ExpectedHeadOID    string `json:"expectedHeadOid,omitempty"`
	ExpectedRepository string `json:"expectedRepository,omitempty"`
}

const (
	MutationEdit             = "edit"
	MutationAddReviewer      = "add_reviewer"
	MutationRemoveReviewer   = "remove_reviewer"
	MutationComment          = "comment"
	MutationReview           = "review"
	MutationReady            = "ready"
	MutationDraft            = "draft"
	MutationClose            = "close"
	MutationReopen           = "reopen"
	MutationMerge            = "merge"
	MutationEnableAutoMerge  = "enable_auto_merge"
	MutationDisableAutoMerge = "disable_auto_merge"
)

// MonitorState is the persisted and live state of one monitored PR.
type MonitorState struct {
	Number          int       `json:"number"`
	Enabled         bool      `json:"enabled"`
	Status          string    `json:"status"`
	Message         string    `json:"message,omitempty"`
	SessionID       string    `json:"sessionId,omitempty"`
	Conflict        bool      `json:"conflict,omitempty"`
	FailingChecks   []string  `json:"failingChecks,omitempty"`
	Fingerprint     string    `json:"fingerprint,omitempty"`
	LastCheckedAt   time.Time `json:"lastCheckedAt,omitempty"`
	LastTriggeredAt time.Time `json:"lastTriggeredAt,omitempty"`
}

const (
	MonitorDisabled  = "disabled"
	MonitorWatching  = "watching"
	MonitorPending   = "pending"
	MonitorRepairing = "repairing"
	MonitorCompleted = "completed"
	MonitorError     = "error"
)

// RepairIssue is the safe, minimal context passed to an automated repair session.
type RepairIssue struct {
	Conflict      bool
	FailingChecks []string
	Fingerprint   string
}
