//go:build windows

package evidence

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func openWorkspaceFile(root, relative string) (*os.File, error) {
	absolute, err := validateWindowsPath(root, relative)
	if err != nil {
		return nil, err
	}
	handle, err := openWindowsReadHandle(absolute)
	if err != nil {
		return nil, err
	}
	if err := validateWindowsHandle(root, handle); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	return os.NewFile(uintptr(handle), absolute), nil
}

func validateWindowsPath(root, relative string) (string, error) {
	converted := filepath.FromSlash(relative)
	current := root
	for _, part := range strings.Split(converted, string(filepath.Separator)) {
		if part == "" {
			return "", fmt.Errorf("workspace path is empty")
		}
		current = filepath.Join(current, part)
		if err := rejectWindowsReparsePoint(current); err != nil {
			return "", err
		}
	}
	return filepath.Join(root, converted), nil
}

func rejectWindowsReparsePoint(path string) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes, err := windows.GetFileAttributes(name)
	if err != nil {
		return err
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("workspace path contains a reparse point")
	}
	return nil
}

func openWindowsReadHandle(path string) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(
		name,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
}

func validateWindowsHandle(root string, handle windows.Handle) error {
	finalPath, err := windowsFinalPath(handle)
	if err != nil {
		return err
	}
	if !windowsPathWithinRoot(root, finalPath) {
		return fmt.Errorf("workspace path resolved outside root")
	}
	return nil
}

func windowsFinalPath(handle windows.Handle) (string, error) {
	buffer := make([]uint16, 512)
	for {
		length, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
		if length < uint32(len(buffer)) {
			return cleanWindowsFinalPath(windows.UTF16ToString(buffer[:length])), nil
		}
		buffer = make([]uint16, length+1)
	}
}

func cleanWindowsFinalPath(path string) string {
	if strings.HasPrefix(path, `\\?\UNC\`) {
		return `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	}
	return strings.TrimPrefix(path, `\\?\`)
}

func windowsPathWithinRoot(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
