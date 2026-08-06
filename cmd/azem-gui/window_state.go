//go:build darwin || windows

package main

import (
	"sync"
	"time"

	"github.com/Viking602/azem/internal/desktop"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const windowStateSaveDelay = 250 * time.Millisecond

// windowStateTracker debounces geometry writes while the user resizes/moves
// the main window, and flushes immediately on close.
type windowStateTracker struct {
	path string
	app  *application.App

	mu    sync.Mutex
	last  desktop.WindowGeometry
	timer *time.Timer
	dirty bool
}

func newWindowStateTracker(path string, app *application.App, initial desktop.WindowGeometry) *windowStateTracker {
	return &windowStateTracker{path: path, app: app, last: desktop.SanitizeWindowGeometry(initial)}
}

func loadMainWindowGeometry(app *application.App, stateDir string) desktop.WindowGeometry {
	path := desktop.WindowStatePath(stateDir)
	geometry := desktop.LoadWindowGeometry(path)
	screens := screenBounds(app)
	return desktop.FitWindowGeometryToScreens(geometry, screens)
}

func applyWindowGeometry(options *application.WebviewWindowOptions, geometry desktop.WindowGeometry) {
	geometry = desktop.SanitizeWindowGeometry(geometry)
	options.Width = geometry.Width
	options.Height = geometry.Height
	options.MinWidth = desktop.MinWindowWidth
	options.MinHeight = desktop.MinWindowHeight
	if geometry.HasOrigin {
		options.InitialPosition = application.WindowXY
		options.X = geometry.X
		options.Y = geometry.Y
	}
	if geometry.Maximised {
		options.StartState = application.WindowStateMaximised
	}
}

func bindWindowStatePersistence(window *application.WebviewWindow, tracker *windowStateTracker) {
	if window == nil || tracker == nil {
		return
	}
	persist := func(_ *application.WindowEvent) {
		tracker.capture(window, false)
	}
	flush := func(_ *application.WindowEvent) {
		tracker.capture(window, true)
	}
	window.OnWindowEvent(events.Common.WindowDidMove, persist)
	window.OnWindowEvent(events.Common.WindowDidResize, persist)
	window.OnWindowEvent(events.Common.WindowMaximise, persist)
	window.OnWindowEvent(events.Common.WindowUnMaximise, persist)
	window.OnWindowEvent(events.Common.WindowClosing, flush)
}

func (t *windowStateTracker) capture(window *application.WebviewWindow, flush bool) {
	if t == nil || window == nil {
		return
	}
	if window.IsMinimised() {
		return
	}
	bounds := window.Bounds()
	if bounds.Width <= 0 || bounds.Height <= 0 {
		return
	}
	maximised := window.IsMaximised()
	screenID := ""
	if t.app != nil {
		if nearest := t.app.Screen.ScreenNearestDipRect(bounds); nearest != nil {
			screenID = nearest.ID
		}
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if !maximised {
		t.last.X = bounds.X
		t.last.Y = bounds.Y
		t.last.Width = bounds.Width
		t.last.Height = bounds.Height
		t.last.HasOrigin = true
	}
	t.last.Maximised = maximised
	if screenID != "" {
		t.last.ScreenID = screenID
	}
	t.last = desktop.SanitizeWindowGeometry(t.last)
	t.dirty = true
	if flush {
		t.saveLocked()
		return
	}
	if t.timer != nil {
		t.timer.Stop()
	}
	t.timer = time.AfterFunc(windowStateSaveDelay, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.saveLocked()
	})
}

func (t *windowStateTracker) Flush() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	t.saveLocked()
}

func (t *windowStateTracker) saveLocked() {
	if !t.dirty {
		return
	}
	if err := desktop.SaveWindowGeometry(t.path, t.last); err == nil {
		t.dirty = false
	}
}

func screenBounds(app *application.App) []desktop.ScreenBounds {
	if app == nil || app.Screen == nil {
		return nil
	}
	screens := app.Screen.GetAll()
	result := make([]desktop.ScreenBounds, 0, len(screens))
	for _, screen := range screens {
		if screen == nil {
			continue
		}
		// Prefer work area so restored windows stay clear of the menu bar/dock.
		area := screen.WorkArea
		if area.Width <= 0 || area.Height <= 0 {
			area = screen.Bounds
		}
		result = append(result, desktop.ScreenBounds{
			ID: screen.ID, X: area.X, Y: area.Y, Width: area.Width, Height: area.Height,
		})
	}
	return result
}
