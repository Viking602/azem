package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const inlinePayloadLimit = 4096

func (s *Service) spillText(ctx context.Context, text string) (inline, digest string, err error) {
	if len(text) <= inlinePayloadLimit {
		return text, "", nil
	}
	digest, err = s.blobs.Put(ctx, []byte(text))
	if err != nil {
		return "", "", err
	}
	return "", digest, nil
}

func (s *Service) loadText(ctx context.Context, inline, digest string) (string, error) {
	digest = strings.TrimSpace(digest)
	if digest == "" {
		return inline, nil
	}
	payload, err := s.blobs.Get(ctx, digest)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func (s *Service) spillBytes(ctx context.Context, payload []byte) (inline []byte, digest string, err error) {
	if len(payload) <= inlinePayloadLimit {
		return payload, "", nil
	}
	digest, err = s.blobs.Put(ctx, payload)
	if err != nil {
		return nil, "", err
	}
	return []byte{}, digest, nil
}

func (s *Service) loadBytes(ctx context.Context, inline []byte, digest string) ([]byte, error) {
	digest = strings.TrimSpace(digest)
	if digest == "" {
		return inline, nil
	}
	return s.blobs.Get(ctx, digest)
}

func (s *Service) encodeBlockData(ctx context.Context, block Block, encoded []byte) (inline []byte, digest string, err error) {
	if !shouldSpillBlock(block, encoded) {
		return encoded, "", nil
	}
	_, digest, err = s.spillBytes(ctx, encoded)
	if err != nil {
		return nil, "", err
	}
	return []byte("{}"), digest, nil
}

func (s *Service) decodeBlockJSON(ctx context.Context, inline []byte, digest string) ([]byte, error) {
	return s.loadBytes(ctx, inline, digest)
}

func shouldSpillBlock(block Block, encoded []byte) bool {
	if len(encoded) <= inlinePayloadLimit {
		return false
	}
	if block.Kind == "user" {
		return false
	}
	if block.Kind == "assistant" && (block.State == "" || block.State == "completed") {
		return false
	}
	return true
}

func (s *Service) encodeModelHistory(ctx context.Context, history ModelHistory) ([]byte, error) {
	encoded, err := json.Marshal(history)
	if err != nil {
		return nil, err
	}
	if len(encoded) <= inlinePayloadLimit {
		return encoded, nil
	}
	digest, err := s.blobs.Put(ctx, encoded)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"blob":                   digest,
		"coveredThroughSequence": history.CoveredThroughSequence,
		"generation":             history.Generation,
	})
}

func (s *Service) decodeModelHistory(ctx context.Context, encoded []byte) (ModelHistory, error) {
	var history ModelHistory
	if len(encoded) == 0 || string(encoded) == "{}" {
		return history, nil
	}
	var stub struct {
		Blob string `json:"blob"`
	}
	if err := json.Unmarshal(encoded, &stub); err != nil {
		return ModelHistory{}, err
	}
	if stub.Blob != "" {
		payload, err := s.blobs.Get(ctx, stub.Blob)
		if err != nil {
			return ModelHistory{}, fmt.Errorf("load model history blob: %w", err)
		}
		encoded = payload
	}
	if err := json.Unmarshal(encoded, &history); err != nil {
		return ModelHistory{}, err
	}
	return history, nil
}

func replaceArtifactIDs(payload []byte, ids map[string]string) []byte {
	if len(payload) == 0 || len(ids) == 0 {
		return payload
	}
	text := string(payload)
	replaced := text
	for source, target := range ids {
		replaced = strings.ReplaceAll(replaced, source, target)
	}
	if replaced == text {
		return payload
	}
	return []byte(replaced)
}
