package devin

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Viking602/venat/message"
)

func TestNativeToolsPreserveGovernedCallsAndHistory(t *testing.T) {
	definitions := []message.ToolDefinition{{Name: "coding.read_file"}, {Name: "coding.write_file"}, {Name: "coding.shell"}, {Name: "replace"}, {Name: "hub"}}
	_, names := nativeTools(definitions, nil)
	for local, native := range nativeToolNames {
		if names.Wire(local) != native || names.Local(native) != local {
			t.Fatalf("mapping %s -> %s", local, names.Wire(local))
		}
	}
	for _, test := range []struct{ local, raw, expected string }{
		{"coding.read_file", `{"path":"x","offset":2,"limit":3}`, `{"path":"x","offset":2,"limit":3}`},
		{"coding.write_file", `{"path":"x","content":"ok"}`, `{"path":"x","content":"ok"}`},
		{"replace", `{"path":"x","old_string":"one","new_string":"two"}`, `{"edits":[{"new_text":"two","old_text":"one"}],"path":"x"}`},
		{"coding.shell", `{"command":"pwd","workdir":"/tmp/a'b"}`, `{"command":"cd '/tmp/a'\"'\"'b' && pwd"}`},
	} {
		got, err := localToolArguments(test.local, json.RawMessage(test.raw), names)
		var actual, expected any
		_ = json.Unmarshal(got, &actual)
		_ = json.Unmarshal([]byte(test.expected), &expected)
		if err != nil || !reflect.DeepEqual(actual, expected) {
			t.Fatalf("%s: got %s err=%v", test.local, got, err)
		}
	}
	for _, raw := range []string{`{"path":"x","old_string":"a","new_string":"b","replace_all":true}`, `{"path":"x","old_string":"","new_string":"b"}`} {
		if _, err := localToolArguments("replace", json.RawMessage(raw), names); err == nil {
			t.Fatal("accepted unsupported edit")
		}
	}
	for _, raw := range []string{`{"command":"pwd","shell_id":"1"}`, `{"command":"pwd","workdir":"relative"}`} {
		if _, err := localToolArguments("coding.shell", json.RawMessage(raw), names); err == nil {
			t.Fatal("accepted unsupported exec")
		}
	}
	// Another extension owning 'edit' must never be decoded as Azem replace.
	definitions = append(definitions, message.ToolDefinition{Name: "edit"})
	_, names = nativeTools(definitions, nil)
	if names.Wire("replace") != "replace" {
		t.Fatal("native alias shadowed extension")
	}
	raw := json.RawMessage(`{"extension":"args"}`)
	if got, err := localToolArguments("edit", raw, names); err != nil || string(got) != string(raw) {
		t.Fatal("extension arguments changed")
	}
	history := []message.Message{{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{Name: "coding.shell", Arguments: json.RawMessage(`{"command":"pwd","async":true}`)}}}}
	_, names = nativeTools(definitions, history)
	if names.Wire("coding.shell") != "coding_shell" {
		t.Fatal("native exec dropped historical async semantics")
	}
	if strings.Contains(names.Wire("coding.read_file"), ".") {
		t.Fatal("unsafe wire name")
	}
}
