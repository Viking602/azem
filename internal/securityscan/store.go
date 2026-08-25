package securityscan

import (
	"context"
	"errors"
)

var (
	ErrNotFound          = errors.New("security scan: not found")
	ErrTerminalState     = errors.New("security scan: terminal state is immutable")
	ErrPublicationExists = errors.New("security scan: publication already claimed")
)

type Store interface {
	CreateScan(context.Context, Scan) error
	UpdateScan(context.Context, Scan) error
	Scan(context.Context, string) (Scan, error)
	AddUsage(context.Context, string, ExecutionResult) error
	ListScans(context.Context, string, int) ([]Scan, error)
	ActiveDeepScan(context.Context, string, string, string) (Scan, error)
	SaveProgress(context.Context, Progress) error
	Progress(context.Context, string) (Progress, error)
	CreateWorker(context.Context, Worker) error
	CompleteWorker(context.Context, Worker, ExecutionResult) error
	UpdateWorker(context.Context, Worker) error
	Workers(context.Context, string) ([]Worker, error)
	SaveCompletion(context.Context, Scan, []Artifact, []Finding) error
	ScanForOccurrence(context.Context, string) (Scan, error)
	SaveRemediation(context.Context, RemediationAttempt) error
	SavePublication(context.Context, Publication) error
	ClaimPublication(context.Context, Publication) error
	DeletePublication(context.Context, string, string, string) error
	ReconcilePublication(context.Context, Publication) error
	PublishedOccurrences(context.Context, string, string) (map[string]bool, error)
	Findings(context.Context, string) ([]Finding, error)
	Finding(context.Context, string) (Finding, error)
	SaveTriage(context.Context, Triage) error
	Triage(context.Context, string) (Triage, error)
	SaveMatch(context.Context, string, string, string, float64) error
	Close() error
}
