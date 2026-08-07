//go:build linux

package desktop

import (
	"context"
	"os/exec"
	"time"
)

const fontconfigFormat = "%{[]family,familylang{%{family}\x1f%{familylang}\x1d}}\x1e"

func systemFontFamilies(language string) []SystemFont {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "fc-list", "--format", fontconfigFormat).Output()
	if err != nil {
		return nil
	}
	return normalizeFontconfigFamilies(string(output), language)
}
