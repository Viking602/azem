package sessionshare

import (
	"crypto/rand"
	"testing"
	"time"
)

func TestShareEnvelopeRoundTripAndWrongKeyFailure(t *testing.T) {
	key := make([]byte, KeyBytes)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	document := Document{Version: shareDocumentVersion, SharedAt: time.Unix(1, 0).UTC(), Session: ShareSession{}}
	sealed, err := sealDocument(document, key)
	if err != nil {
		t.Fatal(err)
	}
	if len(sealed) <= NonceBytes {
		t.Fatalf("sealed bytes=%d", len(sealed))
	}
	opened, err := Open(sealed, key)
	if err != nil || !opened.SharedAt.Equal(document.SharedAt) {
		t.Fatalf("opened=%#v error=%v", opened, err)
	}
	wrong := append([]byte(nil), key...)
	wrong[0] ^= 1
	if _, err := Open(sealed, wrong); err == nil {
		t.Fatal("wrong key decrypted share")
	}
	encoded := EncodeKey(key)
	decoded, err := DecodeKey(encoded)
	if err != nil || string(decoded) != string(key) {
		t.Fatalf("key round trip error=%v", err)
	}
}
