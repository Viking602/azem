package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/githubpr"
	"github.com/Viking602/azem/internal/hooks"
	"github.com/Viking602/azem/internal/securityscan"
	"github.com/Viking602/venat/tool"
)

const maxDesktopSecurityConfigPayloadBytes = 16 << 10

type desktopSecurityConfigInput struct {
	Enabled                    bool    `json:"enabled"`
	DefaultMode                string  `json:"defaultMode"`
	Workers                    int     `json:"workers"`
	Subagents                  int     `json:"subagents"`
	StopAfterNoNew             int     `json:"stopAfterNoNew"`
	StopAfterConsecutiveErrors int     `json:"stopAfterConsecutiveErrors"`
	MaxDiscoveryRuns           int     `json:"maxDiscoveryRuns"`
	MaxTimeHours               float64 `json:"maxTimeHours"`
}

func (input desktopSecurityConfigInput) securityConfig() config.SecurityConfig {
	return config.SecurityConfig{
		Enabled: input.Enabled, DefaultMode: input.DefaultMode,
		Workers: input.Workers, Subagents: input.Subagents,
		StopAfterNoNew: input.StopAfterNoNew, StopAfterConsecutiveErrors: input.StopAfterConsecutiveErrors,
		MaxDiscoveryRuns: input.MaxDiscoveryRuns, MaxTimeHours: input.MaxTimeHours,
		MaxCostUSD: 0,
	}
}

