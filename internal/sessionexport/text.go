package sessionexport

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/Viking602/azem/internal/session"
)

func writeText(ctx context.Context, output io.Writer, snapshot session.ExportSnapshot, options Options) error {
	writer := bufio.NewWriter(output)
	write := func(format string, values ...any) error {
		_, err := fmt.Fprintf(writer, format, values...)
		return err
	}
	if err := write("# %s\n\n", snapshot.Session.Title); err != nil {
		return err
	}
	if err := write("Session: %s\n", snapshot.Session.ID); err != nil {
		return err
	}
	if snapshot.Session.Workspace != "" {
		if err := write("Workspace: %s\n", snapshot.Session.Workspace); err != nil {
			return err
		}
	}
	if err := write("Branch: %s\n", snapshot.Tree.ActiveBranch); err != nil {
		return err
	}
	if snapshot.Tree.SourceKind != "native" {
		if err := write("Source: %s\n", snapshot.Tree.SourceKind); err != nil {
			return err
		}
	}
	if err := write("\n"); err != nil {
		return err
	}
	labels := treeLabels(snapshot.Tree.Roots)
	for _, block := range exportedBlocks(snapshot, options) {
		if err := ctx.Err(); err != nil {
			return err
		}
		heading := blockHeading(block)
		if label := labels[block.Sequence]; label != "" {
			heading += " — " + label
		}
		if err := write("## %s\n\n", heading); err != nil {
			return err
		}
		if content := strings.TrimSpace(block.Content); content != "" {
			if err := write("%s\n\n", content); err != nil {
				return err
			}
		}
		for _, attachment := range block.Attachments {
			if err := write("[Attachment: %s · %s · %d bytes]\n", firstNonempty(attachment.Name, attachment.ID, "unnamed"), firstNonempty(attachment.MIME, "unknown"), attachment.Size); err != nil {
				return err
			}
		}
		if len(block.Attachments) > 0 {
			if err := write("\n"); err != nil {
				return err
			}
		}
	}
	tools := exportedTools(snapshot, options)
	if len(tools) > 0 {
		if err := write("# Tool activity\n\n"); err != nil {
			return err
		}
	}
	for _, record := range tools {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := write("## %s · %s\n\n", record.Name, record.State); err != nil {
			return err
		}
		if len(record.Arguments) > 0 {
			if err := write("Arguments:\n%s\n\n", record.Arguments); err != nil {
				return err
			}
		}
		if strings.TrimSpace(record.Content) != "" {
			if err := write("%s\n\n", record.Content); err != nil {
				return err
			}
		}
	}
	return writer.Flush()
}

func blockHeading(block session.Block) string {
	switch block.Kind {
	case "user":
		return "User"
	case "assistant":
		return "Assistant"
	case "tool":
		return "Tool result"
	case "model_change":
		return "Model change"
	default:
		if strings.TrimSpace(block.Title) != "" {
			return block.Title
		}
		return humanizeKind(block.Kind)
	}
}

func humanizeKind(kind string) string {
	kind = strings.ReplaceAll(strings.TrimSpace(kind), "_", " ")
	runes := []rune(kind)
	if len(runes) == 0 {
		return "Entry"
	}
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func treeLabels(roots []session.TreeNode) map[int64]string {
	labels := make(map[int64]string)
	var visit func([]session.TreeNode)
	visit = func(nodes []session.TreeNode) {
		for _, node := range nodes {
			if node.Entry.Label != "" {
				labels[node.Entry.Sequence] = node.Entry.Label
			}
			visit(node.Children)
		}
	}
	visit(roots)
	return labels
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
