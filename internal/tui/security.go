package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Viking602/azem/internal/securityscan"
)

func (m AppModel) executeSecurityCommand(arguments []string) (tea.Model, tea.Cmd) {
	if len(arguments) == 0 || arguments[0] == "scans" {
		m.securitySelectedScanID = ""
		m.openOverlay(OverlaySecurity)
		return m.beginAction(Action{Kind: ActionListSecurityScans, Target: m.workspace, Limit: 100})
	}
	switch arguments[0] {
	case "scan":
		mode := securityscan.ModeStandard
		if len(arguments) > 2 {
			m.errorBanner = "/security scan [standard|deep]"
			return m, nil
		}
		if len(arguments) == 2 {
			mode = securityscan.Mode(arguments[1])
			if mode != securityscan.ModeStandard && mode != securityscan.ModeDeep {
				m.errorBanner = "/security scan [standard|deep]"
				return m, nil
			}
		}
		payload, _ := json.Marshal(securityscan.StartRequest{
			ProjectID: m.workspace, SessionID: m.sessionID, Repository: m.workspace,
			TargetKind: securityscan.TargetRepository, Mode: mode,
			Route: securityscan.Route{Provider: m.provider, Model: m.model, Reasoning: m.reasoning},
		})
		m.openOverlay(OverlaySecurity)
		m.securitySelectedScanID = ""
		return m.beginAction(Action{Kind: ActionStartSecurityScan, Payload: payload})
	case "show":
		if len(arguments) != 2 {
			m.errorBanner = "/security show <scan-id>"
			return m, nil
		}
		m.openOverlay(OverlaySecurity)
		m.securitySelectedScanID = arguments[1]
		return m.beginAction(Action{Kind: ActionGetSecurityScan, Target: arguments[1]})
	case "findings":
		if len(arguments) != 2 {
			m.errorBanner = "/security findings <scan-id>"
			return m, nil
		}
		m.openOverlay(OverlaySecurity)
		m.securitySelectedScanID = arguments[1]
		return m.beginAction(Action{Kind: ActionListSecurityFindings, Target: arguments[1]})
	case "cancel":
		if len(arguments) != 2 {
			m.errorBanner = "/security cancel <scan-id>"
			return m, nil
		}
		m.openOverlay(OverlaySecurity)
		m.securitySelectedScanID = arguments[1]
		return m.beginAction(Action{Kind: ActionCancelSecurityScan, Target: arguments[1]})
	case "resume":
		if len(arguments) != 2 {
			m.errorBanner = "/security resume <scan-id>"
			return m, nil
		}
		m.openOverlay(OverlaySecurity)
		m.securitySelectedScanID = arguments[1]
		return m.beginAction(Action{Kind: ActionResumeSecurityScan, Target: arguments[1]})
	case "patch":
		if len(arguments) != 2 {
			m.errorBanner = "/security patch <occurrence-id>"
			return m, nil
		}
		m.openOverlay(OverlaySecurity)
		payload, _ := json.Marshal(securityscan.PatchRequest{
			OccurrenceIDs: []string{arguments[1]}, Route: securityscan.Route{Provider: m.provider, Model: m.model, Reasoning: m.reasoning},
		})
		return m.beginAction(Action{Kind: ActionPatchSecurityFinding, Payload: payload})
	case "patch-pr":
		if len(arguments) != 2 {
			m.errorBanner = "/security patch-pr <occurrence-id>"
			return m, nil
		}
		m.openOverlay(OverlaySecurity)
		payload, _ := json.Marshal(securityscan.PatchRequest{
			OccurrenceIDs: []string{arguments[1]},
		})
		return m.beginAction(Action{Kind: ActionPatchSecurityWithPR, Payload: payload})
	case "triage":
		if len(arguments) != 3 {
			m.errorBanner = "/security triage <occurrence-id> <open|false-positive|already-fixed|wont-fix>"
			return m, nil
		}
		reason := strings.ReplaceAll(arguments[2], "-", "_")
		status := "closed"
		if reason == "open" {
			status, reason = "open", ""
		} else if reason != "false_positive" && reason != "already_fixed" && reason != "wont_fix" {
			m.errorBanner = "/security triage <occurrence-id> <open|false-positive|already-fixed|wont-fix>"
			return m, nil
		}
		m.openOverlay(OverlaySecurity)
		payload, _ := json.Marshal(securityscan.Triage{OccurrenceID: arguments[1], Status: status, CloseReason: reason})
		return m.beginAction(Action{Kind: ActionSetSecurityTriage, Payload: payload})
	case "export":
		if len(arguments) != 3 || (arguments[2] != "json" && arguments[2] != "csv" && arguments[2] != "sarif") {
			m.errorBanner = "/security export <scan-id> <json|csv|sarif>"
			return m, nil
		}
		m.openOverlay(OverlaySecurity)
		m.securitySelectedScanID = arguments[1]
		return m.beginAction(Action{Kind: ActionExportSecurityScan, Target: arguments[1], Decision: arguments[2]})
	case "publish":
		if len(arguments) != 2 {
			m.errorBanner = "/security publish <scan-id>"
			return m, nil
		}
		m.openOverlay(OverlaySecurity)
		payload, _ := json.Marshal(map[string]string{"scanId": arguments[1]})
		m.securitySelectedScanID = arguments[1]
		return m.beginAction(Action{Kind: ActionPublishSecurityScan, Target: arguments[1], Payload: payload})
	case "reconcile-publication":
		if len(arguments) != 4 || (arguments[3] != "published" && arguments[3] != "retry") {
			m.errorBanner = "/security reconcile-publication <scan-id> <occurrence-id> <published|retry>"
			return m, nil
		}
		m.openOverlay(OverlaySecurity)
		m.securitySelectedScanID = arguments[1]
		payload, _ := json.Marshal(map[string]string{
			"scanId": arguments[1], "occurrenceId": arguments[2], "decision": arguments[3],
		})
		return m.beginAction(Action{Kind: ActionReconcileSecurityPublish, Target: arguments[1], Payload: payload})
	default:
		m.errorBanner = "/security [scan [standard|deep] | scans | findings <scan-id> | show <scan-id> | cancel <scan-id> | resume <scan-id> | patch <occurrence-id> | patch-pr <occurrence-id> | triage <occurrence-id> <open|false-positive|already-fixed|wont-fix> | export <scan-id> <json|csv|sarif> | publish <scan-id> | reconcile-publication <scan-id> <occurrence-id> <published|retry>]"
		return m, nil
	}
}