var securityActionHandlers = map[ActionKind]actionHandler{
	ActionGetSecurityConfig: func(s *Service, ctx context.Context, action Action) error {
		s.emit(ctx, s.securityConfigEvent("loaded"))
		return nil
	},
	ActionSetSecurityConfig: func(s *Service, ctx context.Context, action Action) error {
		if len(action.Payload) == 0 || len(action.Payload) > maxDesktopSecurityConfigPayloadBytes {
			return fmt.Errorf("security configuration payload must be between 1 byte and 16 KiB")
		}
		var input desktopSecurityConfigInput
		decoder := json.NewDecoder(bytes.NewReader(action.Payload))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			return fmt.Errorf("decode security configuration: %w", err)
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return fmt.Errorf("decode security configuration: trailing JSON value")
		}
		return s.updateSecurityConfig(ctx, input.securityConfig())
	},
	ActionStartSecurityScan: func(s *Service, ctx context.Context, action Action) error {
		if s.security == nil {
			return fmt.Errorf("security scanning is unavailable")
		}
		if !s.cfg.Security.Enabled {
			return fmt.Errorf("security scanning is disabled in configuration")
		}
		var request securityscan.StartRequest
		if len(action.Payload) > 0 {
			if err := json.Unmarshal(action.Payload, &request); err != nil {
				return fmt.Errorf("decode security scan request: %w", err)
			}
		}
		request.Repository = s.cfg.Workspace.Root
		request.ProjectID = s.cfg.Workspace.Root
		request.SessionID = action.SessionID
		request.ParentScanID = ""
		if request.TargetKind == "" {
			request.TargetKind = securityscan.TargetRepository
		}
		if request.Mode == "" {
			request.Mode = securityscan.Mode(s.cfg.Security.DefaultMode)
		}
		request.Route = configuredSecurityRoute(s, s.cfg.Security.Routes.Audit, securityscan.Route{})
		request.ReducerRoute = configuredSecurityRoute(s, s.cfg.Security.Routes.Reducer, request.Route)
		request.FixerRoute = configuredSecurityRoute(s, s.cfg.Security.Routes.Fixer, request.Route)
		request.VerifierRoute = configuredSecurityRoute(s, s.cfg.Security.Routes.Verifier, request.FixerRoute)
		request.Deep = securityscan.DeepOptions{
			Workers: s.cfg.Security.Workers, Subagents: s.cfg.Security.Subagents,
			StopAfterNoNew: s.cfg.Security.StopAfterNoNew, StopAfterConsecutiveErrors: s.cfg.Security.StopAfterConsecutiveErrors,
			MaxDiscoveryRuns: s.cfg.Security.MaxDiscoveryRuns,
		}
		request.Budget = configuredSecurityBudget(s.cfg.Security.MaxTimeHours)
		scan, err := s.security.Start(ctx, request)
		if err != nil {
			return err
		}
		projection, err := s.security.Scan(ctx, scan.ID)
		if err == nil {
			s.emit(ctx, Event{Kind: EventSecurityScanState, SessionID: action.SessionID, State: string(scan.Status), Security: &projection})
		}
		return nil
	},
	ActionCancelSecurityScan: func(s *Service, ctx context.Context, action Action) error {
		if s.security == nil {
			return fmt.Errorf("security scanning is unavailable")
		}
		if _, err := authorizedSecurityScan(s, ctx, action.Target); err != nil {
			return err
		}
		return s.security.Cancel(ctx, strings.TrimSpace(action.Target))
	},
	ActionResumeSecurityScan: func(s *Service, ctx context.Context, action Action) error {
		if s.security == nil {
			return fmt.Errorf("security scanning is unavailable")
		}
		if _, err := authorizedSecurityScan(s, ctx, action.Target); err != nil {
			return err
		}
		return s.security.Resume(ctx, strings.TrimSpace(action.Target))
	},
	ActionListSecurityScans: func(s *Service, ctx context.Context, action Action) error {
		if s.security == nil {
			return fmt.Errorf("security scanning is unavailable")
		}
		scans, err := s.security.List(ctx, s.cfg.Workspace.Root, action.Limit)
		if err != nil {
			return err
		}
		s.emit(ctx, Event{Kind: EventSecurityScanList, SessionID: action.SessionID, State: "listed", SecurityScans: scans})
		return nil
	},
	ActionGetSecurityScan: func(s *Service, ctx context.Context, action Action) error {
		if s.security == nil {
			return fmt.Errorf("security scanning is unavailable")
		}
		projection, err := authorizedSecurityScan(s, ctx, action.Target)
		if err != nil {
			return err
		}
		s.emit(ctx, Event{Kind: EventSecurityScanState, SessionID: action.SessionID, State: string(projection.Scan.Status), Security: &projection})
		return nil
	},
	ActionListSecurityFindings: func(s *Service, ctx context.Context, action Action) error {
		if s.security == nil {
			return fmt.Errorf("security scanning is unavailable")
		}
		if _, err := authorizedSecurityScan(s, ctx, action.Target); err != nil {
			return err
		}
		findings, err := s.security.Findings(ctx, strings.TrimSpace(action.Target))
		if err != nil {
			return err
		}
		s.emit(ctx, Event{Kind: EventSecurityFindings, SessionID: action.SessionID, State: "listed", SecurityFindings: findings, Data: map[string]string{"scanId": strings.TrimSpace(action.Target)}})
		return nil
	},
	ActionGetSecurityFinding: func(s *Service, ctx context.Context, action Action) error {
		if s.security == nil {
			return fmt.Errorf("security scanning is unavailable")
		}
		if _, err := authorizedSecurityOccurrence(s, ctx, action.Target); err != nil {
			return err
		}
		finding, err := s.security.Finding(ctx, strings.TrimSpace(action.Target))
		if err != nil {
			return err
		}
		s.emit(ctx, Event{Kind: EventSecurityFinding, SessionID: action.SessionID, State: "loaded", SecurityFinding: &finding})
		return nil
	},
	ActionSetSecurityTriage: func(s *Service, ctx context.Context, action Action) error {
		if s.security == nil {
			return fmt.Errorf("security scanning is unavailable")
		}
		var triage securityscan.Triage
		if err := json.Unmarshal(action.Payload, &triage); err != nil {
			return fmt.Errorf("decode security finding triage: %w", err)
		}
		scan, err := authorizedSecurityOccurrence(s, ctx, triage.OccurrenceID)
		if err != nil {
			return err
		}
		if err := s.security.SaveTriage(ctx, triage); err != nil {
			return err
		}
		projection, err := s.security.Scan(ctx, scan.ID)
		if err != nil {
			return err
		}
		s.emit(ctx, Event{Kind: EventSecurityScanState, SessionID: action.SessionID, State: "triaged", Security: &projection})
		return nil
	},
	ActionPatchSecurityFindings: handlePatchSecurityFindings,
	ActionPatchSecurityWithPR:   handlePatchSecurityFindings,
	ActionExportSecurityScan: func(s *Service, ctx context.Context, action Action) error {
		if s.security == nil {
			return fmt.Errorf("security scanning is unavailable")
		}
		if _, err := authorizedSecurityScan(s, ctx, action.Target); err != nil {
			return err
		}
		format := strings.TrimSpace(action.Decision)
		if format == "" {
			format = "json"
		}
		path, err := s.security.Export(ctx, strings.TrimSpace(action.Target), format)
		if err != nil {
			return err
		}
		projection, projectionErr := s.security.Scan(ctx, strings.TrimSpace(action.Target))
		if projectionErr != nil {
			return projectionErr
		}
		s.emit(ctx, Event{Kind: EventSecurityScanState, SessionID: action.SessionID, State: "exported", Security: &projection, Data: map[string]string{"scanId": action.Target, "format": format, "path": path}})
		return nil
	},
	ActionPublishSecurityScan: func(s *Service, ctx context.Context, action Action) error {
		if s.security == nil || s.mcp == nil {
			return fmt.Errorf("security publication is unavailable")
		}
		var request struct {
			ScanID           string         `json:"scanId"`
			ToolName         string         `json:"toolName"`
			Destination      string         `json:"destination"`
			BaseArguments    map[string]any `json:"baseArguments"`
			TitleField       string         `json:"titleField"`
			DescriptionField string         `json:"descriptionField"`
		}
		if err := json.Unmarshal(action.Payload, &request); err != nil {
			return fmt.Errorf("decode security publication request: %w", err)
		}
		if request.ScanID == "" {
			request.ScanID = action.Target
		}
		if _, err := authorizedSecurityScan(s, ctx, request.ScanID); err != nil {
			return err
		}
		configuredTool := strings.TrimSpace(s.cfg.Security.PublicationTool)
		if configuredTool == "" {
			return fmt.Errorf("security publication requires security.publication_tool")
		}
		request.ToolName = configuredTool
		request.Destination = strings.TrimSpace(s.cfg.Security.PublicationDestination)
		if request.Destination == "" {
			request.Destination = configuredTool
		}
		request.BaseArguments = make(map[string]any, len(s.cfg.Security.PublicationArguments))
		for key, value := range s.cfg.Security.PublicationArguments {
			request.BaseArguments[key] = value
		}
		request.TitleField = strings.TrimSpace(s.cfg.Security.PublicationTitleField)
		if request.TitleField == "" {
			request.TitleField = "title"
		}
		request.DescriptionField = strings.TrimSpace(s.cfg.Security.PublicationDescriptionField)
		if request.DescriptionField == "" {
			request.DescriptionField = "description"
		}
		var driver tool.Driver
		for _, candidate := range s.mcp.Snapshot() {
			if candidate.Definition().Name == request.ToolName {
				driver = candidate
				break
			}
		}
		if driver == nil {
			return fmt.Errorf("configured publication tool %q is unavailable", request.ToolName)
		}
		driver = hooks.WrapDriver(s.HookDispatcher(), hooks.Metadata{SessionID: action.SessionID, CWD: s.cfg.Workspace.Root}, driver)
		issues, err := s.security.PublicationIssues(ctx, request.ScanID, request.Destination)
		if err != nil {
			return err
		}
		for _, issue := range issues {
			publication := securityscan.Publication{ScanID: request.ScanID, OccurrenceID: issue.OccurrenceID, Destination: request.Destination, Status: "publishing"}
			if err := s.security.ClaimPublication(ctx, publication); errors.Is(err, securityscan.ErrPublicationExists) {
				return fmt.Errorf("security finding %s already has a publication claim; reconcile it before retrying", issue.OccurrenceID)
			} else if err != nil {
				return err
			}
			arguments := make(map[string]any, len(request.BaseArguments)+2)
			for key, value := range request.BaseArguments {
				arguments[key] = value
			}
			arguments[request.TitleField] = issue.Title
			arguments[request.DescriptionField] = issue.Description
			encoded, err := json.Marshal(arguments)
			if err != nil {
				return err
			}
			callID, err := randomID("security-publication")
			if err != nil {
				return err
			}
			result, executeErr := executeSecurityPublicationDriver(s, ctx, action.SessionID, driver, tool.Call{ID: callID, Name: request.ToolName, Arguments: encoded})
			if executeErr != nil || result.IsError {
				message := "configured publication tool failed"
				publication.Status, publication.Error = "failed", message
				_ = s.security.SavePublication(context.WithoutCancel(ctx), publication)
				return fmt.Errorf("publish security finding %s: %s", issue.OccurrenceID, message)
			}
			publication.Status = "published"
			publication.ExternalID, publication.ExternalURL = publicationResultFields(result.Structured, result.Content)
			if err := s.security.SavePublication(context.WithoutCancel(ctx), publication); err != nil {
				return err
			}
			s.emit(ctx, Event{Kind: EventSecurityPublish, SessionID: action.SessionID, State: "published", Data: map[string]string{
				"scanId": request.ScanID, "occurrenceId": issue.OccurrenceID, "externalId": publication.ExternalID, "externalUrl": publication.ExternalURL,
			}})
		}
		return nil
	},
	ActionReconcileSecurityPublish: func(s *Service, ctx context.Context, action Action) error {
		if s.security == nil {
			return fmt.Errorf("security publication is unavailable")
		}
		var request struct {
			ScanID       string `json:"scanId"`
			OccurrenceID string `json:"occurrenceId"`
			Decision     string `json:"decision"`
		}
		if err := json.Unmarshal(action.Payload, &request); err != nil {
			return fmt.Errorf("decode security publication reconciliation: %w", err)
		}
		if request.ScanID == "" {
			request.ScanID = action.Target
		}
		scan, err := authorizedSecurityOccurrence(s, ctx, request.OccurrenceID)
		if err != nil {
			return err
		}
		if scan.ID != request.ScanID {
			return fmt.Errorf("security publication occurrence does not belong to the requested scan")
		}
		destination := strings.TrimSpace(s.cfg.Security.PublicationDestination)
		if destination == "" {
			destination = strings.TrimSpace(s.cfg.Security.PublicationTool)
		}
		if err := s.security.ReconcilePublication(ctx, request.ScanID, request.OccurrenceID, destination, request.Decision); err != nil {
			return err
		}
		s.emit(ctx, Event{Kind: EventSecurityPublish, SessionID: action.SessionID, State: "reconciled_" + request.Decision, Data: map[string]string{
			"scanId": request.ScanID, "occurrenceId": request.OccurrenceID,
		}})
		return nil
	},
}

