package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/venat/tool"
)

const ToolComputer = "computer"

type computerDriver struct{ root string }

func (*computerDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{Name: ToolComputer, Description: "Native macOS desktop control using CoreGraphics/Accessibility through the system Swift toolchain. status reports existing permissions without requesting them; screenshot, snapshot, click/move/drag/scroll/type/press operate the desktop. launch opens an installed app. Requires macOS Screen Recording/Accessibility permission where applicable. No browser or TS runtime.", InputSchema: tool.Schema{Type: "object", Required: []string{"action"}, AdditionalProperties: &additional, Properties: map[string]tool.Schema{
		"action": {Type: "string", Enum: []string{"status", "screenshot", "snapshot", "launch", "click", "move", "drag", "scroll", "type", "press"}}, "x": {Type: "number"}, "y": {Type: "number"}, "to_x": {Type: "number"}, "to_y": {Type: "number"}, "text": {Type: "string"}, "key": {Type: "string"}, "app": {Type: "string"}, "modifiers": {Type: "array", Items: &tool.Schema{Type: "string", Enum: []string{"command", "control", "option", "shift"}}},
	}}, Concurrency: tool.ConcurrencyExclusive, ConcurrencyGroup: "native-desktop"}
}

func (*computerDriver) ToolPolicy() agentruntime.ToolPolicy {
	p := approvedExternalPolicy("computer", "desktop")
	p.Concurrency = tool.ConcurrencyExclusive
	p.ConcurrencyGroup = "native-desktop"
	return p
}

