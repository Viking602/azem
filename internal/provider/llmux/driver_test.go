package llmuxdriver

import (
	"io"
	"testing"

	sdk "github.com/Viking602/llmux"
	hyprovider "github.com/Viking602/venat/provider"
)

type sliceStream struct {
	parts []sdk.Part
}

func (s *sliceStream) Recv() (sdk.Part, error) {
	if len(s.parts) == 0 {
		return sdk.Part{}, io.EOF
	}
	part := s.parts[0]
	s.parts = s.parts[1:]
	return part, nil
}

func (*sliceStream) Close() error { return nil }

func TestProfilesAndStreamMapping(t *testing.T) {
	profiles := Profiles()
	foundOpenAI, foundOpenRouter := false, false
	for index, profile := range profiles {
		if index > 0 && profiles[index-1].ID >= profile.ID {
			t.Fatalf("profiles are not sorted and unique at %q", profile.ID)
		}
		if profile.ID == "chatgpt" || profile.ID == "grok" {
			t.Fatalf("reserved Azem provider leaked into llmux settings: %q", profile.ID)
		}
		foundOpenAI = foundOpenAI || profile.ID == "openai"
		foundOpenRouter = foundOpenRouter || profile.ID == "openrouter"
	}
	if !foundOpenAI || !foundOpenRouter {
		t.Fatalf("missing expected profiles: openai=%v openrouter=%v", foundOpenAI, foundOpenRouter)
	}
	stream := &streamAdapter{inner: &sliceStream{parts: []sdk.Part{
		{Kind: sdk.PartTextDelta, Delta: "hello"},
		{Kind: sdk.PartFinish, FinishReason: sdk.FinishStop, Usage: sdk.Usage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5}},
	}}}
	text, err := stream.Recv()
	if err != nil || text.Kind != hyprovider.EventTextDelta || text.Text != "hello" || text.TextPhase != hyprovider.TextPhaseFinalAnswer {
		t.Fatalf("text event = %+v, error = %v", text, err)
	}
	done, err := stream.Recv()
	if err != nil || done.Kind != hyprovider.EventDone || done.StopReason != hyprovider.StopReasonComplete || done.Usage.TotalTokens != 5 {
		t.Fatalf("done event = %+v, error = %v", done, err)
	}
}
