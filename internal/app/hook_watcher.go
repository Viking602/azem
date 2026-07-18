package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Viking602/azem/internal/hooks"
)

type hookWatchEntry struct {
	sessionID string
	path      string
	config    bool
	source    string
	exists    bool
	modTime   time.Time
	size      int64
	event     string
}

type hookWatcher struct {
	service *Service
	mu      sync.Mutex
	entries map[string]hookWatchEntry
	once    sync.Once
}

func (s *Service) ensureHookWatcher() *hookWatcher {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hookWatcher == nil {
		s.hookWatcher = &hookWatcher{service: s, entries: make(map[string]hookWatchEntry)}
	}
	return s.hookWatcher
}

func (w *hookWatcher) watchConfig(path, source string) {
	w.add("", path, true, source)
}

func (w *hookWatcher) watchFiles(sessionID string, paths []string) {
	for _, path := range paths {
		if filepath.IsAbs(path) {
			w.add(sessionID, filepath.Clean(path), false, "")
		}
	}
}

func (w *hookWatcher) writeConfig(path string, write func() error) error {
	clean := filepath.Clean(path)
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := write(); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	for key, entry := range w.entries {
		if entry.config && filepath.Clean(entry.path) == clean {
			entry.exists, entry.modTime, entry.size = true, info.ModTime(), info.Size()
			w.entries[key] = entry
		}
	}
	return nil
}

func (w *hookWatcher) add(sessionID, path string, config bool, source string) {
	info, err := os.Stat(path)
	entry := hookWatchEntry{sessionID: sessionID, path: path, config: config, source: source, exists: err == nil}
	if err == nil {
		entry.modTime, entry.size = info.ModTime(), info.Size()
	}
	key := sessionID + "\x00" + path
	if config {
		key = "config\x00" + path
	}
	w.mu.Lock()
	w.entries[key] = entry
	w.mu.Unlock()
	w.once.Do(func() {
		w.service.wg.Add(1)
		go w.run(w.service.ctx)
	})
}

func (w *hookWatcher) run(ctx context.Context) {
	defer w.service.wg.Done()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.scan(ctx)
		}
	}
}

func (w *hookWatcher) scan(ctx context.Context) {
	w.mu.Lock()
	changes := make([]hookWatchEntry, 0)
	for key, previous := range w.entries {
		info, err := os.Stat(previous.path)
		current := previous
		current.exists = err == nil
		if err == nil {
			current.modTime, current.size = info.ModTime(), info.Size()
		} else {
			current.modTime, current.size = time.Time{}, 0
		}
		if current.exists != previous.exists || current.modTime != previous.modTime || current.size != previous.size {
			current.event = "change"
			if !current.exists {
				current.event = "unlink"
			} else if !previous.exists {
				current.event = "add"
			}
			changes = append(changes, current)
		}
		current.event = ""
		w.entries[key] = current
	}
	w.mu.Unlock()
	for _, change := range changes {
		if change.config {
			w.service.mu.Lock()
			sessionID := w.service.currentSession
			w.service.mu.Unlock()
			if err := w.service.dispatchLifecycle(ctx, hooks.ConfigChange, w.service.hookMetadata(sessionID, ""), func(e *hooks.Envelope) {
				e.Source, e.FilePath, e.ToolName = firstNonempty(change.source, "user_settings"), change.path, firstNonempty(change.source, "user_settings")
			}); err == nil && w.service.hooks.Registry != nil {
				w.service.hooks.Registry.Replace(hooks.Discover(w.service.hookOptions))
			}
			continue
		}
		_ = w.service.dispatchLifecycle(ctx, hooks.FileChanged, w.service.hookMetadata(change.sessionID, ""), func(e *hooks.Envelope) {
			e.FilePath, e.FileEvent, e.ToolName = change.path, change.event, filepath.Base(change.path)
		})
	}
}

func (s *Service) emitInstructionsLoaded(ctx context.Context, filePath, memoryType, reason string) {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" || !filepath.IsAbs(filePath) {
		return
	}
	_ = s.dispatchLifecycle(ctx, hooks.InstructionsLoaded, s.hookMetadata(s.currentSession, ""), func(e *hooks.Envelope) {
		e.FilePath, e.MemoryType, e.LoadReason, e.ToolName = filePath, memoryType, reason, reason
	})
}
