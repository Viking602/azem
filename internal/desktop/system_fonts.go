package desktop

import (
	"sort"
	"strings"
)

type SystemFont struct {
	Family string `json:"family"`
	Label  string `json:"label"`
}

// SystemFonts returns the font families installed on the host operating system.
func (b *Bridge) SystemFonts(language string) []SystemFont {
	return systemFontFamilies(language)
}

func normalizeSystemFontFamilies(raw, language string) []SystemFont {
	seen := make(map[string]struct{})
	fonts := make([]SystemFont, 0)
	wantedLanguage := strings.ToLower(strings.SplitN(language, "-", 2)[0])
	for _, record := range strings.Split(raw, "\x1e") {
		fields := strings.Split(record, "\x1f")
		family := strings.TrimSpace(fields[0])
		if family == "" || strings.HasPrefix(family, ".") {
			continue
		}
		key := strings.ToLower(family)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		label := family
		if len(fields) >= 3 && strings.HasPrefix(strings.ToLower(fields[2]), wantedLanguage) && strings.TrimSpace(fields[1]) != "" {
			label = strings.TrimSpace(fields[1])
		}
		fonts = append(fonts, SystemFont{Family: family, Label: label})
	}
	sort.Slice(fonts, func(i, j int) bool {
		return strings.ToLower(fonts[i].Label) < strings.ToLower(fonts[j].Label)
	})
	return fonts
}

func normalizeFontconfigFamilies(raw, language string) []SystemFont {
	var normalized strings.Builder
	wanted := strings.ToLower(strings.ReplaceAll(language, "_", "-"))
	wantedBase := strings.SplitN(wanted, "-", 2)[0]
	for _, record := range strings.Split(raw, "\x1e") {
		family, label, fallbackLabel := "", "", ""
		familyIsEnglish := false
		for _, pair := range strings.Split(record, "\x1d") {
			fields := strings.SplitN(pair, "\x1f", 2)
			if len(fields) != 2 || strings.TrimSpace(fields[0]) == "" {
				continue
			}
			name := strings.TrimSpace(fields[0])
			fontLanguage := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(fields[1]), "_", "-"))
			if family == "" || (!familyIsEnglish && strings.HasPrefix(fontLanguage, "en")) {
				family = name
				familyIsEnglish = strings.HasPrefix(fontLanguage, "en")
			}
			if fontLanguage == wanted {
				label = name
			} else if fallbackLabel == "" && strings.SplitN(fontLanguage, "-", 2)[0] == wantedBase {
				fallbackLabel = name
			}
		}
		if family == "" {
			continue
		}
		if label == "" {
			label = fallbackLabel
		}
		if label == "" {
			label = family
		}
		normalized.WriteString(family)
		normalized.WriteByte('\x1f')
		normalized.WriteString(label)
		normalized.WriteByte('\x1f')
		normalized.WriteString(language)
		normalized.WriteByte('\x1e')
	}
	return normalizeSystemFontFamilies(normalized.String(), language)
}
