//go:build unix

package skills

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func writeManagedFileNoFollow(path string, content []byte, expected os.FileInfo) error {
	stat, ok := expected.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 {
		return fmt.Errorf("managed skill file has unsafe hard links")
	}
	descriptor, err := unix.Open(path, unix.O_WRONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	handle := os.NewFile(uintptr(descriptor), path)
	if handle == nil {
		_ = unix.Close(descriptor)
		return fmt.Errorf("open managed skill file")
	}
	defer handle.Close()
	opened, err := handle.Stat()
	if err != nil {
		return err
	}
	openedStat, ok := opened.Sys().(*syscall.Stat_t)
	if !ok || openedStat.Nlink != 1 || !os.SameFile(expected, opened) {
		return fmt.Errorf("managed skill file changed during update")
	}
	if err := handle.Truncate(0); err != nil {
		return err
	}
	if _, err := handle.WriteAt(content, 0); err != nil {
		return err
	}
	return handle.Sync()
}
