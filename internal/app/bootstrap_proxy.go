package app

import (
	"context"

	"github.com/Viking602/azem/internal/netproxy"
)

func bootstrap(ctx context.Context, startupWorkspace, configFile string, forceWorkspace, desktopMode bool) (BootstrapResult, error) {
	netproxy.InstallDefaultTransport()
	assembly := bootstrapAssembly{ctx: ctx}
	result, err := assembly.build(startupWorkspace, configFile, forceWorkspace, desktopMode)
	if err != nil {
		assembly.close()
		return BootstrapResult{}, err
	}
	return result, nil
}
