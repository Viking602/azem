package securityscan

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	ContractVersion      = "1.0"
	WorkflowVersion      = "azem-security/v1"
	IdentityAlgorithm    = "codex-security/v1"
	SnapshotAlgorithm    = "codex-security-snapshot/v1"
	ProducerName         = "azem-security"
	ProducerVersion      = "1"
	ManifestDocumentType = "codex-security.scan-manifest"
	FindingsDocumentType = "codex-security.findings"
	CoverageDocumentType = "codex-security.coverage"
)

type Mode string

const (
	ModeStandard Mode = "standard"
	ModeDeep     Mode = "deep"
)

func (m Mode) Validate() error {
	if m != ModeStandard && m != ModeDeep {
		return fmt.Errorf("security scan: unsupported mode %q", m)
	}
	return nil
}

type TargetKind string

const (
	TargetRepository  TargetKind = "repository"
	TargetPaths       TargetKind = "paths"
	TargetGitRefs     TargetKind = "git_refs"
	TargetWorkingTree TargetKind = "working_tree"
)

func (k TargetKind) Validate() error {
	switch k {
	case TargetRepository, TargetPaths, TargetGitRefs, TargetWorkingTree:
		return nil
	default:
		return fmt.Errorf("security scan: unsupported target kind %q", k)
	}
}

type Status string

const (
	StatusQueued   Status = "queued"
	StatusRunning  Status = "running"
	StatusBlocked  Status = "blocked"
	StatusComplete Status = "complete"
	StatusFailed   Status = "failed"
	StatusCanceled Status = "canceled"
)

func (s Status) Terminal() bool {
	return s == StatusComplete || s == StatusFailed || s == StatusCanceled
}

type Phase string

const (
	PhasePreflight   Phase = "preflight"
	PhaseSnapshot    Phase = "snapshot"
	PhaseThreatModel Phase = "threat_model"
	PhaseDiscovery   Phase = "discovery"
	PhaseValidation  Phase = "validation"
	PhaseAttackPath  Phase = "attack_path"
	PhaseReducing    Phase = "reducing"
	PhaseReporting   Phase = "reporting"
	PhasePatching    Phase = "patching"
)

type Completeness string

const (
	CompletenessComplete Completeness = "complete"
	CompletenessPartial  Completeness = "partial"
	CompletenessUnknown  Completeness = "unknown"
)

type Route struct {
	Provider  string `json:"provider"`
	AccountID string `json:"accountId,omitempty"`
	Model     string `json:"model"`
	Reasoning string `json:"reasoning"`
}

func (r Route) Validate() error {
	if strings.TrimSpace(r.Provider) == "" || strings.TrimSpace(r.Model) == "" {
		return fmt.Errorf("security scan: provider and model are required")
	}
	return nil
}

type Budget struct {
	MaxTokens        int64   `json:"maxTokens,omitempty"`
	MaxToolCalls     int     `json:"maxToolCalls,omitempty"`
	MaxWallClockNS   int64   `json:"maxWallClockNs,omitempty"`
	MaxCostUSD       float64 `json:"maxCostUsd,omitempty"`
	MaxDiscoveryRuns int     `json:"maxDiscoveryRuns,omitempty"`
	MaxTimeHours     float64 `json:"maxTimeHours,omitempty"`
}

const (
	MaxDeepWorkers       = 32
	MaxDeepSubagents     = 32
	MaxDeepDiscoveryRuns = 1000
)

type DeepOptions struct {
	Workers                    int `json:"workers"`
	Subagents                  int `json:"subagents"`
	StopAfterNoNew             int `json:"stopAfterNoNew"`
	StopAfterConsecutiveErrors int `json:"stopAfterConsecutiveErrors"`
	MaxDiscoveryRuns           int `json:"maxDiscoveryRuns"`
}

func DefaultDeepOptions() DeepOptions {
	return DeepOptions{Workers: 4, Subagents: 3, StopAfterNoNew: 4, StopAfterConsecutiveErrors: 3, MaxDiscoveryRuns: 40}
}

