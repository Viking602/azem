//go:build windows

package skills

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func writeManagedFileNoFollow(path string, content []byte, expected os.FileInfo) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_WRITE|windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return fmt.Errorf("open managed skill file")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		return err
	}
	if information.NumberOfLinks != 1 || !os.SameFile(expected, opened) || opened.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("managed skill file changed during update")
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.WriteAt(content, 0); err != nil {
		return err
	}
	return file.Sync()
}
