package app

import (
	"context"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/capability"
	"github.com/Viking602/azem/internal/config"
)

func TestCapabilityRegistryIncludesRuntimeModesAndProviders(t *testing.T) {
	resources, err := buildResourceRouter(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := buildCapabilityRegistry(config.Default(), nil, nil, nil, resources)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(context.Background(), config.Default())
	service.AttachCapabilities(registry)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = service.Shutdown(ctx)
	})

	snapshot, err := service.CapabilitySnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []struct {
		kind capability.Kind
		id   string
	}{
		{capability.KindMode, "single"},
		{capability.KindMode, "team"},
		{capability.KindProvider, "chatgpt"},
		{capability.KindProvider, "cursor"},
		{capability.KindProvider, "grok"},
		{capability.KindResource, "artifact"},
		{capability.KindResource, "skill"},
	} {
		found := false
		for _, current := range snapshot {
			found = found || current.Kind == expected.kind && current.ID == expected.id
		}
		if !found {
			t.Fatalf("missing capability %s/%s in %#v", expected.kind, expected.id, snapshot)
		}
	}
}
