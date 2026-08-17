package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func semanticStateForTest(objective string) string {
	encoded, _ := json.Marshal(SemanticStateV1{Version: 1, Objective: StateFactV1{
		Text: objective, Status: "active", Authority: "agent", Confidence: "inferred",
		Sources: []EvidenceRefV1{{Kind: "checkpoint", ID: "test:evidence"}},
	}})
	return string(encoded)
}

func writeProviderToolCall(writer http.ResponseWriter, responseID, callID, name, arguments string) {
	_, _ = fmt.Fprintf(writer, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":%q,\"call_id\":%q,\"name\":%q,\"arguments\":%q}}\n\n", responseID+"-item", callID, name, arguments)
	_, _ = fmt.Fprintf(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":%q,\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":4,\"total_tokens\":14}}}\n\n", responseID)
}

func writeProviderText(writer http.ResponseWriter, responseID, text string) {
	_, _ = fmt.Fprintf(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\n", text)
	_, _ = fmt.Fprintf(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":%q,\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":4,\"total_tokens\":14}}}\n\n", responseID)
}

func waitForProviderRun(t *testing.T, service *Service, runID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		event, err := service.NextEvent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.RunID != runID {
			continue
		}
		switch event.Kind {
		case EventRunFinished:
			return
		case EventRunFailed, EventRunCancelled:
			t.Fatalf("run ended as %s: %s", event.Kind, event.Text)
		}
	}
}