func (o DeepOptions) Validate() error {
	if o.Workers < 1 || o.Workers > MaxDeepWorkers ||
		o.Subagents < 0 || o.Subagents > MaxDeepSubagents ||
		o.StopAfterNoNew < 1 || o.StopAfterNoNew > MaxDeepDiscoveryRuns ||
		o.StopAfterConsecutiveErrors < 1 || o.StopAfterConsecutiveErrors > MaxDeepDiscoveryRuns ||
		o.MaxDiscoveryRuns < 1 || o.MaxDiscoveryRuns > MaxDeepDiscoveryRuns {
		return fmt.Errorf("security scan: invalid deep scan limits")
	}
	return nil
}

type Target struct {
	Kind           TargetKind `json:"kind"`
	Repository     string     `json:"repository"`
	TargetID       string     `json:"targetId"`
	DisplayName    string     `json:"displayName"`
	Remote         string     `json:"remote,omitempty"`
	Revision       string     `json:"revision,omitempty"`
	BaseRevision   string     `json:"baseRevision,omitempty"`
	HeadRevision   string     `json:"headRevision,omitempty"`
	SnapshotDigest string     `json:"snapshotDigest"`
	SnapshotRoot   string     `json:"-"`
	IncludePaths   []string   `json:"includePaths"`
	ExcludePaths   []string   `json:"excludePaths"`
	Inventory      []string   `json:"inventory,omitempty"`
	SnapshotPaths  []string   `json:"snapshotPaths,omitempty"`
	ScopePaths     []string   `json:"scopePaths,omitempty"`
	DiffArtifact   string     `json:"diffArtifact,omitempty"`
	DiffDigest     string     `json:"diffDigest,omitempty"`
}

func (t Target) Validate() error {
	if err := t.Kind.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(t.Repository) == "" || strings.TrimSpace(t.TargetID) == "" || strings.TrimSpace(t.SnapshotDigest) == "" {
		return fmt.Errorf("security scan: target repository, ID, and snapshot digest are required")
	}
	if t.Kind == TargetPaths && len(t.IncludePaths) == 0 {
		return fmt.Errorf("security scan: path target requires include paths")
	}
	if t.Kind == TargetGitRefs && (t.BaseRevision == "" || t.HeadRevision == "") {
		return fmt.Errorf("security scan: committed diff requires base and head revisions")
	}
	if t.Kind == TargetGitRefs || t.Kind == TargetWorkingTree {
		if len(t.ScopePaths) == 0 || t.DiffArtifact == "" || t.DiffDigest == "" {
			return fmt.Errorf("security scan: diff target requires immutable scope and diff evidence")
		}
	}
	return nil
}

type StartRequest struct {
	ProjectID     string      `json:"projectId"`
	SessionID     string      `json:"sessionId,omitempty"`
	Repository    string      `json:"repository"`
	TargetKind    TargetKind  `json:"targetKind"`
	Paths         []string    `json:"paths,omitempty"`
	Base          string      `json:"base,omitempty"`
	Head          string      `json:"head,omitempty"`
	Mode          Mode        `json:"mode"`
	Route         Route       `json:"route"`
	ReducerRoute  Route       `json:"reducerRoute,omitempty"`
	FixerRoute    Route       `json:"fixerRoute,omitempty"`
	VerifierRoute Route       `json:"verifierRoute,omitempty"`
	Budget        Budget      `json:"budget"`
	Deep          DeepOptions `json:"deep"`
	UserContext   string      `json:"userContext,omitempty"`
	Knowledge     []string    `json:"knowledge,omitempty"`
	ParentScanID  string      `json:"parentScanId,omitempty"`
}

