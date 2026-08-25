package securityscan

import (
	"context"
	"fmt"
	"strings"
)

func (s *Service) PublicationIssues(ctx context.Context, scanID, destination string) ([]PublicationIssue, error) {
	if strings.TrimSpace(destination) == "" {
		return nil, fmt.Errorf("security scan: publication destination is required")
	}
	scan, err := s.store.Scan(ctx, scanID)
	if err != nil {
		return nil, err
	}
	if scan.Status != StatusComplete {
		return nil, fmt.Errorf("security scan: only completed scans can be published")
	}
	findings, err := s.store.Findings(ctx, scanID)
	if err != nil {
		return nil, err
	}
	published, err := s.store.PublishedOccurrences(ctx, scanID, destination)
	if err != nil {
		return nil, err
	}
	issues := make([]PublicationIssue, 0, len(findings))
	for _, finding := range findings {
		if published[finding.OccurrenceID] {
			continue
		}
		var locations []string
		for _, location := range finding.Locations {
			locations = append(locations, fmt.Sprintf("- `%s:%d` (%s)", location.Path, location.StartLine, location.Role))
		}
		description := strings.Join([]string{
			finding.Summary,
			"",
			"Severity: " + finding.Severity.Level,
			"Confidence: " + finding.Confidence.Level,
			"Scan: " + scan.ID,
			"Occurrence: " + finding.OccurrenceID,
			"",
			"Affected locations:", strings.Join(locations, "\n"),
			"",
			"Remediation:", finding.Remediation,
		}, "\n")
		issues = append(issues, PublicationIssue{OccurrenceID: finding.OccurrenceID, Title: finding.Title, Description: description})
	}
	return issues, nil
}

func (s *Service) SavePublication(ctx context.Context, publication Publication) error {
	if publication.ScanID == "" || publication.OccurrenceID == "" || publication.Destination == "" {
		return fmt.Errorf("security scan: publication identity is incomplete")
	}
	if publication.Status != "pending" && publication.Status != "publishing" && publication.Status != "published" && publication.Status != "failed" {
		return fmt.Errorf("security scan: invalid publication status %q", publication.Status)
	}
	if publication.CreatedAt.IsZero() {
		publication.CreatedAt = s.now().UTC()
	}
	publication.UpdatedAt = s.now().UTC()
	return s.store.SavePublication(ctx, publication)
}

func (s *Service) ClaimPublication(ctx context.Context, publication Publication) error {
	if publication.ScanID == "" || publication.OccurrenceID == "" || publication.Destination == "" {
		return fmt.Errorf("security scan: publication identity is incomplete")
	}
	publication.Status = "publishing"
	publication.CreatedAt = s.now().UTC()
	publication.UpdatedAt = publication.CreatedAt
	return s.store.ClaimPublication(ctx, publication)
}

func (s *Service) ReconcilePublication(ctx context.Context, scanID, occurrenceID, destination, decision string) error {
	if scanID == "" || occurrenceID == "" || destination == "" {
		return fmt.Errorf("security scan: publication identity is incomplete")
	}
	switch decision {
	case "published":
		publication := Publication{
			ScanID: scanID, OccurrenceID: occurrenceID, Destination: destination, Status: "published",
			UpdatedAt: s.now().UTC(),
		}
		return s.store.ReconcilePublication(ctx, publication)
	case "retry":
		return s.store.DeletePublication(ctx, scanID, occurrenceID, destination)
	default:
		return fmt.Errorf("security scan: publication reconciliation must be published or retry")
	}
}
