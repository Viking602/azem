package app

import "github.com/Viking602/azem/internal/securityscan"

func (b *bootstrapAssembly) attachSecurity() error {
	securityStore, err := securityscan.NewSQLStore(b.store.DB())
	if err != nil {
		return err
	}
	finalizer, err := securityscan.NewFinalizer()
	if err != nil {
		return err
	}
	b.securityStore = securityStore
	b.securityRunner = &securityExecutor{runtime: b.providerRuntime, coding: b.coding}
	b.securityService, err = securityscan.NewService(securityscan.ServiceOptions{
		Store: securityStore, Executor: b.securityRunner, BaseContext: b.service.ctx,
		Snapshotter: securityscan.Snapshotter{DataRoot: b.paths.DataDir}, Finalizer: finalizer,
		Emit: func(projection securityscan.Projection) {
			b.service.emit(b.service.ctx, Event{Kind: EventKind("security_scan_state"), State: string(projection.Scan.Status), Security: &projection})
		},
	})
	if err != nil {
		return err
	}
	b.securityRunner.service = b.securityService
	b.service.AttachSecurity(b.securityService)
	return nil
}