func (r StartRequest) Validate() error {
	if strings.TrimSpace(r.Repository) == "" {
		return fmt.Errorf("security scan: repository is required")
	}
	if err := r.TargetKind.Validate(); err != nil {
		return err
	}
	if err := r.Mode.Validate(); err != nil {
		return err
	}
	if err := r.Route.Validate(); err != nil {
		return err
	}
	if r.Mode == ModeDeep {
		if r.TargetKind == TargetGitRefs || r.TargetKind == TargetWorkingTree {
			return fmt.Errorf("security scan: deep mode supports repository and path targets only")
		}
		if err := r.Deep.Validate(); err != nil {
			return err
		}
	}
	executionUnits := 1 + r.Deep.Subagents
	if r.Mode == ModeDeep {
		executionUnits *= (1 + r.Deep.StopAfterConsecutiveErrors) * r.Deep.MaxDiscoveryRuns
	}
	if (r.Budget.MaxTokens > 0 && r.Budget.MaxTokens < int64(executionUnits)) ||
		(r.Budget.MaxToolCalls > 0 && r.Budget.MaxToolCalls < executionUnits) {
		return fmt.Errorf("security scan: token and tool budgets must cover every bounded execution unit")
	}
	for name, route := range map[string]Route{"reducer": r.ReducerRoute, "fixer": r.FixerRoute, "verifier": r.VerifierRoute} {
		if route.Provider == "" && route.Model == "" {
			continue
		}
		if err := route.Validate(); err != nil {
			return fmt.Errorf("security scan: %s route: %w", name, err)
		}
	}
	if r.Budget.MaxCostUSD < 0 || r.Budget.MaxTimeHours < 0 || r.Budget.MaxTimeHours > 96 {
		return fmt.Errorf("security scan: invalid cost or time budget")
	}
	if r.Budget.MaxCostUSD > 0 {
		return fmt.Errorf("security scan: USD cost limits require trusted provider pricing; use a token or time budget for this route")
	}
	return nil
}

type Scan struct {
	ID                   string       `json:"id"`
	ProjectID            string       `json:"projectId"`
	RequestedBySessionID string       `json:"requestedBySessionId,omitempty"`
	RootRunID            string       `json:"rootRunId,omitempty"`
	ParentScanID         string       `json:"parentScanId,omitempty"`
	Mode                 Mode         `json:"mode"`
	Status               Status       `json:"status"`
	Phase                Phase        `json:"phase"`
	Completeness         Completeness `json:"completeness,omitempty"`
	Target               Target       `json:"target"`
	Route                Route        `json:"route"`
	ReducerRoute         Route        `json:"reducerRoute,omitempty"`
	FixerRoute           Route        `json:"fixerRoute,omitempty"`
	VerifierRoute        Route        `json:"verifierRoute,omitempty"`
	Budget               Budget       `json:"budget"`
	Deep                 DeepOptions  `json:"deep"`
	UserContext          string       `json:"userContext,omitempty"`
	Knowledge            []string     `json:"knowledge,omitempty"`
	WorkflowVersion      string       `json:"workflowVersion"`
	ContractVersion      string       `json:"contractVersion"`
	OutputDirectory      string       `json:"outputDirectory"`
	FailureMessage       string       `json:"failureMessage,omitempty"`
	BlockingReason       string       `json:"blockingReason,omitempty"`
	Warning              string       `json:"warning,omitempty"`
	InputTokens          int64        `json:"inputTokens,omitempty"`
	CachedInputTokens    int64        `json:"cachedInputTokens,omitempty"`
	OutputTokens         int64        `json:"outputTokens,omitempty"`
	EstimatedCostUSD     float64      `json:"estimatedCostUsd,omitempty"`
	CreatedAt            time.Time    `json:"createdAt"`
	StartedAt            time.Time    `json:"startedAt,omitempty"`
	CompletedAt          time.Time    `json:"completedAt,omitempty"`
	UpdatedAt            time.Time    `json:"updatedAt"`
}

type WorkerKind string

const (
	WorkerAudit    WorkerKind = "audit"
	WorkerReducer  WorkerKind = "reducer"
	WorkerMatcher  WorkerKind = "matcher"
	WorkerFixer    WorkerKind = "fixer"
	WorkerVerifier WorkerKind = "verifier"
)

type WorkerStatus string

const (
	WorkerQueued    WorkerStatus = "queued"
	WorkerRunning   WorkerStatus = "running"
	WorkerSucceeded WorkerStatus = "succeeded"
	WorkerFailed    WorkerStatus = "failed"
	WorkerCanceled  WorkerStatus = "canceled"
)

