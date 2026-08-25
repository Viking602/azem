package azem_test

import (
	"context"
	"io"

	azem "github.com/Viking602/azem"
)

func Example() {
	_ = azem.Options{Workspace: "/workspace"}
	_ = azem.Turn{Prompt: "inspect the project", Reasoning: "high"}
	_ = azem.ExportOptions{AllBranches: true}
	_ = azem.ShareOptions{ServerURL: "https://share.example/s"}
	_ = azem.ProtocolOptions{Runtime: azem.Options{Workspace: "/workspace"}, Input: nil, Output: io.Discard}
	var _ func(context.Context, azem.Options) (*azem.Runtime, error) = azem.Open
	var _ azem.ExportFormat = azem.ExportJSON
}
