package desktopipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Client struct {
	connection net.Conn
	codec      *Codec
	clientID   string
	workspace  string
	mu         sync.Mutex
	OnEnvelope func(Envelope)
	OnBinary   func(BinaryMetadata, []byte)
}

func Connect(ctx context.Context, endpoint Endpoint, clientID string, lastSequence uint64) (*Client, HelloAck, error) {
	if endpoint.Protocol != ProtocolVersion {
		return nil, HelloAck{}, fmt.Errorf("unsupported daemon protocol %d", endpoint.Protocol)
	}
	token, err := ReadTokenFile(endpoint.TokenFile)
	if err != nil {
		return nil, HelloAck{}, err
	}
	connection, err := Dial(ctx, endpoint.Address)
	if err != nil {
		return nil, HelloAck{}, err
	}
	fail := func(err error) (*Client, HelloAck, error) {
		connection.Close()
		return nil, HelloAck{}, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	} else {
		_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
	}
	codec := NewCodec(connection)
	frame, err := codec.ReadFrame()
	if err != nil {
		return fail(err)
	}
	if frame.Envelope == nil || frame.Envelope.Kind != FrameHello {
		return fail(ErrAuthentication)
	}
	var challenge Challenge
	if err := json.Unmarshal(frame.Envelope.Payload, &challenge); err != nil || challenge.Protocol != ProtocolVersion || challenge.WorkspaceID != endpoint.WorkspaceID {
		return fail(ErrAuthentication)
	}
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		clientID = uuid.NewString()
	}
	authentication := Authenticate{
		ClientID: clientID, Protocol: ProtocolVersion, LastSequence: lastSequence,
		Proof: AuthenticationProof(token, challenge.Nonce, clientID, endpoint.WorkspaceID, ProtocolVersion),
	}
	hello := NewEnvelope(FrameHelloAck)
	hello.ClientID, hello.WorkspaceID, hello.Payload = clientID, endpoint.WorkspaceID, mustJSON(authentication)
	if err := codec.WriteEnvelope(hello); err != nil {
		return fail(err)
	}
	frame, err = codec.ReadFrame()
	if err != nil {
		return fail(err)
	}
	if frame.Envelope == nil || frame.Envelope.Kind != FrameHelloAck {
		return fail(ErrAuthentication)
	}
	var ack HelloAck
	if err := json.Unmarshal(frame.Envelope.Payload, &ack); err != nil {
		return fail(err)
	}
	_ = connection.SetDeadline(time.Time{})
	return &Client{connection: connection, codec: codec, clientID: clientID, workspace: endpoint.WorkspaceID}, ack, nil
}

func (client *Client) Request(ctx context.Context, method Method, payload any, target any) error {
	if client == nil || client.connection == nil {
		return errors.New("IPC client is unavailable")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	requestID := uuid.NewString()
	envelope := NewEnvelope(FrameRequest)
	envelope.ID, envelope.ClientID, envelope.WorkspaceID, envelope.Method, envelope.Payload = requestID, client.clientID, client.workspace, method, encoded
	client.mu.Lock()
	defer client.mu.Unlock()
	if deadline, ok := ctx.Deadline(); ok {
		_ = client.connection.SetDeadline(deadline)
		defer client.connection.SetDeadline(time.Time{})
	}
	if err := client.codec.WriteEnvelope(envelope); err != nil {
		return err
	}
	for {
		frame, err := client.codec.ReadFrame()
		if err != nil {
			return err
		}
		if frame.Binary != nil {
			if client.OnBinary != nil {
				client.OnBinary(*frame.Binary, frame.Data)
			}
			continue
		}
		response := frame.Envelope
		if response.Kind == FrameResponse && response.ID == requestID {
			if response.Error != nil {
				return fmt.Errorf("%s: %s", response.Error.Code, response.Error.Message)
			}
			if target != nil && len(response.Payload) > 0 && string(response.Payload) != "null" {
				if err := json.Unmarshal(response.Payload, target); err != nil {
					return err
				}
			}
			return nil
		}
		if client.OnEnvelope != nil {
			client.OnEnvelope(*response)
		}
	}
}

func (client *Client) StopDaemon(ctx context.Context, includeActive bool) error {
	if client == nil || client.connection == nil {
		return nil
	}
	requestID := uuid.NewString()
	envelope := NewEnvelope(FrameDaemonStop)
	envelope.ID, envelope.ClientID, envelope.WorkspaceID = requestID, client.clientID, client.workspace
	envelope.Payload = mustJSON(DaemonStop{IncludeActive: includeActive})
	client.mu.Lock()
	defer client.mu.Unlock()
	if deadline, ok := ctx.Deadline(); ok {
		_ = client.connection.SetDeadline(deadline)
		defer client.connection.SetDeadline(time.Time{})
	}
	if err := client.codec.WriteEnvelope(envelope); err != nil {
		return err
	}
	for {
		frame, err := client.codec.ReadFrame()
		if err != nil {
			return err
		}
		if frame.Binary != nil {
			if client.OnBinary != nil {
				client.OnBinary(*frame.Binary, frame.Data)
			}
			continue
		}
		response := frame.Envelope
		if response.Kind == FrameResponse && response.ID == requestID {
			if response.Error != nil {
				return fmt.Errorf("%s: %s", response.Error.Code, response.Error.Message)
			}
			return nil
		}
		if client.OnEnvelope != nil {
			client.OnEnvelope(*response)
		}
	}
}

func (client *Client) Close() error {
	if client == nil || client.connection == nil {
		return nil
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	detach := NewEnvelope(FrameClientDetach)
	detach.ClientID, detach.WorkspaceID = client.clientID, client.workspace
	_ = client.codec.WriteEnvelope(detach)
	err := client.connection.Close()
	client.connection = nil
	return err
}