type Worker struct {
	ID                 string       `json:"id"`
	ScanID             string       `json:"scanId"`
	RunID              string       `json:"runId,omitempty"`
	Kind               WorkerKind   `json:"kind"`
	Status             WorkerStatus `json:"status"`
	Sequence           int          `json:"sequence"`
	Attempt            int          `json:"attempt"`
	CompletionSequence int          `json:"completionSequence,omitempty"`
	Route              Route        `json:"route"`
	ResultPath         string       `json:"resultPath,omitempty"`
	Error              string       `json:"error,omitempty"`
	StartedAt          time.Time    `json:"startedAt,omitempty"`
	CompletedAt        time.Time    `json:"completedAt,omitempty"`
	UpdatedAt          time.Time    `json:"updatedAt"`
}

type Progress struct {
	ScanID         string    `json:"scanId"`
	Phase          Phase     `json:"phase"`
	FilesCompleted int       `json:"filesCompleted"`
	FilesTotal     int       `json:"filesTotal"`
	ReviewedPaths  []string  `json:"reviewedPaths,omitempty"`
	WorkersPlanned int       `json:"workersPlanned,omitempty"`
	WorkersRunning int       `json:"workersRunning,omitempty"`
	WorkersDone    int       `json:"workersDone,omitempty"`
	Message        string    `json:"message,omitempty"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type FindingIdentity struct {
	Anchor   string `json:"anchor"`
	Instance string `json:"instance,omitempty"`
}

type FindingFingerprints struct {
	Algorithm string `json:"algorithm"`
	Primary   string `json:"primary"`
}

type FindingLocation struct {
	Path      string `json:"path"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine,omitempty"`
	Role      string `json:"role"`
}

type Severity struct {
	Level            string  `json:"level"`
	Score            float64 `json:"score,omitempty"`
	ScoringSystem    string  `json:"scoringSystem,omitempty"`
	Rationale        string  `json:"rationale,omitempty"`
	ChangeConditions string  `json:"changeConditions,omitempty"`
}

type Confidence struct {
	Level     string `json:"level"`
	Rationale string `json:"rationale"`
}

type Taxonomy struct {
	Category string   `json:"category"`
	CWE      []string `json:"cwe"`
}

type Finding struct {
	FindingID        string              `json:"findingId"`
	OccurrenceID     string              `json:"occurrenceId"`
	RuleID           string              `json:"ruleId"`
	Identity         FindingIdentity     `json:"identity"`
	Fingerprints     FindingFingerprints `json:"fingerprints"`
	Title            string              `json:"title"`
	Summary          string              `json:"summary"`
	Severity         Severity            `json:"severity"`
	Confidence       Confidence          `json:"confidence"`
	Taxonomy         Taxonomy            `json:"taxonomy"`
	Locations        []FindingLocation   `json:"locations"`
	Remediation      string              `json:"remediation"`
	RootCause        any                 `json:"rootCause,omitempty"`
	Validation       any                 `json:"validation"`
	AttackPath       any                 `json:"attackPath"`
	RemediationTests []string            `json:"remediationTests"`
	Preventive       []string            `json:"preventiveControls"`
	Evidence         []map[string]any    `json:"evidence,omitempty"`
	Provenance       map[string]any      `json:"provenance"`
	Extensions       map[string]any      `json:"extensions"`
}

func (f Finding) ValidateDraft() error {
	if strings.TrimSpace(f.RuleID) == "" || strings.TrimSpace(f.Identity.Anchor) == "" || strings.TrimSpace(f.Title) == "" || strings.TrimSpace(f.Summary) == "" || strings.TrimSpace(f.Remediation) == "" {
		return fmt.Errorf("security scan: finding rule, identity, title, summary, and remediation are required")
	}
	if len(f.Locations) == 0 {
		return fmt.Errorf("security scan: finding %q requires a source location", f.Title)
	}
	if f.Severity.Level == "" || f.Confidence.Level == "" || f.Confidence.Rationale == "" || f.Taxonomy.Category == "" {
		return fmt.Errorf("security scan: finding %q has incomplete classification", f.Title)
	}
	return nil
}

type CoverageSurface struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Disposition string   `json:"disposition"`
	ReceiptRefs []string `json:"receiptRefs"`
}

type CoverageExclusion struct {
	Pattern string `json:"pattern"`
	Reason  string `json:"reason"`
}

type DeferredWork struct {
	ID         string   `json:"id"`
	Reason     string   `json:"reason"`
	Paths      []string `json:"paths,omitempty"`
	SurfaceIDs []string `json:"surfaceIds,omitempty"`
}

