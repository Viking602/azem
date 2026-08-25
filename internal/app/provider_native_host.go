package app

import (
	"context"
	"errors"
	"strings"

	"github.com/Viking602/venat/message"
)

type attachmentRequestHost struct {
	root string
}

func newAttachmentRequestHost(host providerHost) *attachmentRequestHost {
	if host == nil || strings.TrimSpace(host.AttachmentRoot()) == "" {
		return nil
	}
	return &attachmentRequestHost{root: strings.TrimSpace(host.AttachmentRoot())}
}

func newAttachmentRequestHostRoot(root string) *attachmentRequestHost {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	return &attachmentRequestHost{root: root}
}

func (host *attachmentRequestHost) AttachmentRoot() string {
	if host == nil {
		return ""
	}
	return host.root
}

func (*attachmentRequestHost) ExecuteNativeTool(context.Context, message.ToolCall) (message.ToolResult, error) {
	return message.ToolResult{}, errors.New("provider-native tools are unavailable on this request")
}
