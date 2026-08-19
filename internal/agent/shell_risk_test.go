package agent

import "testing"

func TestClassifyShellRisk(t *testing.T) {
	tests := []struct {
		command string
		network bool
		want    string
	}{
		{command: "go test ./internal/agent", want: "low"},
		{command: "gofmt -w shell.go", want: "low"},
		{command: "git status", want: "low"},
		{command: "git diff --stat", want: "low"},
		{command: "ls -la", want: "low"},
		{command: "rg ClassifyShellRisk", want: "low"},
		{command: "cat 'literal > text'", want: "low"},
		{command: `cat "literal ; text"`, want: "low"},
		{command: "bun test", want: "low"},
		{command: "make test", want: "medium"},
		{command: "go build ./...", want: "medium"},
		{command: "python3 script.py", want: "medium"},
		{command: "cat ~/.ssh/id_ed25519 > workspace/leak", want: "high"},
		{command: "cat ~/.ssh/id_ed25519", want: "high"},
		{command: "cat /Users/alice/.aws/credentials", want: "high"},
		{command: "cat .env.production", want: "high"},
		{command: "go test ./...; cat ~/.ssh/id_ed25519", want: "high"},
		{command: "go test ./... && cat ~/.ssh/id_ed25519", want: "high"},
		{command: "go test $(cat ~/.ssh/id_ed25519)", want: "high"},
		{command: "rm -rf /tmp/out", want: "high"},
		{command: "git push origin HEAD", want: "high"},
		{command: "git reset --hard", want: "high"},
		{command: "curl https://example.com", want: "high"},
		{command: "sudo reboot", want: "high"},
		{command: "find . -name '*.go' -delete", want: "high"},
		{command: "go test ./...", network: true, want: "high"},
		{command: "", want: "high"},
	}
	for _, test := range tests {
		if got := ClassifyShellRisk(test.command, test.network); got != test.want {
			t.Fatalf("ClassifyShellRisk(%q, %v) = %q, want %q", test.command, test.network, got, test.want)
		}
	}
}