type Coverage struct {
	Mode               string              `json:"mode"`
	Completeness       Completeness        `json:"completeness"`
	InventoryStrategy  string              `json:"inventoryStrategy"`
	IncludePaths       []string            `json:"includePaths"`
	ExcludePaths       []string            `json:"excludePaths"`
	Surfaces           []CoverageSurface   `json:"surfaces"`
	ExplicitExclusions []CoverageExclusion `json:"explicitExclusions"`
	Deferred           []DeferredWork      `json:"deferred"`
	OpenQuestions      []map[string]any    `json:"openQuestions,omitempty"`
}

type Draft struct {
	ScanID      string          `json:"scanId"`
	Scope       json.RawMessage `json:"scope,omitempty"`
	ThreatModel json.RawMessage `json:"threatModel,omitempty"`
	Findings    []Finding       `json:"findings"`
	Coverage    Coverage        `json:"coverage"`
}

func (d Draft) Validate() error {
	if strings.TrimSpace(d.ScanID) == "" {
		return fmt.Errorf("security scan: draft scan ID is required")
	}
	if d.Coverage.Completeness != CompletenessComplete && d.Coverage.Completeness != CompletenessPartial && d.Coverage.Completeness != CompletenessUnknown {
		return fmt.Errorf("security scan: invalid coverage completeness %q", d.Coverage.Completeness)
	}
	for index, finding := range d.Findings {
		if err := finding.ValidateDraft(); err != nil {
			return fmt.Errorf("finding %d: %w", index, err)
		}
	}
	return nil
}

type Artifact struct {
	ScanID    string    `json:"scanId"`
	Kind      string    `json:"kind"`
	Path      string    `json:"path"`
	MediaType string    `json:"mediaType"`
	SHA256    string    `json:"sha256"`
	Bytes     int64     `json:"bytes"`
	CreatedAt time.Time `json:"createdAt"`
}

type Triage struct {
	OccurrenceID string    `json:"occurrenceId"`
	Status       string    `json:"status"`
	CloseReason  string    `json:"closeReason,omitempty"`
	Note         string    `json:"note,omitempty"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type PatchResult struct {
	OccurrenceID string   `json:"occurrenceId"`
	Status       string   `json:"status"`
	Files        []string `json:"files"`
	Verification string   `json:"verification,omitempty"`
	Reason       string   `json:"reason,omitempty"`
	Branch       string   `json:"branch,omitempty"`
	Commit       string   `json:"commit,omitempty"`
}
type RemediationAttempt struct {
	ID                    string    `json:"id"`
	OccurrenceID          string    `json:"occurrenceId"`
	State                 string    `json:"state"`
	Version               int       `json:"version"`
	BaseRevision          string    `json:"baseRevision"`
	BaseSnapshotDigest    string    `json:"baseSnapshotDigest"`
	AppliedSnapshotDigest string    `json:"appliedSnapshotDigest,omitempty"`
	Files                 []string  `json:"files"`
	Verification          string    `json:"verification,omitempty"`
	Reason                string    `json:"reason,omitempty"`
	Branch                string    `json:"branch,omitempty"`
	Commit                string    `json:"commit,omitempty"`
	CreatedAt             time.Time `json:"createdAt"`
	UpdatedAt             time.Time `json:"updatedAt"`
}

type Publication struct {
	ScanID       string    `json:"scanId"`
	OccurrenceID string    `json:"occurrenceId"`
	Destination  string    `json:"destination"`
	Status       string    `json:"status"`
	ExternalID   string    `json:"externalId,omitempty"`
	ExternalURL  string    `json:"externalUrl,omitempty"`
	Error        string    `json:"error,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type PublicationIssue struct {
	OccurrenceID string `json:"occurrenceId"`
	Title        string `json:"title"`
	Description  string `json:"description"`
}

type Projection struct {
	Scan      Scan              `json:"scan"`
	Progress  Progress          `json:"progress"`
	Workers   []Worker          `json:"workers,omitempty"`
	Findings  []Finding         `json:"findings,omitempty"`
	Artifacts []Artifact        `json:"artifacts,omitempty"`
	Triage    map[string]Triage `json:"triage,omitempty"`
}
