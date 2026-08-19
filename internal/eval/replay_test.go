package eval

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestLifecycleReplayFixturesConvergeDeterministically(t *testing.T) {
	t.Parallel()
	file, err := os.Open("testdata/replay/lifecycle_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	fixtures, err := ReadReplayFixtures(file)
	if err != nil {
		t.Fatal(err)
	}
	wantScenarios := map[string]bool{
		"cancellation": false, "retry": false, "approval": false, "tool-spill": false,
		"compaction": false, "recovery": false, "background-child-completion": false, "stale-user-guidance": false,
	}
	if len(fixtures) != len(wantScenarios) {
		t.Fatalf("fixture count = %d, want %d", len(fixtures), len(wantScenarios))
	}
	for _, fixture := range fixtures {
		if _, exists := wantScenarios[fixture.Name]; !exists {
			t.Fatalf("unexpected fixture %q", fixture.Name)
		}
		wantScenarios[fixture.Name] = true
		first, err := Replay(fixture)
		if err != nil {
			t.Fatalf("replay %s: %v", fixture.Name, err)
		}
		second, err := Replay(fixture)
		if err != nil {
			t.Fatalf("second replay %s: %v", fixture.Name, err)
		}
		if !reflect.DeepEqual(first, fixture.Expected) || !reflect.DeepEqual(second, fixture.Expected) {
			t.Fatalf("fixture %s did not converge\n first: %+v\nsecond: %+v\n  want: %+v", fixture.Name, first, second, fixture.Expected)
		}
	}
	for name, seen := range wantScenarios {
		if !seen {
			t.Fatalf("fixture %s was not loaded", name)
		}
	}
}

func TestReplayFixturesRoundTripWithoutLosingEvidence(t *testing.T) {
	t.Parallel()
	file, err := os.Open("testdata/replay/lifecycle_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	fixtures, err := ReadReplayFixtures(file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	var first, second bytes.Buffer
	if err := WriteReplayFixtures(&first, fixtures); err != nil {
		t.Fatal(err)
	}
	if err := WriteReplayFixtures(&second, fixtures); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("replay fixture encoding is not deterministic")
	}
	roundTrip, err := ReadReplayFixtures(bytes.NewReader(first.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roundTrip, fixtures) {
		t.Fatalf("replay round trip changed fixtures")
	}
}

func TestReplayRejectsExpectedStateMismatch(t *testing.T) {
	t.Parallel()
	fixture := completedReplayFixture("mismatch")
	fixture.Expected.RunState = "failed"
	if _, err := Replay(fixture); err == nil {
		t.Fatal("accepted replay with mismatched expected terminal state")
	}
}

func TestReplayRejectsInvalidArtifactDigest(t *testing.T) {
	t.Parallel()
	fixture := completedReplayFixture("bad-artifact")
	fixture.Steps = append(fixture.Steps, ReplayStepV1{Sequence: 3, ID: "artifact", Kind: "artifact", TargetID: "artifact", SHA256: "bad"})
	if _, err := Replay(fixture); err == nil {
		t.Fatal("accepted invalid artifact digest")
	}
}

func TestReplayRejectsUnboundInitialArtifactEvidence(t *testing.T) {
	t.Parallel()
	fixture := completedReplayFixture("unbound-artifact-evidence")
	digest := strings.Repeat("a", 64)
	fixture.Initial.Evidence = []ReplayEvidenceRefV1{{Kind: "artifact", ID: "artifact", SHA256: digest}}
	fixture.Expected.Evidence = append([]ReplayEvidenceRefV1(nil), fixture.Initial.Evidence...)

	if _, err := Replay(fixture); err == nil || !strings.Contains(err.Error(), "does not match stored hash") {
		t.Fatalf("unbound artifact evidence error = %v", err)
	}
}

func TestReplayRejectsTerminalToolMutation(t *testing.T) {
	t.Parallel()
	fixture := ReplayFixtureV1{
		Version: 1, Name: "invalid", Scenario: "terminal state mutation",
		Initial: ReplayStateV1{},
		Steps: []ReplayStepV1{
			{Sequence: 1, ID: "start", Kind: "tool", TargetID: "call", State: "running"},
			{Sequence: 2, ID: "complete", Kind: "tool", TargetID: "call", State: "completed"},
			{Sequence: 3, ID: "rewrite", Kind: "tool", TargetID: "call", State: "failed"},
		},
	}
	if _, err := Replay(fixture); err == nil {
		t.Fatal("accepted a terminal tool state rewrite")
	}
}
