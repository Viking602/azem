package desktop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	DefaultWindowWidth  = 1440
	DefaultWindowHeight = 920
	MinWindowWidth      = 880
	MinWindowHeight     = 640
	maxWindowDimension  = 10000
	windowStateFileName = "window.json"
)

// WindowGeometry is the last known main-window frame on this machine.
// Coordinates are absolute DIP pixels (same space as Wails Bounds/Position).
type WindowGeometry struct {
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Maximised bool   `json:"maximised,omitempty"`
	ScreenID  string `json:"screenId,omitempty"`
	HasOrigin bool   `json:"hasOrigin,omitempty"`
}

// WindowStatePath returns the machine-local path used for window geometry.
func WindowStatePath(stateDir string) string {
	return filepath.Join(stateDir, windowStateFileName)
}

// DefaultWindowGeometry is the first-launch frame.
func DefaultWindowGeometry() WindowGeometry {
	return WindowGeometry{
		Width:  DefaultWindowWidth,
		Height: DefaultWindowHeight,
	}
}

// LoadWindowGeometry reads the last saved frame. Missing or invalid files return defaults.
func LoadWindowGeometry(path string) WindowGeometry {
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultWindowGeometry()
	}
	var geometry WindowGeometry
	if err := json.Unmarshal(data, &geometry); err != nil {
		return DefaultWindowGeometry()
	}
	return SanitizeWindowGeometry(geometry)
}

// SaveWindowGeometry atomically writes the frame to path.
func SaveWindowGeometry(path string, geometry WindowGeometry) error {
	geometry = SanitizeWindowGeometry(geometry)
	if path == "" {
		return fmt.Errorf("window state path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create window state directory: %w", err)
	}
	encoded, err := json.MarshalIndent(geometry, "", "  ")
	if err != nil {
		return fmt.Errorf("encode window state: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".window-*.json")
	if err != nil {
		return fmt.Errorf("create window state temp: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	_ = temporary.Chmod(0o600)
	if _, err := temporary.Write(append(encoded, '\n')); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write window state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close window state: %w", err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("replace window state: %w", err)
	}
	return nil
}

// SanitizeWindowGeometry clamps size into a usable range and clears bad origins.
func SanitizeWindowGeometry(geometry WindowGeometry) WindowGeometry {
	if geometry.Width < MinWindowWidth || geometry.Width > maxWindowDimension {
		geometry.Width = DefaultWindowWidth
	}
	if geometry.Height < MinWindowHeight || geometry.Height > maxWindowDimension {
		geometry.Height = DefaultWindowHeight
	}
	if geometry.Width == 0 {
		geometry.Width = DefaultWindowWidth
	}
	if geometry.Height == 0 {
		geometry.Height = DefaultWindowHeight
	}
	// Reject extreme coordinates that would leave the window unusable.
	if geometry.X < -maxWindowDimension || geometry.X > maxWindowDimension ||
		geometry.Y < -maxWindowDimension || geometry.Y > maxWindowDimension {
		geometry.X, geometry.Y = 0, 0
		geometry.HasOrigin = false
		geometry.ScreenID = ""
	}
	return geometry
}

// ScreenBounds is a minimal rectangle used to validate restored origins.
type ScreenBounds struct {
	X, Y, Width, Height int
	ID                  string
}

// FitWindowGeometryToScreens drops origins that no longer intersect any display,
// so a removed external monitor cannot leave the window off-screen.
func FitWindowGeometryToScreens(geometry WindowGeometry, screens []ScreenBounds) WindowGeometry {
	geometry = SanitizeWindowGeometry(geometry)
	if len(screens) == 0 || !geometry.HasOrigin {
		return geometry
	}
	// Prefer the previously used screen when still present.
	if geometry.ScreenID != "" {
		for _, screen := range screens {
			if screen.ID == geometry.ScreenID && rectsIntersect(
				geometry.X, geometry.Y, geometry.Width, geometry.Height,
				screen.X, screen.Y, screen.Width, screen.Height,
			) {
				return geometry
			}
		}
	}
	for _, screen := range screens {
		if rectsIntersect(
			geometry.X, geometry.Y, geometry.Width, geometry.Height,
			screen.X, screen.Y, screen.Width, screen.Height,
		) {
			return geometry
		}
	}
	// Screen gone: keep size, forget position so the OS can place it.
	geometry.X, geometry.Y = 0, 0
	geometry.HasOrigin = false
	geometry.ScreenID = ""
	return geometry
}

func rectsIntersect(ax, ay, aw, ah, bx, by, bw, bh int) bool {
	// Require a meaningful overlap so a 1px edge contact is not enough.
	const minOverlap = 48
	left := max(ax, bx)
	top := max(ay, by)
	right := min(ax+aw, bx+bw)
	bottom := min(ay+ah, by+bh)
	return right-left >= minOverlap && bottom-top >= minOverlap
}