func configuredSecurityBudget(maxTimeHours float64) securityscan.Budget {
	return securityscan.Budget{MaxTimeHours: maxTimeHours}
}

func (s *Service) securityConfigEvent(state string) Event {
	s.mu.Lock()
	security := s.cfg.Security
	security.PublicationArguments = nil
	s.mu.Unlock()
	return Event{Kind: EventSecurityConfig, State: state, SecurityConfig: &security}
}

func (s *Service) updateSecurityConfig(ctx context.Context, requested config.SecurityConfig) error {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	s.mu.Lock()
	currentSecurity := s.cfg.Security
	requested.Routes = currentSecurity.Routes
	requested.PublicationTool = currentSecurity.PublicationTool
	requested.PublicationDestination = currentSecurity.PublicationDestination
	requested.PublicationArguments = currentSecurity.PublicationArguments
	requested.PublicationTitleField = currentSecurity.PublicationTitleField
	requested.PublicationDescriptionField = currentSecurity.PublicationDescriptionField
	currentSession := s.currentSession
	workspace := s.cfg.Workspace.Root
	s.mu.Unlock()
	if err := requested.Validate(); err != nil {
		return err
	}
	if err := s.dispatchLifecycle(ctx, hooks.ConfigChange, s.hookMetadata(currentSession, ""), func(e *hooks.Envelope) {
		e.Source, e.FilePath = "user_settings", s.configPath
	}); err != nil {
		return err
	}
	if s.configPath != "" {
		if err := s.ensureHookWatcher().writeConfig(s.configPath, func() error {
			return config.UpdateDesktopSecurityConfig(s.configPath, requested)
		}); err != nil {
			return err
		}
		loaded, err := config.LoadAtWorkspace(s.configPath, workspace)
		if err != nil {
			return fmt.Errorf("reload security configuration: %w", err)
		}
		requested = loaded.Security
	}
	if s.providers != nil {
		for name, route := range map[string]config.ModelRouteConfig{
			"audit": requested.Routes.Audit, "reducer": requested.Routes.Reducer,
			"fixer": requested.Routes.Fixer, "verifier": requested.Routes.Verifier,
		} {
			if route == (config.ModelRouteConfig{}) {
				continue
			}
			if _, _, _, _, err := s.providers.resolveDriver(ctx, route.Provider, route.Model, route.Reasoning); err != nil {
				return fmt.Errorf("security %s route: %w", name, err)
			}
		}
	}
	s.mu.Lock()
	s.cfg.Security = requested
	s.mu.Unlock()
	if s.providers != nil {
		s.providers.UpdateModelRoute("security", "audit", requested.Routes.Audit)
		s.providers.UpdateModelRoute("security", "reducer", requested.Routes.Reducer)
		s.providers.UpdateModelRoute("security", "fixer", requested.Routes.Fixer)
		s.providers.UpdateModelRoute("security", "verifier", requested.Routes.Verifier)
	}
	s.emit(ctx, s.securityConfigEvent("updated"))
	s.emit(ctx, s.modelRoutesEvent("updated"))
	return nil
}

