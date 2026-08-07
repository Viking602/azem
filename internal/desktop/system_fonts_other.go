//go:build !darwin && !linux && !windows

package desktop

func systemFontFamilies(string) []SystemFont {
	return nil
}