func (d *computerDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input struct {
		Action    string   `json:"action"`
		X         float64  `json:"x"`
		Y         float64  `json:"y"`
		ToX       float64  `json:"to_x"`
		ToY       float64  `json:"to_y"`
		Text      string   `json:"text"`
		Key       string   `json:"key"`
		App       string   `json:"app"`
		Modifiers []string `json:"modifiers"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return toolError(call, err.Error()), nil
	}
	if runtime.GOOS != "darwin" {
		return toolError(call, "native computer control currently requires macOS"), nil
	}
	if len(input.Text) > 20000 || len(input.App) > 256 || len(input.Modifiers) > 4 {
		return toolError(call, "computer arguments exceed bounds"), nil
	}
	for _, coordinate := range []float64{input.X, input.Y, input.ToX, input.ToY} {
		if math.IsNaN(coordinate) || math.IsInf(coordinate, 0) || math.Abs(coordinate) > 100000 {
			return toolError(call, "coordinates must be finite and within +/-100000"), nil
		}
	}
	for _, modifier := range input.Modifiers {
		if modifier != "command" && modifier != "control" && modifier != "option" && modifier != "shift" {
			return toolError(call, "invalid key modifier"), nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if input.Action == "screenshot" {
		directory, err := os.MkdirTemp("", "azem-screen-")
		if err != nil {
			return toolError(call, err.Error()), nil
		}
		defer os.RemoveAll(directory)
		path := filepath.Join(directory, "screen.png")
		if _, err := nativeRun(ctx, d.root, "", "/usr/sbin/screencapture", "-x", "-t", "png", path); err != nil {
			return toolError(call, err.Error()), nil
		}
		data, err := nativeImageFile(directory, "screen.png")
		if err != nil {
			return toolError(call, err.Error()), nil
		}
		return nativeImageResult(call, data, "Desktop screenshot")
	}
	if input.Action == "launch" {
		if input.App == "" {
			return toolError(call, "app is required"), nil
		}
		result, err := nativeRun(ctx, d.root, "", "/usr/bin/open", "-a", input.App)
		if err != nil {
			return toolError(call, err.Error()), nil
		}
		return nativeJSONResult(call, map[string]string{"app": input.App, "result": result})
	}
	valid := map[string]bool{"status": true, "snapshot": true, "click": true, "move": true, "drag": true, "scroll": true, "type": true, "press": true}
	if !valid[input.Action] {
		return toolError(call, "unsupported computer action"), nil
	}
	if input.Action == "type" && input.Text == "" {
		return toolError(call, "text is required"), nil
	}
	if input.Action == "press" && input.Key == "" {
		return toolError(call, "key is required"), nil
	}
	parameters, err := json.Marshal(input)
	if err != nil {
		return toolError(call, err.Error()), nil
	}
	output, err := nativeRun(ctx, d.root, string(parameters), "/usr/bin/xcrun", "swift", "-e", nativeComputerSwift)
	if err != nil {
		return toolError(call, fmt.Sprintf("native desktop action failed: %v", err)), nil
	}
	if !json.Valid([]byte(output)) {
		return toolError(call, "invalid native desktop response: "+strconv.Quote(output)), nil
	}
	return nativeJSONResult(call, json.RawMessage(output))
}

const nativeComputerSwift = `import Foundation
import AppKit
import ApplicationServices
let request = try JSONSerialization.jsonObject(with: FileHandle.standardInput.readDataToEndOfFile()) as! [String: Any]
let action = request["action"] as! String
func emit(_ value: Any) throws { let data = try JSONSerialization.data(withJSONObject: value, options: [.sortedKeys]); FileHandle.standardOutput.write(data) }
func failure(_ text: String) -> Never { FileHandle.standardError.write(Data(text.utf8)); exit(1) }
func number(_ key: String) -> Double { (request[key] as? NSNumber)?.doubleValue ?? 0 }
if action == "status" {
    try emit(["accessibility": AXIsProcessTrusted(), "screenRecording": CGPreflightScreenCaptureAccess()])
    exit(0)
}
guard AXIsProcessTrusted() else { failure("Accessibility permission is unavailable for the native desktop tool") }
if action == "snapshot" {
    guard let app = NSWorkspace.shared.frontmostApplication else { failure("No foreground application") }
    let root = AXUIElementCreateApplication(app.processIdentifier)
    var nodes = [[String: Any]]()
    func attribute(_ element: AXUIElement, _ key: String) -> CFTypeRef? {
        var value: CFTypeRef?
        guard AXUIElementCopyAttributeValue(element, key as CFString, &value) == .success else { return nil }
        return value
    }
    func visit(_ element: AXUIElement, _ depth: Int) {
        if nodes.count >= 1000 || depth > 8 { return }
        let role = attribute(element, kAXRoleAttribute) as? String ?? ""
        var node: [String: Any] = ["depth": depth, "role": role]
        for key in [kAXTitleAttribute, kAXDescriptionAttribute] { if let value = attribute(element, key) as? String { node[key] = String(value.prefix(1000)) } }
        if role != "AXSecureTextField", let value = attribute(element, kAXValueAttribute) as? String { node["value"] = String(value.prefix(2000)) }
        nodes.append(node)
        if let children = attribute(element, kAXChildrenAttribute) as? [AXUIElement] { for child in children.prefix(1000) { visit(child, depth+1) } }
    }
    visit(root, 0)
    try emit(["app": app.localizedName ?? "", "pid": app.processIdentifier, "nodes": nodes, "bounded": true])
    exit(0)
}
var flags = CGEventFlags()
for modifier in request["modifiers"] as? [String] ?? [] {
    switch modifier { case "command": flags.insert(.maskCommand); case "control": flags.insert(.maskControl); case "option": flags.insert(.maskAlternate); case "shift": flags.insert(.maskShift); default: failure("Invalid modifier") }
}
func post(_ event: CGEvent?) { guard let event = event else { failure("Cannot create native input event") }; event.flags = flags; event.post(tap: .cghidEventTap) }
let point = CGPoint(x: number("x"), y: number("y"))
switch action {
case "click":
    post(CGEvent(mouseEventSource: nil, mouseType: .leftMouseDown, mouseCursorPosition: point, mouseButton: .left))
    post(CGEvent(mouseEventSource: nil, mouseType: .leftMouseUp, mouseCursorPosition: point, mouseButton: .left))
case "move": post(CGEvent(mouseEventSource: nil, mouseType: .mouseMoved, mouseCursorPosition: point, mouseButton: .left))
case "drag":
    post(CGEvent(mouseEventSource: nil, mouseType: .leftMouseDown, mouseCursorPosition: point, mouseButton: .left))
    let target = CGPoint(x: number("to_x"), y: number("to_y"))
    for step in 1...10 { let fraction = Double(step)/10; let location = CGPoint(x: point.x+(target.x-point.x)*fraction, y: point.y+(target.y-point.y)*fraction); post(CGEvent(mouseEventSource: nil, mouseType: .leftMouseDragged, mouseCursorPosition: location, mouseButton: .left)); Thread.sleep(forTimeInterval: 0.01) }
    post(CGEvent(mouseEventSource: nil, mouseType: .leftMouseUp, mouseCursorPosition: target, mouseButton: .left))
case "scroll": post(CGEvent(scrollWheelEvent2Source: nil, units: .pixel, wheelCount: 2, wheel1: Int32(number("y")), wheel2: Int32(number("x")), wheel3: 0))
case "type":
    let units = Array((request["text"] as? String ?? "").utf16)
    var start = 0
    while start < units.count {
        var end = min(start+20, units.count)
        if end < units.count && units[end-1] >= 0xD800 && units[end-1] <= 0xDBFF { end -= 1 }
        let chunk = Array(units[start..<end])
        guard let down = CGEvent(keyboardEventSource: nil, virtualKey: 0, keyDown: true), let up = CGEvent(keyboardEventSource: nil, virtualKey: 0, keyDown: false) else { failure("Cannot create keyboard event") }
        chunk.withUnsafeBufferPointer { buffer in down.keyboardSetUnicodeString(stringLength: buffer.count, unicodeString: buffer.baseAddress); up.keyboardSetUnicodeString(stringLength: buffer.count, unicodeString: buffer.baseAddress) }
        post(down); post(up); start = end
    }
case "press":
    let keys: [String: CGKeyCode] = ["enter":36,"tab":48,"space":49,"backspace":51,"escape":53,"delete":117,"left":123,"right":124,"down":125,"up":126,"home":115,"end":119,"pageup":116,"pagedown":121,"a":0,"c":8,"v":9,"x":7,"z":6,"q":12,"s":1,"f":3,"r":15,"w":13,"l":37]
    guard let key = keys[(request["key"] as? String ?? "").lowercased()] else { failure("Unsupported key") }
    post(CGEvent(keyboardEventSource: nil, virtualKey: key, keyDown: true)); post(CGEvent(keyboardEventSource: nil, virtualKey: key, keyDown: false))
default: failure("Unsupported action")
}
try emit(["action":action,"sent":true])
`
