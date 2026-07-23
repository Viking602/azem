package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
)

type Theme struct {
	Header        lipgloss.Style
	HeaderBrand   lipgloss.Style
	HeaderMode    lipgloss.Style
	Chrome        lipgloss.Style
	RuntimeStrip  lipgloss.Style
	ContextStrip  lipgloss.Style
	HelpStrip     lipgloss.Style
	Border        lipgloss.Style
	User          lipgloss.Style
	UserAccent    lipgloss.Style
	Assistant     lipgloss.Style
	Thinking      lipgloss.Style
	Tool          lipgloss.Style
	Diff          lipgloss.Style
	DiffAdd       lipgloss.Style
	DiffDel       lipgloss.Style
	DiffHunk      lipgloss.Style
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
	PanelFocused  lipgloss.Style
	PanelBlurred  lipgloss.Style
	OverlayTitle  lipgloss.Style
	OverlayGroup  lipgloss.Style
	OverlayFooter lipgloss.Style
	BlockRail     lipgloss.Style
	MetaLabel     lipgloss.Style
	MetaValue     lipgloss.Style
	MetaDivider   lipgloss.Style
	HelpKey       lipgloss.Style
	HelpDesc      lipgloss.Style
	Chip          lipgloss.Style
	ChipAsk       lipgloss.Style
	ChipSmart     lipgloss.Style
	ChipDanger    lipgloss.Style
	BarFilled     lipgloss.Style
	BarEmpty      lipgloss.Style
	ScrollTrack   lipgloss.Style
	ScrollThumb   lipgloss.Style
	UserSurface   lipgloss.Style
	AssistantTag  lipgloss.Style
	ThinkingTag   lipgloss.Style
	ToolTag       lipgloss.Style
	ToolRead      lipgloss.Style
	ToolSearch    lipgloss.Style
	ToolWrite     lipgloss.Style
	ToolExecute   lipgloss.Style
	ToolMemory    lipgloss.Style
	ToolAgent     lipgloss.Style
	CodeKeyword   lipgloss.Style
	CodeString    lipgloss.Style
	CodeNumber    lipgloss.Style
	CodeComment   lipgloss.Style
	CodeName      lipgloss.Style
	CodeOperator  lipgloss.Style
	AgentTag      lipgloss.Style
	ApprovalTag   lipgloss.Style
	ErrorTag      lipgloss.Style
	HookTag       lipgloss.Style
	RailTitle     lipgloss.Style
	RailTodo      lipgloss.Style
	RailAgents    lipgloss.Style
	RailMCP       lipgloss.Style
	AttachmentTag lipgloss.Style
	Attachment    lipgloss.Style
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
	diffAddBackground := adaptiveColor("#e6f4ea", "194", "7", "#183325", "22", "0")
	diffDelBackground := adaptiveColor("#fce8e8", "224", "7", "#3a2023", "52", "0")
	diffHunkBackground := adaptiveColor("#e7f1f5", "195", "7", "#1d3037", "236", "0")
	border := adaptiveColor("#c5cdc6", "250", "7", "#3a433c", "238", "0")
	focusBorder := adaptiveColor("#4f8f78", "66", "6", "#6fa892", "108", "6")
	surfaceAccent := adaptiveColor("#285f50", "23", "0", "#67d4ee", "81", "6")
	chromeBackground := adaptiveColor("#edf2f0", "255", "7", "#101820", "234", "0")
	runtimeBackground := adaptiveColor("#f3f6f4", "255", "7", "#151e27", "235", "0")
	contextBackground := adaptiveColor("#f7f9f8", "255", "7", "#121a22", "234", "0")
	helpBackground := adaptiveColor("#fafbfa", "255", "7", "#0f161d", "233", "0")
	surfaceBackground := adaptiveColor("#f7f9f8", "255", "7", "#151e27", "235", "0")
	raisedBackground := adaptiveColor("#ffffff", "231", "7", "#1a2530", "236", "0")
	overlayTitleBackground := adaptiveColor("#e7efeb", "254", "7", "#1a2927", "235", "0")
	overlayFooterBackground := adaptiveColor("#f0f4f2", "255", "7", "#121b23", "234", "0")
	cyan := adaptiveColor("#087f9c", "30", "6", "#67d4ee", "81", "6")
	violet := adaptiveColor("#6754b8", "61", "5", "#b4a7ff", "147", "5")
	// Transcript accents stay background-free. Distinction comes from restrained
	// foreground hues so the conversation keeps its original dark, quiet tone.
	chipBg := adaptiveColor("#e7eee9", "254", "7", "#222a25", "235", "0")
	chipAskBg := adaptiveColor("#f5edd8", "230", "7", "#2f2818", "236", "0")
	chipSmartBg := adaptiveColor("#e2eef7", "195", "7", "#1b2a35", "236", "0")
	chipDangerBg := adaptiveColor("#f8e4e4", "224", "7", "#331f21", "236", "0")
	panelBase := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1)
	chipBase := lipgloss.NewStyle().Padding(0, 1).Bold(true)
	messageTag := lipgloss.NewStyle().Bold(true)

	return Theme{
		Header:        lipgloss.NewStyle().Bold(true).Foreground(accent),
		HeaderBrand:   lipgloss.NewStyle().Bold(true).Foreground(surfaceAccent).Background(chromeBackground),
		HeaderMode:    lipgloss.NewStyle().Bold(true).Foreground(text).Background(chipBg).Padding(0, 1),
		Chrome:        lipgloss.NewStyle().Background(chromeBackground),
		RuntimeStrip:  lipgloss.NewStyle().Background(runtimeBackground),
		ContextStrip:  lipgloss.NewStyle().Background(contextBackground),
		HelpStrip:     lipgloss.NewStyle().Background(helpBackground),
		Border:        lipgloss.NewStyle().Foreground(border),
		User:          lipgloss.NewStyle().Foreground(userAccent),
		UserAccent:    lipgloss.NewStyle().Bold(true).Foreground(userAccent),
		Assistant:     lipgloss.NewStyle().Foreground(text),
		Thinking:      lipgloss.NewStyle().Foreground(secondary),
		Tool:          lipgloss.NewStyle().Foreground(warning),
		Diff:          lipgloss.NewStyle().Foreground(accent),
		DiffAdd:       lipgloss.NewStyle().Foreground(success).Background(diffAddBackground),
		DiffDel:       lipgloss.NewStyle().Foreground(danger).Background(diffDelBackground),
		DiffHunk:      lipgloss.NewStyle().Bold(true).Foreground(accent).Background(diffHunkBackground),
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
		PanelFocused:  panelBase.BorderForeground(focusBorder).Background(raisedBackground),
		PanelBlurred:  panelBase.BorderForeground(border).Background(surfaceBackground),
		OverlayTitle:  lipgloss.NewStyle().Bold(true).Foreground(surfaceAccent).Background(overlayTitleBackground),
		OverlayGroup:  lipgloss.NewStyle().Bold(true).Foreground(secondary),
		OverlayFooter: lipgloss.NewStyle().Foreground(muted).Background(overlayFooterBackground),
		BlockRail:     lipgloss.NewStyle().Foreground(border),
		MetaLabel:     lipgloss.NewStyle().Bold(true).Foreground(accent),
		MetaValue:     lipgloss.NewStyle().Foreground(text),
		MetaDivider:   lipgloss.NewStyle().Foreground(border),
		HelpKey:       lipgloss.NewStyle().Bold(true).Foreground(secondary),
		HelpDesc:      lipgloss.NewStyle().Foreground(muted),
		Chip:          chipBase.Foreground(text).Background(chipBg),
		ChipAsk:       chipBase.Foreground(warning).Background(chipAskBg),
		ChipSmart:     chipBase.Foreground(blue).Background(chipSmartBg),
		ChipDanger:    chipBase.Foreground(danger).Background(chipDangerBg),
		BarFilled:     lipgloss.NewStyle().Foreground(accent),
		BarEmpty:      lipgloss.NewStyle().Foreground(border),
		ScrollTrack:   lipgloss.NewStyle().Foreground(border),
		ScrollThumb:   lipgloss.NewStyle().Foreground(accent),
		UserSurface:   lipgloss.NewStyle().Foreground(userAccent),
		AssistantTag:  messageTag.Foreground(cyan),
		ThinkingTag:   messageTag.Foreground(violet),
		ToolTag:       messageTag.Foreground(warning),
		ToolRead:      lipgloss.NewStyle().Foreground(blue),
		ToolSearch:    lipgloss.NewStyle().Foreground(violet),
		ToolWrite:     lipgloss.NewStyle().Foreground(accent),
		ToolExecute:   lipgloss.NewStyle().Foreground(warning),
		ToolMemory:    lipgloss.NewStyle().Foreground(cyan),
		ToolAgent:     lipgloss.NewStyle().Foreground(secondary),
		CodeKeyword:   lipgloss.NewStyle().Bold(true).Foreground(violet),
		CodeString:    lipgloss.NewStyle().Foreground(success),
		CodeNumber:    lipgloss.NewStyle().Foreground(warning),
		CodeComment:   lipgloss.NewStyle().Italic(true).Foreground(muted),
		CodeName:      lipgloss.NewStyle().Foreground(cyan),
		CodeOperator:  lipgloss.NewStyle().Foreground(blue),
		AgentTag:      messageTag.Foreground(blue),
		ApprovalTag:   messageTag.Foreground(accent),
		ErrorTag:      messageTag.Foreground(danger),
		HookTag:       messageTag.Foreground(secondary),
		RailTitle:     lipgloss.NewStyle().Bold(true).Foreground(violet),
		RailTodo:      lipgloss.NewStyle().Bold(true).Foreground(warning),
		RailAgents:    lipgloss.NewStyle().Bold(true).Foreground(blue),
		RailMCP:       lipgloss.NewStyle().Bold(true).Foreground(accent),
		AttachmentTag: messageTag.Foreground(violet),
		Attachment:    lipgloss.NewStyle().Foreground(cyan),
	}
}

// renderSurface preserves a surface background across nested Lip Gloss spans.
// Child styles emit a full ANSI reset, so an ordinary outer Style.Render would
// leave the rest of the row on the terminal's default background.
func renderSurface(style lipgloss.Style, content string) string {
	opener := surfaceBackgroundOpener(style)
	if opener != "" {
		content = strings.ReplaceAll(content, "\x1b[0m", "\x1b[0m"+opener)
		content = strings.ReplaceAll(content, "\x1b[m", "\x1b[m"+opener)
	}
	return style.Render(content)
}

func surfaceBackgroundOpener(style lipgloss.Style) string {
	background := style.GetBackground()
	if background == nil {
		return ""
	}
	rendered := lipgloss.NewStyle().Background(background).Render(" ")
	opener, _, found := strings.Cut(rendered, " ")
	if !found {
		return ""
	}
	return opener
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
