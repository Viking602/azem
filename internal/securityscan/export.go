package securityscan

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (s *Service) Export(ctx context.Context, scanID, format string) (string, error) {
	scan, err := s.store.Scan(ctx, scanID)
	if err != nil {
		return "", err
	}
	if scan.Status != StatusComplete {
		return "", fmt.Errorf("security scan: only completed scans can be exported")
	}
	findings, err := s.store.Findings(ctx, scanID)
	if err != nil {
		return "", err
	}
	format = strings.ToLower(strings.TrimSpace(format))
	var relative string
	var payload []byte
	switch format {
	case "json":
		relative = "exports/findings.json"
		payload, err = canonicalJSON(FindingsDocument{DocumentType: FindingsDocumentType, SchemaVersion: ContractVersion, ScanID: scan.ID, Findings: findings})
	case "csv":
		relative = "exports/findings.csv"
		payload, err = buildFindingsCSV(findings, scan.Completeness)
	case "sarif":
		relative = "exports/results.sarif"
		payload, err = os.ReadFile(filepath.Join(scan.OutputDirectory, filepath.FromSlash(relative)))
	default:
		return "", fmt.Errorf("security scan: export format must be json, csv, or sarif")
	}
	if err != nil {
		return "", err
	}
	if format != "sarif" {
		if err := atomicWriteScanFile(ctx, scan.OutputDirectory, relative, payload); err != nil {
			return "", err
		}
	}
	return filepath.Join(scan.OutputDirectory, filepath.FromSlash(relative)), nil
}

func buildFindingsCSV(findings []Finding, completeness Completeness) ([]byte, error) {
	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	if err := writer.Write([]string{"finding_id", "occurrence_id", "rule_id", "title", "severity", "confidence", "path", "start_line", "summary", "remediation", "coverage"}); err != nil {
		return nil, err
	}
	for _, finding := range findings {
		locations := finding.Locations
		if len(locations) == 0 {
			locations = []FindingLocation{{}}
		}
		for _, location := range locations {
			row := []string{
				finding.FindingID, finding.OccurrenceID, finding.RuleID, finding.Title,
				finding.Severity.Level, finding.Confidence.Level, location.Path, strconv.Itoa(location.StartLine),
				finding.Summary, finding.Remediation, string(completeness),
			}
			for index := range row {
				row[index] = safeCSVCell(row[index])
			}
			if err := writer.Write(row); err != nil {
				return nil, err
			}
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func safeCSVCell(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	var decoded any
	if json.Unmarshal([]byte(value), &decoded) == nil {
		return value
	}
	return value
}
