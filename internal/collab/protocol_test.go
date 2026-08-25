package collab

import (
	"bytes"
	"testing"
)

func TestCollaborationLinksRoundTripWritableViewAndWebForms(t *testing.T) {
	roomID, key, token, err := GenerateRoom()
	if err != nil {
		t.Fatal(err)
	}
	full, err := FormatLink(DefaultRelayURL, roomID, key, token)
	if err != nil {
		t.Fatal(err)
	}
	view, err := FormatLink(DefaultRelayURL, roomID, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	parsedFull, err := ParseLink(full)
	if err != nil {
		t.Fatal(err)
	}
	parsedView, err := ParseLink(view)
	if err != nil {
		t.Fatal(err)
	}
	if parsedFull.WebSocketURL != DefaultRelayURL+"/r/"+roomID || !bytes.Equal(parsedFull.Key, key) || !bytes.Equal(parsedFull.WriteToken, token) {
		t.Fatalf("full link=%q parsed=%#v", full, parsedFull)
	}
	if !bytes.Equal(parsedView.Key, key) || len(parsedView.WriteToken) != 0 {
		t.Fatalf("view link=%q parsed=%#v", view, parsedView)
	}
	web, err := FormatWebLink(DefaultRelayURL, "", roomID, key, token)
	if err != nil {
		t.Fatal(err)
	}
	parsedWeb, err := ParseLink(web)
	if err != nil || !bytes.Equal(parsedWeb.WriteToken, token) {
		t.Fatalf("web link=%q parsed=%#v error=%v", web, parsedWeb, err)
	}
	local, err := FormatLink("ws://localhost:7475", roomID, key, token)
	if err != nil || len(local) < len("ws://localhost:7475") || local[:len("ws://localhost:7475")] != "ws://localhost:7475" {
		t.Fatalf("local link=%q error=%v", local, err)
	}
	if _, err := FormatLink("ws://relay.example", roomID, key, token); err == nil {
		t.Fatal("accepted insecure remote relay")
	}
}

func TestCollaborationEnvelopeAndFrameAuthentication(t *testing.T) {
	_, key, _, err := GenerateRoom()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := SealFrame(key, Frame{Type: "prompt", Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	packed, err := PackEnvelope(42, sealed)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := UnpackEnvelope(packed)
	if err != nil || envelope.PeerID != 42 {
		t.Fatalf("envelope=%#v error=%v", envelope, err)
	}
	opened, err := OpenFrame(key, envelope.Payload)
	if err != nil || opened.Type != "prompt" || opened.Text != "hello" {
		t.Fatalf("opened=%#v error=%v", opened, err)
	}
	if err := RewriteEnvelopePeer(packed, 7); err != nil {
		t.Fatal(err)
	}
	envelope, _ = UnpackEnvelope(packed)
	if envelope.PeerID != 7 {
		t.Fatalf("rewritten peer=%d", envelope.PeerID)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := OpenFrame(key, sealed); err == nil {
		t.Fatal("tampered frame authenticated")
	}
}
