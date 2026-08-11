package desktop

import (
	"reflect"
	"testing"
)

func TestTerminalCommand(t *testing.T) {
	tests := []struct {
		goos string
		name string
		args []string
	}{
		{goos: "darwin", name: "open", args: []string{"-a", "Terminal", "/workspace/azem"}},
		{goos: "windows", name: "cmd.exe", args: []string{"/C", "start", "", "/workspace/azem"}},
		{goos: "linux", name: "x-terminal-emulator", args: []string{"--working-directory", "/workspace/azem"}},
	}
	for _, test := range tests {
		t.Run(test.goos, func(t *testing.T) {
			name, args, err := terminalCommand(test.goos, "/workspace/azem")
			if err != nil {
				t.Fatal(err)
			}
			if name != test.name || !reflect.DeepEqual(args, test.args) {
				t.Fatalf("terminalCommand() = %q %#v, want %q %#v", name, args, test.name, test.args)
			}
		})
	}
}

func TestTerminalCommandRejectsUnknownPlatform(t *testing.T) {
	if _, _, err := terminalCommand("plan9", "/workspace/azem"); err == nil {
		t.Fatal("terminalCommand() accepted an unsupported platform")
	}
}
