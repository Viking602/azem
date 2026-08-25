package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/Viking602/venat/tool"
)

const backgroundJobRetention = 5 * time.Minute

type backgroundJobSnapshot struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	Label      string `json:"label"`
	Owner      string `json:"owner,omitempty"`
	DurationMS int64  `json:"durationMs"`
	ResultText string `json:"resultText,omitempty"`
	ErrorText  string `json:"errorText,omitempty"`
}

type backgroundJob struct {
	id, kind, label, owner string
	status                 string
	startedAt, settledAt   time.Time
	resultText, errorText  string
	cancel                 context.CancelFunc
}

type backgroundJobManager struct {
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	jobs    map[string]*backgroundJob
	changed chan struct{}
	wg      sync.WaitGroup
	closed  bool
}

func newBackgroundJobManager(parent context.Context) *backgroundJobManager {
	ctx, cancel := context.WithCancel(parent)
	return &backgroundJobManager{ctx: ctx, cancel: cancel, jobs: make(map[string]*backgroundJob), changed: make(chan struct{})}
}

func (manager *backgroundJobManager) start(kind, label, owner string, run func(context.Context) (tool.Result, error)) (backgroundJobSnapshot, error) {
	if manager == nil || run == nil {
		return backgroundJobSnapshot{}, errors.New("background jobs are unavailable")
	}
	jobID, err := newBackgroundJobID(kind)
	if err != nil {
		return backgroundJobSnapshot{}, err
	}
	ctx, cancel := context.WithCancel(manager.ctx)
	job := &backgroundJob{id: jobID, kind: kind, label: label, owner: owner, status: "running", startedAt: time.Now(), cancel: cancel}
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		cancel()
		return backgroundJobSnapshot{}, errors.New("background job manager is closed")
	}
	manager.pruneLocked(time.Now())
	manager.jobs[jobID] = job
	manager.signalLocked()
	manager.wg.Add(1)
	manager.mu.Unlock()
	go func() {
		defer manager.wg.Done()
		result, runErr := run(ctx)
		manager.mu.Lock()
		defer manager.mu.Unlock()
		current := manager.jobs[jobID]
		if current == nil || current.status != "running" {
			return
		}
		current.settledAt = time.Now()
		switch {
		case ctx.Err() != nil:
			current.status, current.errorText = "cancelled", ctx.Err().Error()
		case runErr != nil:
			current.status, current.errorText = "failed", runErr.Error()
		case result.IsError:
			current.status, current.errorText = "failed", result.Content
		default:
			current.status, current.resultText = "completed", result.Content
		}
		manager.signalLocked()
	}()
	return manager.snapshotJob(job, time.Now()), nil
}

func (manager *backgroundJobManager) snapshots(owner string) []backgroundJobSnapshot {
	if manager == nil {
		return nil
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	now := time.Now()
	manager.pruneLocked(now)
	result := make([]backgroundJobSnapshot, 0, len(manager.jobs))
	for _, job := range manager.jobs {
		if owner != "" && job.owner != owner {
			continue
		}
		result = append(result, manager.snapshotJob(job, now))
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
	return result
}

func (manager *backgroundJobManager) wait(ctx context.Context, owner string, ids []string, timeout time.Duration) []backgroundJobSnapshot {
	if manager == nil {
		return nil
	}
	var timer <-chan time.Time
	if timeout > 0 {
		clock := time.NewTimer(timeout)
		defer clock.Stop()
		timer = clock.C
	}
	for {
		manager.mu.Lock()
		now := time.Now()
		manager.pruneLocked(now)
		selected := manager.selectLocked(owner, ids, now)
		for _, job := range selected {
			if job.Status != "running" {
				manager.mu.Unlock()
				return selected
			}
		}
		changed := manager.changed
		manager.mu.Unlock()
		if len(selected) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return selected
		case <-timer:
			return selected
		case <-changed:
		}
	}
}

func (manager *backgroundJobManager) cancelJobs(owner string, ids []string) []backgroundJobSnapshot {
	if manager == nil {
		return nil
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	now := time.Now()
	result := make([]backgroundJobSnapshot, 0, len(ids))
	for _, id := range ids {
		job := manager.jobs[id]
		if job == nil || owner != "" && job.owner != owner {
			result = append(result, backgroundJobSnapshot{ID: id, Status: "not_found"})
			continue
		}
		if job.status != "running" {
			result = append(result, manager.snapshotJob(job, now))
			continue
		}
		job.cancel()
		job.status, job.settledAt, job.errorText = "cancelled", now, "cancelled by hub"
		result = append(result, manager.snapshotJob(job, now))
	}
	manager.signalLocked()
	return result
}

func (manager *backgroundJobManager) shutdown(ctx context.Context) error {
	if manager == nil {
		return nil
	}
	manager.mu.Lock()
	if !manager.closed {
		manager.closed = true
		manager.cancel()
		for _, job := range manager.jobs {
			if job.status == "running" {
				job.cancel()
			}
		}
		manager.signalLocked()
	}
	manager.mu.Unlock()
	done := make(chan struct{})
	go func() { manager.wg.Wait(); close(done) }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (manager *backgroundJobManager) selectLocked(owner string, ids []string, now time.Time) []backgroundJobSnapshot {
	if len(ids) == 0 {
		result := make([]backgroundJobSnapshot, 0)
		for _, job := range manager.jobs {
			if owner == "" || job.owner == owner {
				result = append(result, manager.snapshotJob(job, now))
			}
		}
		sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
		return result
	}
	result := make([]backgroundJobSnapshot, 0, len(ids))
	for _, id := range ids {
		if job := manager.jobs[id]; job != nil && (owner == "" || job.owner == owner) {
			result = append(result, manager.snapshotJob(job, now))
		}
	}
	return result
}

func (manager *backgroundJobManager) snapshotJob(job *backgroundJob, now time.Time) backgroundJobSnapshot {
	end := now
	if !job.settledAt.IsZero() {
		end = job.settledAt
	}
	return backgroundJobSnapshot{ID: job.id, Type: job.kind, Status: job.status, Label: job.label, Owner: job.owner, DurationMS: max(0, end.Sub(job.startedAt).Milliseconds()), ResultText: job.resultText, ErrorText: job.errorText}
}

func (manager *backgroundJobManager) pruneLocked(now time.Time) {
	for id, job := range manager.jobs {
		if job.status != "running" && !job.settledAt.IsZero() && now.Sub(job.settledAt) > backgroundJobRetention {
			delete(manager.jobs, id)
		}
	}
}

func (manager *backgroundJobManager) signalLocked() {
	close(manager.changed)
	manager.changed = make(chan struct{})
}

func newBackgroundJobID(kind string) (string, error) {
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return kind + "_" + hex.EncodeToString(random[:]), nil
}
