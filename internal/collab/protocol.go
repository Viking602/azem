package collab

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/Viking602/azem/internal/session"
)

const (
	ProtocolVersion     = 3
	RoomIDBytes         = 16
	RoomKeyBytes        = 32
	WriteTokenBytes     = 16
	EnvelopeHeaderBytes = 4
	DefaultRelayURL     = "wss://my.omp.sh"
	maxFrameBytes       = 8 << 20
	SnapshotChunkBytes  = 512 << 10
)

type Frame struct {
	Type       string               `json:"t"`
	Proto      int                  `json:"proto,omitempty"`
	Name       string               `json:"name,omitempty"`
	WriteToken string               `json:"writeToken,omitempty"`
	ReadOnly   bool                 `json:"readOnly,omitempty"`
	EntryCount int                  `json:"entryCount,omitempty"`
	Session    *session.Session     `json:"session,omitempty"`
	Tree       *session.SessionTree `json:"tree,omitempty"`
	Blocks     []session.Block      `json:"blocks,omitempty"`
	Block      *session.Block       `json:"block,omitempty"`
	State      json.RawMessage      `json:"state,omitempty"`
	Event      json.RawMessage      `json:"event,omitempty"`
	Text       string               `json:"text,omitempty"`
	Reason     string               `json:"reason,omitempty"`
	Message    string               `json:"message,omitempty"`
	Final      bool                 `json:"final,omitempty"`
}

type Envelope struct {
	PeerID  uint32
	Payload []byte
}

type Link struct {
	WebSocketURL string
	RoomID       string
	Key          []byte
	WriteToken   []byte
}

func GenerateRoom() (roomID string, key, writeToken []byte, err error) {
	room := make([]byte, RoomIDBytes)
	key = make([]byte, RoomKeyBytes)
	writeToken = make([]byte, WriteTokenBytes)
	for _, value := range [][]byte{room, key, writeToken} {
		if _, err = rand.Read(value); err != nil {
			return "", nil, nil, err
		}
	}
	return base64.RawURLEncoding.EncodeToString(room), key, writeToken, nil
}

func PackEnvelope(peerID uint32, sealed []byte) ([]byte, error) {
	if len(sealed) == 0 || len(sealed)+EnvelopeHeaderBytes > maxFrameBytes {
		return nil, errors.New("collaboration envelope payload is empty or oversized")
	}
	result := make([]byte, EnvelopeHeaderBytes+len(sealed))
	binary.BigEndian.PutUint32(result[:EnvelopeHeaderBytes], peerID)
	copy(result[EnvelopeHeaderBytes:], sealed)
	return result, nil
}

func UnpackEnvelope(value []byte) (Envelope, error) {
	if len(value) <= EnvelopeHeaderBytes || len(value) > maxFrameBytes {
		return Envelope{}, errors.New("collaboration envelope is truncated or oversized")
	}
	return Envelope{PeerID: binary.BigEndian.Uint32(value[:EnvelopeHeaderBytes]), Payload: value[EnvelopeHeaderBytes:]}, nil
}

func RewriteEnvelopePeer(value []byte, peerID uint32) error {
	if len(value) <= EnvelopeHeaderBytes {
		return errors.New("collaboration envelope is truncated")
	}
	binary.BigEndian.PutUint32(value[:EnvelopeHeaderBytes], peerID)
	return nil
}

func FormatLink(relayURL, roomID string, key, writeToken []byte) (string, error) {
	origin, err := normalizeRelayOrigin(relayURL)
	if err != nil {
		return "", err
	}
	if !roomIDPattern.MatchString(roomID) || len(key) != RoomKeyBytes || (writeToken != nil && len(writeToken) != WriteTokenBytes) {
		return "", errors.New("invalid collaboration room id, key, or write token")
	}
	secret := append(append([]byte(nil), key...), writeToken...)
	encoded := base64.RawURLEncoding.EncodeToString(secret)
	if origin == DefaultRelayURL {
		return roomID + "." + encoded, nil
	}
	if strings.HasPrefix(origin, "wss://") {
		return strings.TrimPrefix(origin, "wss://") + "/r/" + roomID + "." + encoded, nil
	}
	return origin + "/r/" + roomID + "." + encoded, nil
}

