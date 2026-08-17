package app

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Viking602/azem/internal/provider/errcode"
	"github.com/Viking602/azem/internal/provider/responses"
)

// TestProviderFailureDataCarriesStableErrorCode pins the run_failed event
// contract: terminal provider failures carry the stable taxonomy code under
// Data["errorCode"] so the desktop and TUI never parse error prose.
func TestProviderFailureDataCarriesStableErrorCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want errcode.Code
	}{
		{
			name: "auth API error",
			err:  fmt.Errorf("run turn: %w", &responses.APIError{Kind: responses.ErrorAuthentication, Message: "token expired"}),
			want: errcode.CodeAuth,
		},
		{
			name: "rate limit API error",
			err:  &responses.APIError{Kind: responses.ErrorRateLimit, Message: "slow down"},
			want: errcode.CodeRateLimit,
		},
		{
			name: "unclassified error",
			err:  errors.New("something bespoke"),
			want: errcode.CodeUnknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := providerFailureData(tc.err)
			if got := data[errcode.DataKey]; got != string(tc.want) {
				t.Fatalf("errorCode = %q, want %q", got, tc.want)
			}
		})
	}
}
