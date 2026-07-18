package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
)

type Theme struct {
	Header        lipgloss.Style
	Border        lipgloss.Style
	User          lipgloss.Style
	UserAccent    lipgloss.Style
	Assistant     lipgloss.Style
	Thinking      lipgloss.Style
	Tool          lipgloss.Style
	Diff          lipgloss.Style
	DiffAdd       lipgloss.Style
	DiffDel       lipgloss.Style
	Error         lipgloss.Style
	Muted         lipgloss.Style
	Status        lipgloss.Style
	Success       lipgloss.Style
	Warning       lipgloss.Style
	ApprovalAsk   lipgloss.Style
	ApprovalSmart lipgloss.Style
	FullAccess    lipgloss.Style
	Cursor        lipgloss.Style
	Selected      lipgloss.Style
}

func DefaultTheme() Theme {
	text := adaptiveColor("#20231f", "235", "0", "#d8ddd7", "252", "7")
	muted := adaptiveColor("#616861", "241", "0", "#808880", "244", "7")
	accent := adaptiveColor("#285f50", "23", "6", "#8fb9a8", "108", "6")
	secondary := adaptiveColor("#4a5965", "240", "4", "#9aabb8", "110", "4")
	warning := adaptiveColor("#805719", "94", "3", "#d5a65b", "179", "3")
	danger := adaptiveColor("#8a3434", "124", "1", "#d67b78", "174", "1")
	blue := adaptiveColor("#285f8a", "25", "4", "#79b8e8", "110", "4")
	cursor := adaptiveColor("#6d4aff", "99", "5", "#a78bfa", "141", "5")
	success := adaptiveColor("#3d6c31", "22", "2", "#91b477", "107", "2")
	selection := adaptiveColor("#dce9e3", "254", "7", "#27332d", "236", "0")
	userAccent := adaptiveColor("#176f5b", "29", "6", "#62d6b5", "79", "6")

	return Theme{
		Header:        lipgloss.NewStyle().Bold(true).Foreground(accent),
		Border:        lipgloss.NewStyle().Foreground(muted),
		User:          lipgloss.NewStyle().Foreground(userAccent),
		UserAccent:    lipgloss.NewStyle().Bold(true).Foreground(userAccent),
		Assistant:     lipgloss.NewStyle().Foreground(text),
		Thinking:      lipgloss.NewStyle().Foreground(secondary),
		Tool:          lipgloss.NewStyle().Foreground(warning),
		Diff:          lipgloss.NewStyle().Foreground(accent),
		DiffAdd:       lipgloss.NewStyle().Foreground(success),
		DiffDel:       lipgloss.NewStyle().Foreground(danger),
		Error:         lipgloss.NewStyle().Bold(true).Foreground(danger),
		Muted:         lipgloss.NewStyle().Foreground(muted),
		Status:        lipgloss.NewStyle().Bold(true).Foreground(accent),
		Success:       lipgloss.NewStyle().Foreground(success),
		Warning:       lipgloss.NewStyle().Foreground(warning),
		ApprovalAsk:   lipgloss.NewStyle().Bold(true).Foreground(warning),
		ApprovalSmart: lipgloss.NewStyle().Bold(true).Foreground(blue),
		FullAccess:    lipgloss.NewStyle().Bold(true).Foreground(danger),
		Cursor:        lipgloss.NewStyle().Foreground(cursor),
		Selected:      lipgloss.NewStyle().Bold(true).Foreground(text).Background(selection),
	}
}

func adaptiveColor(lightTrueColor string, lightANSI256 string, lightANSI string, darkTrueColor string, darkANSI256 string, darkANSI string) color.Color {
	return compat.CompleteAdaptiveColor{
		Light: compat.CompleteColor{
			TrueColor: lipgloss.Color(lightTrueColor), ANSI256: lipgloss.Color(lightANSI256), ANSI: lipgloss.Color(lightANSI),
		},
		Dark: compat.CompleteColor{
			TrueColor: lipgloss.Color(darkTrueColor), ANSI256: lipgloss.Color(darkANSI256), ANSI: lipgloss.Color(darkANSI),
		},
	}
}