func upsertSecurityScan(scans []securityscan.Scan, scan securityscan.Scan) []securityscan.Scan {
	result := make([]securityscan.Scan, 0, len(scans)+1)
	for _, current := range scans {
		if current.ID != scan.ID {
			result = append(result, current)
		}
	}
	result = append(result, scan)
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result
}

func (m AppModel) securityOptions() []overlayOption {
	options := make([]overlayOption, 0, len(m.securityScans))
	for _, scan := range m.securityScans {
		label := fmt.Sprintf("%s · %s", m.tr("security.mode."+string(scan.Mode)), scan.Target.DisplayName)
		detail := fmt.Sprintf("%s · %s/%s · %s · %s", scan.ID, scan.Status, scan.Route.Provider, scan.Route.Model, securityTime(scan.CreatedAt))
		options = append(options, overlayOption{Label: label, Detail: detail, State: string(scan.Status)})
	}
	return options
}

func (m AppModel) securityDescription() []string {
	if m.securityProjection == nil {
		return []string{m.tr("security.start")}
	}
	scan := m.securityProjection.Scan
	progress := m.securityProjection.Progress
	lines := []string{
		m.tr("security.summary", map[string]string{"id": scan.ID, "status": string(scan.Status), "phase": string(scan.Phase)}),
		m.tr("security.coverage", map[string]string{
			"coverage": string(scan.Completeness),
			"files":    fmt.Sprintf("%d/%d", progress.FilesCompleted, progress.FilesTotal),
			"workers":  fmt.Sprintf("%d/%d", progress.WorkersDone, progress.WorkersPlanned),
		}),
	}
	if scan.BlockingReason != "" {
		lines = append(lines,
			m.tr("security.blocked", map[string]string{"reason": scan.BlockingReason}),
			m.tr("security.resume", map[string]string{"id": scan.ID}),
		)
	}
	if scan.Warning != "" {
		lines = append(lines, m.tr("security.warning", map[string]string{"warning": scan.Warning}))
	}
	if m.securityExportPath != "" {
		lines = append(lines, m.tr("security.export", map[string]string{"path": m.securityExportPath}))
	}
	if len(m.securityFindings) > 0 {
		lines = append(lines, m.tr("security.findings", map[string]string{"count": fmt.Sprint(len(m.securityFindings))}))
	}
	if m.securityFinding != nil {
		lines = append(lines, m.tr("security.finding", map[string]string{
			"id": m.securityFinding.OccurrenceID, "severity": m.securityFinding.Severity.Level, "title": m.securityFinding.Title,
		}))
	}
	for index, finding := range m.securityFindings {
		if index >= 5 {
			lines = append(lines, m.tr("security.more", map[string]string{"count": fmt.Sprint(len(m.securityFindings) - index)}))
			break
		}
		lines = append(lines, m.tr("security.finding", map[string]string{
			"id": finding.OccurrenceID, "severity": finding.Severity.Level, "title": finding.Title,
		}))
	}
	if m.securityPatch != nil {
		lines = append(lines, m.tr("security.patch", map[string]string{
			"status": m.securityPatch.Status, "detail": m.firstSecurityText(m.securityPatch.Verification, m.securityPatch.Reason),
		}))
	}
	if m.securityFinding != nil {
		if triage, ok := m.securityProjection.Triage[m.securityFinding.OccurrenceID]; ok {
			lines = append(lines, m.tr("security.triage", map[string]string{"status": triage.Status, "reason": triage.CloseReason}))
		}
	}
	if m.securityPublication != "" {
		lines = append(lines, m.tr("security.publication", map[string]string{"value": m.securityPublication}))
	}
	return lines
}

func (m AppModel) firstSecurityText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return m.tr("security.no_detail")
}

func securityTime(value time.Time) string {
	if value.IsZero() {
		return "unknown"
	}
	return value.Local().Format("Jan 02 15:04")
}