func handlePatchSecurityFindings(s *Service, ctx context.Context, action Action) error {
	if s.security == nil {
		return fmt.Errorf("security scanning is unavailable")
	}
	var request securityscan.PatchRequest
	if err := json.Unmarshal(action.Payload, &request); err != nil {
		return fmt.Errorf("decode security patch request: %w", err)
	}
	request.CreatePR = action.Kind == ActionPatchSecurityWithPR
	if len(request.OccurrenceIDs) == 0 {
		return fmt.Errorf("security scan: at least one finding is required")
	}
	patchScan, err := authorizedSecurityOccurrence(s, ctx, request.OccurrenceIDs[0])
	if err != nil {
		return err
	}
	for _, occurrenceID := range request.OccurrenceIDs[1:] {
		scan, authorizeErr := authorizedSecurityOccurrence(s, ctx, occurrenceID)
		if authorizeErr != nil {
			return authorizeErr
		}
		if scan.ID != patchScan.ID {
			return fmt.Errorf("security scan: one patch batch cannot mix scans")
		}
	}
	request.Route = configuredSecurityRoute(s, s.cfg.Security.Routes.Fixer, patchScan.FixerRoute)
	request.VerifierRoute = configuredSecurityRoute(s, s.cfg.Security.Routes.Verifier, patchScan.VerifierRoute)
	results, err := s.security.Patch(ctx, request)
	if err != nil {
		return err
	}
	pullRequestURL := ""
	if request.CreatePR {
		for _, result := range results {
			if result.Status != "verified" || result.Branch == "" || result.Commit == "" {
				continue
			}
			created, createErr := githubpr.NewClient(patchScan.Target.Repository).Create(ctx, githubpr.CreateRequest{
				Branch: result.Branch, ExpectedCommit: result.Commit,
				Title: "fix: patch verified security findings",
				Body:  "Applies verified security fixes from a completed Azem Security scan.",
			})
			if createErr != nil {
				return createErr
			}
			pullRequestURL = created.URL
			break
		}
	}
	for index := range results {
		result := results[index]
		data := map[string]string{}
		if pullRequestURL != "" {
			data["pullRequestUrl"] = pullRequestURL
		}
		s.emit(ctx, Event{Kind: EventSecurityPatch, SessionID: action.SessionID, State: result.Status, SecurityPatch: &result, Data: data})
	}
	return nil
}

