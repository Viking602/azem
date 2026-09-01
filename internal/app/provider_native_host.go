package app

import (
	"context"
	"strings"

	cursordriver "github.com/Viking602/azem/internal/provider/cursor"
	"github.com/Viking602/azem/internal/provider/responses"
	hyagent "github.com/Viking602/venat/agent"
	hyprovider "github.com/Viking602/venat/provider"
)

// bindProviderRequestScope attaches process-local request capabilities through
// a provider interceptor. The provider.Request and its wire representation stay
// pure data; the interceptor passes the request unchanged and invokes next once.
func bindProviderRequestScope(engine hyagent.Engine, attachmentRoot string, execHost cursordriver.ExecHost) hyagent.Engine {
	attachmentRoot = strings.TrimSpace(attachmentRoot)
	if attachmentRoot == "" && execHost == nil {
		return engine
	}
	scope := hyprovider.StreamInterceptorFunc(func(ctx context.Context, next hyprovider.Driver, request hyprovider.Request) (hyprovider.Stream, error) {
		if attachmentRoot != "" {
			ctx = responses.WithAttachmentRoot(ctx, attachmentRoot)
		}
		if execHost != nil {
			ctx = cursordriver.WithExecHost(ctx, execHost)
		}
		return next.Stream(ctx, request)
	})
	engine.ModelInterceptor = hyprovider.ChainStreamInterceptors(scope, engine.ModelInterceptor)
	return engine
}

func providerAttachmentRoot(host providerHost) string {
	if host == nil {
		return ""
	}
	return strings.TrimSpace(host.AttachmentRoot())
}
