//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package agent

import (
	"fmt"

	"golang.org/x/sys/unix"
)

const secureDeleteSupported = true

func deleteRegularFile(root, rel string) (int64, error) {
	resolvedRoot, parts, err := cleanDeletePath(root, rel)
	if err != nil {
		return 0, err
	}
	fd, err := unix.Open(resolvedRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return 0, fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()

	for _, part := range parts[:len(parts)-1] {
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return 0, fmt.Errorf("open workspace path component %q without symlinks: %w", part, err)
		}
		if err := unix.Close(fd); err != nil {
			unix.Close(next)
			return 0, fmt.Errorf("close workspace path component: %w", err)
		}
		fd = next
	}

	name := parts[len(parts)-1]
	var stat unix.Stat_t
	if err := unix.Fstatat(fd, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return 0, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return 0, fmt.Errorf("Path is not a file: %s", rel)
	}
	if err := unix.Unlinkat(fd, name, 0); err != nil {
		return 0, err
	}
	return stat.Size, nil
}
