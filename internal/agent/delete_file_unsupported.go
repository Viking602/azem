//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package agent

import "fmt"

const secureDeleteSupported = false

func deleteRegularFile(_, _ string) (int64, error) {
	return 0, fmt.Errorf("secure workspace deletion is unavailable on this platform")
}
