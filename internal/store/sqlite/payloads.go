package sqlite

import (
	"context"

	"github.com/Viking602/azem/internal/blobstore"
)

func preparePayload(payload []byte) (inline []byte, digest string) {
	if len(payload) <= inlinePayloadLimit {
		return payload, ""
	}
	return []byte("{}"), blobstore.Sum(payload)
}

func loadPayload(ctx context.Context, blobs blobstore.Store, inline []byte, digest string) ([]byte, error) {
	if digest == "" {
		return inline, nil
	}
	return blobs.Get(ctx, digest)
}
