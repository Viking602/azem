package app

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// ReadClipboardImage returns image bytes and MIME type from the system clipboard.
// It returns (nil, "", nil) when the clipboard has no image data.
func ReadClipboardImage() (data []byte, mimeType string, err error) {
	switch runtime.GOOS {
	case "darwin":
		return readClipboardImageDarwin()
	case "linux":
		return readClipboardImageLinux()
	case "windows":
		return readClipboardImageWindows()
	default:
		return nil, "", fmt.Errorf("clipboard image paste is not supported on %s", runtime.GOOS)
	}
}

func readClipboardImageDarwin() ([]byte, string, error) {
	temporary, err := os.CreateTemp("", "azem-clipboard-image-*")
	if err != nil {
		return nil, "", fmt.Errorf("create macOS clipboard temporary file: %w", err)
	}
	path := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(path)
		return nil, "", err
	}
	defer os.Remove(path)
	// Prefer PNG; fall back to TIFF/JPEG class names used by macOS clipboard.
	script := `
on run argv
  set outPath to item 1 of argv
  try
    set img to the clipboard as «class PNGf»
    set mimeType to "image/png"
  on error
    try
      set img to the clipboard as JPEG picture
      set mimeType to "image/jpeg"
    on error
      try
        set img to the clipboard as «class TIFF»
        set mimeType to "image/tiff"
      on error
        return "NOIMAGE"
      end try
    end try
  end try
  set f to open for access POSIX file outPath with write permission
  set eof f to 0
  write img to f
  close access f
  return mimeType
end run
`
	out, err := exec.Command("osascript", "-e", script, path).CombinedOutput()
	result := strings.TrimSpace(string(out))
	if err != nil {
		if strings.Contains(result, "NOIMAGE") || result == "NOIMAGE" {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("read macOS clipboard image: %w (%s)", err, result)
	}
	if result == "NOIMAGE" || result == "" {
		return nil, "", nil
	}
	mimeType := result
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		return nil, "", readErr
	}
	if mimeType == "image/tiff" {
		// Keep TIFF bytes; DetectContentType/ImportBytes may reject. Convert via sips if available.
		if converted, convErr := convertImageWithSips(data, "png"); convErr == nil {
			return converted, "image/png", nil
		}
		return nil, "", fmt.Errorf("clipboard has TIFF image; convert to PNG failed")
	}
	if len(data) == 0 {
		return nil, "", nil
	}
	return data, mimeType, nil
}

func convertImageWithSips(data []byte, format string) ([]byte, error) {
	in, err := os.CreateTemp("", "azem-clip-in-*")
	if err != nil {
		return nil, err
	}
	inPath := in.Name()
	_ = in.Close()
	defer os.Remove(inPath)
	if err := os.WriteFile(inPath, data, 0o600); err != nil {
		return nil, err
	}
	out, err := os.CreateTemp("", "azem-clip-out-*.png")
	if err != nil {
		return nil, err
	}
	outPath := out.Name()
	_ = out.Close()
	defer os.Remove(outPath)
	cmd := exec.Command("sips", "-s", "format", format, inPath, "--out", outPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("sips: %w (%s)", err, bytes.TrimSpace(output))
	}
	return os.ReadFile(outPath)
}

func readClipboardImageLinux() ([]byte, string, error) {
	// Wayland first, then X11.
	candidates := []struct {
		mime string
		cmd  []string
	}{
		{"image/png", []string{"wl-paste", "-t", "image/png"}},
		{"image/jpeg", []string{"wl-paste", "-t", "image/jpeg"}},
		{"image/png", []string{"xclip", "-selection", "clipboard", "-t", "image/png", "-o"}},
		{"image/jpeg", []string{"xclip", "-selection", "clipboard", "-t", "image/jpeg", "-o"}},
	}
	var lastErr error
	for _, candidate := range candidates {
		if _, err := exec.LookPath(candidate.cmd[0]); err != nil {
			continue
		}
		out, err := exec.Command(candidate.cmd[0], candidate.cmd[1:]...).Output()
		if err != nil {
			lastErr = err
			continue
		}
		if len(out) > 0 {
			return out, candidate.mime, nil
		}
	}
	if lastErr != nil {
		// No image is common; treat as empty rather than hard failure when tools exist but types missing.
		return nil, "", nil
	}
	return nil, "", fmt.Errorf("install wl-paste or xclip to paste clipboard images")
}

func readClipboardImageWindows() ([]byte, string, error) {
	script := `
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
if (-not [System.Windows.Forms.Clipboard]::ContainsImage()) { exit 2 }
$img = [System.Windows.Forms.Clipboard]::GetImage()
$path = Join-Path $env:TEMP ("azem-clipboard-" + [guid]::NewGuid().ToString() + ".png")
$img.Save($path, [System.Drawing.Imaging.ImageFormat]::Png)
Write-Output $path
`
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	out, err := cmd.CombinedOutput()
	result := strings.TrimSpace(string(out))
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 2 {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("read Windows clipboard image: %w (%s)", err, result)
	}
	if result == "" {
		return nil, "", nil
	}
	data, readErr := os.ReadFile(result)
	_ = os.Remove(result)
	if readErr != nil {
		return nil, "", readErr
	}
	if len(data) == 0 {
		return nil, "", nil
	}
	return data, "image/png", nil
}
