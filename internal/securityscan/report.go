package securityscan

import (
	"fmt"
	"sort"
	"strings"
)

func BuildReport(scan Scan, findings []Finding, coverage Coverage) []byte {
	var out strings.Builder
	out.WriteString("# Azem Security Scan\n\n")
	out.WriteString("## Scan summary\n\n")
	fmt.Fprintf(&out, "- Scan ID: `%s`\n", scan.ID)
	fmt.Fprintf(&out, "- Target: `%s`\n", scan.Target.DisplayName)
	fmt.Fprintf(&out, "- Mode: `%s`\n", scan.Mode)
	fmt.Fprintf(&out, "- Coverage: `%s`\n", coverage.Completeness)
	fmt.Fprintf(&out, "- Model: `%s/%s`\n", scan.Route.Provider, scan.Route.Model)
	fmt.Fprintf(&out, "- Findings: %d\n", len(findings))
	if scan.Warning != "" {
		fmt.Fprintf(&out, "- Warning: %s\n", scan.Warning)
	}
	out.WriteString("\n")

	if len(findings) == 0 {
		out.WriteString("## Findings\n\nNo reportable findings were recorded. This means only that the reviewed scope produced no reportable finding; consult the coverage section before treating the result as complete.\n\n")
	} else {
		out.WriteString("## Findings\n\n")
		ordered := append([]Finding(nil), findings...)
		sort.SliceStable(ordered, func(i, j int) bool {
			left, right := severityRank(ordered[i].Severity.Level), severityRank(ordered[j].Severity.Level)
			if left != right {
				return left < right
			}
			return ordered[i].Title < ordered[j].Title
		})
		for index, finding := range ordered {
			fmt.Fprintf(&out, "### %d. %s\n\n", index+1, finding.Title)
			fmt.Fprintf(&out, "- Severity: **%s**\n", finding.Severity.Level)
			fmt.Fprintf(&out, "- Confidence: **%s**\n", finding.Confidence.Level)
			fmt.Fprintf(&out, "- Rule: `%s`\n", finding.RuleID)
			fmt.Fprintf(&out, "- Finding ID: `%s`\n\n", finding.FindingID)
			out.WriteString(finding.Summary + "\n\n")
			out.WriteString("**Locations**\n\n")
			for _, location := range finding.Locations {
				end := location.EndLine
				if end <= 0 || end == location.StartLine {
					fmt.Fprintf(&out, "- `%s:%d` — %s\n", location.Path, location.StartLine, location.Role)
				} else {
					fmt.Fprintf(&out, "- `%s:%d-%d` — %s\n", location.Path, location.StartLine, end, location.Role)
				}
			}
			out.WriteString("\n**Remediation**\n\n" + finding.Remediation + "\n\n")
			if len(finding.RemediationTests) > 0 {
				out.WriteString("**Verification**\n\n")
				for _, test := range finding.RemediationTests {
					out.WriteString("- " + test + "\n")
				}
				out.WriteString("\n")
			}
		}
	}

	out.WriteString("## Coverage\n\n")
	fmt.Fprintf(&out, "Inventory strategy: `%s`\n\n", coverage.InventoryStrategy)
	if len(coverage.Surfaces) > 0 {
		out.WriteString("| Surface | Disposition |\n|---|---|\n")
		for _, surface := range coverage.Surfaces {
			fmt.Fprintf(&out, "| %s | `%s` |\n", escapeMarkdownCell(surface.Label), surface.Disposition)
		}
		out.WriteString("\n")
	}
	if len(coverage.ExplicitExclusions) > 0 {
		out.WriteString("### Explicit exclusions\n\n")
		for _, exclusion := range coverage.ExplicitExclusions {
			fmt.Fprintf(&out, "- `%s` — %s\n", exclusion.Pattern, exclusion.Reason)
		}
		out.WriteString("\n")
	}
	if len(coverage.Deferred) > 0 {
		out.WriteString("### Deferred work\n\n")
		for _, deferred := range coverage.Deferred {
			out.WriteString("- " + deferred.Reason + "\n")
		}
		out.WriteString("\n")
	}
	return []byte(out.String())
}

func severityRank(value string) int {
	switch value {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	default:
		return 4
	}
}

func escapeMarkdownCell(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "|", "\\|"), "\n", " ")
}
