package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoadWindowGeometry(t *testing.T) {
	dir := t.TempDir()
	path := WindowStatePath(dir)
	want := WindowGeometry{X: 120, Y: 80, Width: 1600, Height: 1000, Maximised: true, ScreenID: "display-2", HasOrigin: true}
	if err := SaveWindowGeometry(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got := LoadWindowGeometry(path)
	if got != want {
		t.Fatalf("load = %#v, want %#v", got, want)
	}
}

func TestSanitizeWindowGeometryRejectsTinyFrames(t *testing.T) {
	got := SanitizeWindowGeometry(WindowGeometry{Width: 200, Height: 100, X: 10, Y: 10, HasOrigin: true})
	if got.Width != DefaultWindowWidth || got.Height != DefaultWindowHeight {
		t.Fatalf("sanitize size = %dx%d", got.Width, got.Height)
	}
}

func TestFitWindowGeometryToScreensDropsMissingDisplay(t *testing.T) {
	geometry := WindowGeometry{X: 3000, Y: 100, Width: 1200, Height: 800, HasOrigin: true, ScreenID: "gone"}
	screens := []ScreenBounds{{ID: "main", X: 0, Y: 0, Width: 1920, Height: 1080}}
	got := FitWindowGeometryToScreens(geometry, screens)
	if got.HasOrigin || got.ScreenID != "" {
		t.Fatalf("expected origin dropped, got %#v", got)
	}
	if got.Width != 1200 || got.Height != 800 {
		t.Fatalf("size should be kept, got %dx%d", got.Width, got.Height)
	}
}

func TestLoadWindowGeometryMissingFileUsesDefaults(t *testing.T) {
	got := LoadWindowGeometry(filepath.Join(t.TempDir(), "missing.json"))
	if got.Width != DefaultWindowWidth || got.Height != DefaultWindowHeight {
		t.Fatalf("defaults = %#v", got)
	}
	if _, err := os.Stat(filepath.Join(t.TempDir(), "missing.json")); !os.IsNotExist(err) && err != nil {
		t.Fatalf("stat: %v", err)
	}
}