func authorizedSecurityScan(s *Service, ctx context.Context, scanID string) (securityscan.Projection, error) {
	if s.security == nil {
		return securityscan.Projection{}, fmt.Errorf("security scanning is unavailable")
	}
	projection, err := s.security.Scan(ctx, strings.TrimSpace(scanID))
	if err != nil {
		return securityscan.Projection{}, err
	}
	if !sameSecurityWorkspace(projection.Scan, s.cfg.Workspace.Root) {
		return securityscan.Projection{}, fmt.Errorf("security scan does not belong to the active workspace")
	}
	return projection, nil
}

func authorizedSecurityOccurrence(s *Service, ctx context.Context, occurrenceID string) (securityscan.Scan, error) {
	if s.security == nil {
		return securityscan.Scan{}, fmt.Errorf("security scanning is unavailable")
	}
	scan, err := s.security.ScanForOccurrence(ctx, strings.TrimSpace(occurrenceID))
	if err != nil {
		return securityscan.Scan{}, err
	}
	if !sameSecurityWorkspace(scan, s.cfg.Workspace.Root) {
		return securityscan.Scan{}, fmt.Errorf("security finding does not belong to the active workspace")
	}
	return scan, nil
}

func sameSecurityWorkspace(scan securityscan.Scan, workspace string) bool {
	if scan.ProjectID != workspace {
		return false
	}
	scanRoot, scanErr := filepath.EvalSymlinks(scan.Target.Repository)
	workspaceRoot, workspaceErr := filepath.EvalSymlinks(workspace)
	if scanErr != nil || workspaceErr != nil {
		return false
	}
	scanRoot, scanErr = filepath.Abs(scanRoot)
	workspaceRoot, workspaceErr = filepath.Abs(workspaceRoot)
	return scanErr == nil && workspaceErr == nil && filepath.Clean(scanRoot) == filepath.Clean(workspaceRoot)
}