func FormatWebLink(relayURL, webURL, roomID string, key, writeToken []byte) (string, error) {
	inner, err := FormatLink(relayURL, roomID, key, writeToken)
	if err != nil {
		return "", err
	}
	base, err := normalizeWebBase(relayURL, webURL)
	if err != nil {
		return "", err
	}
	return base + "/#" + inner, nil
}

var (
	roomPathPattern = regexp.MustCompile(`^/r/([A-Za-z0-9_-]{10,64})(?:\.([A-Za-z0-9_-]+))?$`)
	bareLinkPattern = regexp.MustCompile(`^([A-Za-z0-9_-]{10,64})[#.]([A-Za-z0-9_-]+)$`)
	roomIDPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{10,64}$`)
)

func ParseLink(raw string) (Link, error) {
	text := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(raw, "%23", "#"), "%23", "#"))
	if match := bareLinkPattern.FindStringSubmatch(text); match != nil {
		text = DefaultRelayURL + "/r/" + match[1] + "." + match[2]
	} else if !strings.Contains(text, "://") {
		text = "wss://" + text
	}
	parsed, err := url.Parse(text)
	if err != nil {
		return Link{}, fmt.Errorf("invalid collaboration link: %w", err)
	}
	if (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Fragment != "" {
		return ParseLink(parsed.Fragment)
	}
	origin, err := normalizeRelayOrigin(parsed.Scheme + "://" + parsed.Host)
	if err != nil {
		return Link{}, err
	}
	match := roomPathPattern.FindStringSubmatch(parsed.Path)
	if match == nil {
		if parsed.Fragment != "" && parsed.Scheme != "http" && parsed.Scheme != "https" {
			return ParseLink(parsed.Fragment)
		}
		return Link{}, errors.New("collaboration link must contain /r/<roomId>")
	}
	secretText := match[2]
	if secretText == "" {
		secretText = parsed.Fragment
	}
	secret, err := base64.RawURLEncoding.DecodeString(secretText)
	if err != nil || (len(secret) != RoomKeyBytes && len(secret) != RoomKeyBytes+WriteTokenBytes) {
		return Link{}, errors.New("collaboration link key must contain 32 view bytes or 48 writable bytes")
	}
	result := Link{WebSocketURL: origin + "/r/" + match[1], RoomID: match[1], Key: append([]byte(nil), secret[:RoomKeyBytes]...)}
	if len(secret) > RoomKeyBytes {
		result.WriteToken = append([]byte(nil), secret[RoomKeyBytes:]...)
	}
	return result, nil
}

func normalizeRelayOrigin(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("invalid collaboration relay URL")
	}
	scheme := parsed.Scheme
	switch scheme {
	case "https", "wss":
		scheme = "wss"
	case "http", "ws":
		scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported collaboration relay scheme %q", parsed.Scheme)
	}
	if scheme == "ws" && !isLocalHost(parsed.Hostname()) {
		return "", errors.New("collaboration relay must use WSS outside localhost")
	}
	return scheme + "://" + parsed.Host, nil
}

func normalizeWebBase(relayURL, webURL string) (string, error) {
	if strings.TrimSpace(webURL) == "" {
		origin, err := normalizeRelayOrigin(relayURL)
		if err != nil {
			return "", err
		}
		return strings.Replace(strings.Replace(origin, "wss://", "https://", 1), "ws://", "http://", 1), nil
	}
	parsed, err := url.Parse(strings.TrimSpace(webURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("collaboration web URL must be absolute HTTP(S) without query or fragment")
	}
	if parsed.Scheme == "http" && !isLocalHost(parsed.Hostname()) {
		return "", errors.New("collaboration web URL must use HTTPS outside localhost")
	}
	return strings.TrimRight(parsed.Scheme+"://"+parsed.Host+parsed.Path, "/"), nil
}

func isLocalHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
