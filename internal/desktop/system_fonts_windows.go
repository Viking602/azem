//go:build windows

package desktop

import (
	"context"
	"os"
	"os/exec"
	"time"
)

const windowsSystemFontScript = `
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
Add-Type -AssemblyName System.Drawing
$language = $env:AZEM_UI_LANGUAGE
$culture = [Globalization.CultureInfo]::GetCultureInfo($language)
$separator = [char]31
$recordSeparator = [char]30
$fonts = [Drawing.Text.InstalledFontCollection]::new()
$records = foreach ($family in $fonts.Families) {
  $label = try { $family.GetName($culture.LCID) } catch { $family.Name }
  $family.Name + $separator + $label + $separator + $language
}
[Console]::Write(($records -join $recordSeparator))
`

func systemFontFamilies(language string) []SystemFont {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", windowsSystemFontScript)
	command.Env = append(os.Environ(), "AZEM_UI_LANGUAGE="+language)
	output, err := command.Output()
	if err != nil {
		return nil
	}
	return normalizeSystemFontFamilies(string(output), language)
}
