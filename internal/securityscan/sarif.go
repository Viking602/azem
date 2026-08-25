package securityscan

import (
	"encoding/json"
	"sort"
)

func BuildSARIF(scan Scan, findings []Finding) ([]byte, error) {
	byRule := make(map[string][]Finding)
	for _, finding := range findings {
		byRule[finding.RuleID] = append(byRule[finding.RuleID], finding)
	}
	ruleIDs := make([]string, 0, len(byRule))
	for ruleID := range byRule {
		ruleIDs = append(ruleIDs, ruleID)
	}
	sort.Strings(ruleIDs)
	rules := make([]any, 0, len(ruleIDs))
	for _, ruleID := range ruleIDs {
		example := byRule[ruleID][0]
		rules = append(rules, map[string]any{
			"id":               ruleID,
			"name":             example.Title,
			"shortDescription": map[string]any{"text": example.Summary},
			"help":             map[string]any{"text": example.Remediation},
			"properties": map[string]any{
				"tags":      append([]string{"security"}, example.Taxonomy.CWE...),
				"precision": example.Confidence.Level,
			},
		})
	}
	results := make([]any, 0, len(findings))
	for _, finding := range findings {
		locations := make([]any, 0, len(finding.Locations))
		for _, location := range finding.Locations {
			end := location.EndLine
			if end <= 0 {
				end = location.StartLine
			}
			locations = append(locations, map[string]any{
				"physicalLocation": map[string]any{
					"artifactLocation": map[string]any{"uri": location.Path, "uriBaseId": "%SRCROOT%"},
					"region":           map[string]any{"startLine": location.StartLine, "endLine": end},
				},
				"message": map[string]any{"text": location.Role},
			})
		}
		results = append(results, map[string]any{
			"ruleId":              finding.RuleID,
			"level":               sarifLevel(finding.Severity.Level),
			"message":             map[string]any{"text": finding.Summary + "\n\nRemediation: " + finding.Remediation},
			"locations":           locations,
			"partialFingerprints": map[string]any{"primaryLocationLineHash": finding.Fingerprints.Primary},
			"properties": map[string]any{
				"findingId":         finding.FindingID,
				"occurrenceId":      finding.OccurrenceID,
				"severity":          finding.Severity.Level,
				"confidence":        finding.Confidence.Level,
				"security-severity": finding.Severity.Score,
			},
		})
	}
	document := map[string]any{
		"$schema": "https://docs.oasis-open.org/sarif/sarif/v2.1.0/os/schemas/sarif-schema-2.1.0.json",
		"version": "2.1.0",
		"runs": []any{map[string]any{
			"tool": map[string]any{"driver": map[string]any{
				"name":           ProducerName,
				"version":        ProducerVersion,
				"informationUri": "https://github.com/Viking602/azem",
				"rules":          rules,
			}},
			"automationDetails":  map[string]any{"id": scan.ID},
			"originalUriBaseIds": map[string]any{"%SRCROOT%": map[string]any{"uri": "file:///"}},
			"results":            results,
		}},
	}
	return json.MarshalIndent(document, "", "  ")
}

func sarifLevel(severity string) string {
	switch severity {
	case "critical", "high":
		return "error"
	case "medium":
		return "warning"
	default:
		return "note"
	}
}