func executeSecurityPublicationDriver(s *Service, ctx context.Context, sessionID string, driver tool.Driver, call tool.Call) (tool.Result, error) {
	if s.coding == nil {
		return tool.Result{}, fmt.Errorf("security publication runtime is unavailable")
	}
	run, err := s.coding.StartRunWithMetadata(ctx, "Publish verified security finding", map[string]string{
		"session_id": sessionID, "automation_kind": "security_publication", "tool": call.Name,
	})
	if err != nil {
		return tool.Result{}, err
	}
	var executionErr error
	var result tool.Result
	prepared, ready, prepareErr := s.coding.PrepareDriver(ctx, run, driver, call)
	if prepareErr != nil {
		executionErr = prepareErr
	} else if !ready && prepared.Approval != nil {
		if resolveErr := s.coding.ResolveApproval(ctx, run, call.ID, agentservice.ApprovalOnce, "explicit security publication action"); resolveErr != nil {
			executionErr = resolveErr
		} else {
			executed, executeErr := s.coding.ExecuteDriver(ctx, run, driver, call, nil)
			result, executionErr = executed.Result, executeErr
		}
	} else if !ready {
		result = prepared.Result
	} else {
		executed, executeErr := s.coding.ExecutePreparedDriver(ctx, run, driver, call, nil)
		result, executionErr = executed.Result, executeErr
	}
	if result.IsError && executionErr == nil {
		executionErr = errors.New("configured publication tool returned an error")
	}
	completeErr := s.coding.CompleteRun(context.WithoutCancel(ctx), run, result.Content, executionErr)
	return result, errors.Join(executionErr, completeErr)
}

func configuredSecurityRoute(s *Service, configured config.ModelRouteConfig, fallback securityscan.Route) securityscan.Route {
	if configured.Provider != "" && configured.Model != "" {
		return securityscan.Route{Provider: configured.Provider, Model: configured.Model, Reasoning: configured.Reasoning}
	}
	if fallback.Provider != "" && fallback.Model != "" {
		return fallback
	}
	return securityscan.Route{Provider: s.cfg.Defaults.Provider, Model: s.cfg.Defaults.Model, Reasoning: s.cfg.Defaults.Reasoning}
}
func publicationResultFields(structured json.RawMessage, content string) (string, string) {
	var payload map[string]any
	_ = json.Unmarshal(structured, &payload)
	externalID := firstPublicationString(payload, "identifier", "id", "issueId", "issue_id")
	externalURL := firstPublicationString(payload, "url", "issueUrl", "issue_url")
	if externalURL == "" {
		for _, field := range strings.Fields(content) {
			if strings.HasPrefix(field, "https://") {
				externalURL = strings.TrimRight(field, ".,;)")
				break
			}
		}
	}
	return externalID, externalURL
}

func firstPublicationString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	for _, value := range payload {
		if nested, ok := value.(map[string]any); ok {
			if found := firstPublicationString(nested, keys...); found != "" {
				return found
			}
		}
	}
	return ""
}
