//go:build !unix && !windows

package skills

import (
	"fmt"
	"os"
)

func writeManagedFileNoFollow(string, []byte, os.FileInfo) error {
	return fmt.Errorf("managed skill updates are unsupported on this platform")
}
