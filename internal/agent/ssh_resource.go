package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Viking602/azem/internal/resource"
)

type sshResourceHandler struct {
	bridge        *lspBridgeRuntime
	networkPolicy string
}

type sshBridgeDetails struct {
	Resource struct {
		URL         string `json:"url"`
		Content     string `json:"content"`
		ContentType string `json:"contentType"`
		Size        int    `json:"size"`
		Immutable   bool   `json:"immutable"`
		IsDirectory bool   `json:"isDirectory"`
	} `json:"resource"`
}

func newSSHResourceHandler(bridge *lspBridgeRuntime, networkPolicy string) *sshResourceHandler {
	if bridge == nil {
		bridge = newLSPBridgeRuntime()
	}
	return &sshResourceHandler{bridge: bridge, networkPolicy: networkPolicy}
}

func (handler *sshResourceHandler) Read(ctx context.Context, request resource.Request) (resource.Result, error) {
	if handler.networkPolicy == "deny" {
		return resource.Result{}, errors.New("SSH access is denied by workspace network policy")
	}
	response, err := handler.bridge.requestSSH(ctx, request.Scope.Workspace, request.Scope.SessionID, map[string]any{"action": "read", "uri": request.URI.Raw})
	if err != nil {
		return resource.Result{}, err
	}
	var details sshBridgeDetails
	if json.Unmarshal(response.Details, &details) != nil {
		details.Resource.URL = request.URI.Raw
		details.Resource.Content = response.Content
		details.Resource.ContentType = "text/plain"
		details.Resource.Size = len(response.Content)
	}
	content := details.Resource.Content
	if request.Selector != "" {
		content = applyTextSelector(content, ompReadInput{Selector: request.Selector})
	}
	metadata := map[string]string{}
	if details.Resource.IsDirectory {
		metadata["directory"] = "true"
	}
	if details.Resource.Immutable {
		metadata["immutable"] = "true"
	}
	return resource.Result{
		URI: firstString(details.Resource.URL, request.URI.Raw), MediaType: firstString(details.Resource.ContentType, "text/plain"),
		Data: []byte(content), Metadata: metadata,
	}, nil
}

func (handler *sshResourceHandler) Write(ctx context.Context, request resource.Request, value resource.Result) (resource.Result, error) {
	if handler.networkPolicy == "deny" {
		return resource.Result{}, errors.New("SSH access is denied by workspace network policy")
	}
	if request.Selector != "" && request.Selector != "raw" {
		return resource.Result{}, errors.New("SSH writes do not accept line selectors")
	}
	response, err := handler.bridge.requestSSH(ctx, request.Scope.Workspace, request.Scope.SessionID, map[string]any{
		"action": "write", "uri": request.URI.Raw, "content": string(value.Data),
	})
	if err != nil {
		return resource.Result{}, err
	}
	return resource.Result{URI: request.URI.Raw, MediaType: firstString(value.MediaType, "text/plain"), Data: []byte(strings.TrimSpace(response.Content)), Metadata: map[string]string{"written": "true"}}, nil
}

func firstString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
