//go:build darwin

package desktop

import (
	"context"
	"os/exec"
	"time"
)

const systemFontScript = `
ObjC.import("AppKit");
const manager = $.NSFontManager.sharedFontManager;
const families = ObjC.deepUnwrap(manager.availableFontFamilies);
const languages = ObjC.deepUnwrap($.NSUserDefaults.standardUserDefaults.objectForKey("AppleLanguages")) || [];
const language = languages[0] || "";
families.map(family => [
  family,
  ObjC.unwrap(manager.localizedNameForFamilyFace($(family), undefined)) || family,
  language,
].join("\x1f")).join("\x1e");
`

func systemFontFamilies(language string) []SystemFont {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", systemFontScript).Output()
	if err != nil {
		return nil
	}
	return normalizeSystemFontFamilies(string(output), language)
}
