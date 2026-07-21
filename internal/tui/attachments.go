package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/session"
)

const maxPendingImages = 6

type clipboardImageResultMsg struct {
	attachment session.Attachment
	err        error
	empty      bool
}

func (m *AppModel) attachImagePath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("image path is empty")
	}
	if len(m.pendingImages) >= maxPendingImages {
		return fmt.Errorf("at most %d images can be attached", maxPendingImages)
	}
	importer, ok := m.runtime.(interface {
		ImportImage(string, string) (session.Attachment, error)
	})
	if !ok {
		return fmt.Errorf("image attachments are unavailable in this runtime")
	}
	att, err := importer.ImportImage(m.sessionID, path)
	if err != nil {
		return err
	}
	return m.appendPendingImage(att)
}

func (m *AppModel) attachImageBytes(name, mimeType string, data []byte) error {
	if len(m.pendingImages) >= maxPendingImages {
		return fmt.Errorf("at most %d images can be attached", maxPendingImages)
	}
	importer, ok := m.runtime.(interface {
		ImportImageBytes(string, string, string, []byte) (session.Attachment, error)
	})
	if !ok {
		return fmt.Errorf("image attachments are unavailable in this runtime")
	}
	att, err := importer.ImportImageBytes(m.sessionID, name, mimeType, data)
	if err != nil {
		return err
	}
	return m.appendPendingImage(att)
}

func (m *AppModel) appendPendingImage(att session.Attachment) error {
	for _, existing := range m.pendingImages {
		if existing.Path == att.Path || (existing.Name == att.Name && existing.Size == att.Size && existing.MIME == att.MIME) {
			return fmt.Errorf("image already attached: %s", att.Name)
		}
	}
	m.pendingImages = append(m.pendingImages, att)
	return nil
}

func (m *AppModel) clearPendingImages() {
	m.pendingImages = nil
}

func (m *AppModel) dropLastPendingImage() bool {
	if len(m.pendingImages) == 0 {
		return false
	}
	m.pendingImages = m.pendingImages[:len(m.pendingImages)-1]
	return true
}

func pasteClipboardImage(runtime Runtime, sessionID string) tea.Cmd {
	return func() tea.Msg {
		data, mimeType, err := app.ReadClipboardImage()
		if err != nil {
			return clipboardImageResultMsg{err: err}
		}
		if len(data) == 0 {
			return clipboardImageResultMsg{empty: true}
		}
		importer, ok := runtime.(interface {
			ImportImageBytes(string, string, string, []byte) (session.Attachment, error)
		})
		if !ok {
			return clipboardImageResultMsg{err: fmt.Errorf("image attachments are unavailable in this runtime")}
		}
		name := fmt.Sprintf("clipboard-%s%s", time.Now().Format("150405"), extForMIME(mimeType))
		att, err := importer.ImportImageBytes(sessionID, name, mimeType, data)
		if err != nil {
			return clipboardImageResultMsg{err: err}
		}
		return clipboardImageResultMsg{attachment: att}
	}
}

func extForMIME(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}

func (m AppModel) renderPendingAttachments(width int) string {
	if len(m.pendingImages) == 0 || width <= 0 {
		return ""
	}
	names := make([]string, 0, len(m.pendingImages))
	for _, att := range m.pendingImages {
		name := strings.TrimSpace(att.Name)
		if name == "" {
			name = filepath.Base(att.Path)
		}
		names = append(names, m.theme.Attachment.Render(name))
	}
	label := m.theme.AttachmentTag.Render(fmt.Sprintf("ATTACHMENTS %d/%d", len(m.pendingImages), maxPendingImages))
	content := label + "  " + strings.Join(names, m.theme.MetaDivider.Render("  ·  "))
	hint := m.theme.HelpKey.Render("Esc") + m.theme.HelpDesc.Render(" remove last")
	if ansi.StringWidth(content)+ansi.StringWidth(hint)+4 <= width {
		content = joinSides(content, hint+" ", width)
	}
	return padOrTrim(content, width)
}

func formatUserContent(text string, atts []session.Attachment) string {
	text = strings.TrimSpace(text)
	label := app.FormatAttachmentLabel(atts)
	switch {
	case label == "":
		return text
	case text == "":
		return "📎 " + label
	default:
		return "📎 " + label + "\n" + text
	}
}
