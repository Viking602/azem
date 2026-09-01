package sessionimport

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"path/filepath"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/venat/message"
)

func timestampValue(value any, fallback time.Time) time.Time {
	switch current := value.(type) {
	case json.Number:
		number, _ := current.Float64()
		if number > 0 {
			if number < 10_000_000_000 {
				number *= 1000
			}
			return time.UnixMilli(int64(number)).UTC()
		}
	case float64:
		if current > 0 {
			if current < 10_000_000_000 {
				current *= 1000
			}
			return time.UnixMilli(int64(current)).UTC()
		}
	case string:
		if parsed, err := time.Parse(time.RFC3339Nano, current); err == nil {
			return parsed.UTC()
		}
	}
	return fallback.UTC()
}

func importedBlock(modelMessage message.Message, attachments []session.Attachment, runID string) (session.Block, error) {
	modelMessage.SyncLegacyContent()
	block := session.Block{RunID: runID, State: "completed", Attachments: attachments}
	switch modelMessage.Role {
	case message.RoleUser:
		block.Kind = "user"
		block.Title = "Imported user"
	case message.RoleAssistant:
		block.Kind = "assistant"
		block.Title = "Imported assistant"
	case message.RoleTool:
		block.Kind = "tool"
		block.Title = "Imported tool result"
	default:
		block.Kind = "context"
		block.Title = "Imported context"
	}
	block.Content = cleanText(modelMessage.TextContent())
	block.Thinking = cleanText(modelMessage.ReasoningContent())
	if modelMessage.ToolResult != nil {
		block.Content = cleanText(modelMessage.ToolResult.Content)
		if block.Content == "" {
			parts := make([]string, 0, len(modelMessage.ToolResult.Parts))
			for _, part := range modelMessage.ToolResult.Parts {
				if part.Kind == message.ContentText && cleanText(part.Text) != "" {
					parts = append(parts, cleanText(part.Text))
				}
			}
			block.Content = strings.Join(parts, "\n")
		}
	}
	if block.Content == "" && len(modelMessage.ToolCalls) > 0 {
		names := make([]string, 0, len(modelMessage.ToolCalls))
		for _, call := range modelMessage.ToolCalls {
			names = append(names, call.Name)
		}
		block.Content = "Tool call: " + strings.Join(names, ", ")
	}
	if needsImportedMessage(modelMessage) {
		encoded, err := json.Marshal(agentruntime.PersistMessage(modelMessage))
		if err != nil {
			return session.Block{}, err
		}
		block.ImportedMessage = encoded
	}
	return block, nil
}

func needsImportedMessage(value message.Message) bool {
	if value.Role == message.RoleTool || len(value.ToolCalls) > 0 || value.Thinking != "" || value.RedactedThinking != "" {
		return true
	}
	for _, part := range value.CanonicalContent() {
		if part.Kind != message.ContentText {
			return true
		}
	}
	return value.Role != message.RoleUser && value.Role != message.RoleAssistant
}

func textAndImageParts(content any) (parts []message.ContentPart, images []foreignImage) {
	if text, ok := content.(string); ok {
		if text != "" {
			parts = append(parts, message.TextPart(text))
		}
		return parts, nil
	}
	for _, raw := range arrayValue(content) {
		item := objectValue(raw)
		switch stringValue(item["type"]) {
		case "text", "input_text", "output_text":
			if text := stringValue(item["text"]); text != "" {
				parts = append(parts, message.TextPart(text))
			}
		case "image":
			source := objectValue(item["source"])
			if data, mediaType := stringValue(source["data"]), stringValue(source["media_type"]); data != "" && mediaType != "" {
				images = append(images, foreignImage{MediaType: mediaType, Encoded: data})
			}
		case "input_image":
			if imageURL := stringValue(item["image_url"]); imageURL != "" {
				if image, ok := dataURLImage(imageURL); ok {
					images = append(images, image)
				}
			}
		}
	}
	return parts, images
}

type foreignImage struct {
	MediaType string
	Encoded   string
}

func dataURLImage(value string) (foreignImage, bool) {
	if !strings.HasPrefix(value, "data:") {
		return foreignImage{}, false
	}
	comma := strings.IndexByte(value, ',')
	if comma < 0 || !strings.HasSuffix(value[:comma], ";base64") {
		return foreignImage{}, false
	}
	mediaType := strings.TrimSuffix(strings.TrimPrefix(value[:comma], "data:"), ";base64")
	if mediaType == "" {
		return foreignImage{}, false
	}
	return foreignImage{MediaType: mediaType, Encoded: value[comma+1:]}, true
}

func (importer *Importer) importImages(targetSessionID, sourcePrefix string, images []foreignImage) ([]session.Attachment, []message.ContentPart, error) {
	attachments := make([]session.Attachment, 0, len(images))
	inline := make([]message.ContentPart, 0, len(images))
	for index, image := range images {
		if int64(base64.StdEncoding.DecodedLen(len(image.Encoded))) > maxSessionFileBytes {
			return nil, nil, fmt.Errorf("foreign image %d exceeds import budget", index+1)
		}
		payload, err := base64.StdEncoding.DecodeString(image.Encoded)
		if err != nil {
			return nil, nil, fmt.Errorf("decode foreign image %d: %w", index+1, err)
		}
		if importer.Attachments == nil {
			inline = append(inline, message.ContentPart{Kind: message.ContentImage, Data: payload, MediaType: image.MediaType})
			continue
		}
		extension := ".bin"
		if candidates, _ := mime.ExtensionsByType(image.MediaType); len(candidates) > 0 {
			extension = candidates[0]
		}
		name := fmt.Sprintf("%s-%03d%s", sourcePrefix, index+1, filepath.Ext(extension))
		attachment, err := importer.Attachments.ImportBytes(targetSessionID, name, image.MediaType, payload)
		if err != nil {
			return nil, nil, fmt.Errorf("persist foreign image %d: %w", index+1, err)
		}
		attachments = append(attachments, attachment)
	}
	return attachments, inline, nil
}

func sourceTitle(title, firstMessage string, source Source) string {
	title = cleanText(title)
	if title == "" {
		title = cleanText(firstMessage)
	}
	if title == "" {
		sourceName := "Claude"
		if source == SourceCodex {
			sourceName = "Codex"
		}
		title = fmt.Sprintf("Imported %s session", sourceName)
	}
	runes := []rune(title)
	if len(runes) > 200 {
		title = string(runes[:200])
	}
	return title
}
