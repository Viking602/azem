package parity

import "testing"

func TestManifestLoadsValidatedReferenceBaseline(t *testing.T) {
	manifest, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if manifest.BaselineVersion != "18.0.3" || manifest.BaselineCommit != "160ed439ac0df594347e7d7018b813a7ffdb5e81" {
		t.Fatalf("unexpected reference baseline: version=%q commit=%q", manifest.BaselineVersion, manifest.BaselineCommit)
	}
	if open := manifest.OpenCapabilities(); len(open) != 0 {
		t.Fatalf("completed parity manifest still has %d open capabilities: %#v", len(open), open)
	}
	for _, capability := range manifest.Capabilities {
		if capability.Status != StatusComplete && capability.Status != StatusStronger {
			t.Fatalf("capability %q has non-terminal status %q", capability.ID, capability.Status)
		}
	}
}

func TestManifestRejectsDuplicateCapabilityIdentity(t *testing.T) {
	manifest, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	manifest.Capabilities = append(manifest.Capabilities, manifest.Capabilities[0])
	if err := manifest.Validate(); err == nil {
		t.Fatal("duplicate capability identity was accepted")
	}
}
