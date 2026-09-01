package apiserver

import (
	"context"
	"errors"
	"testing"

	"github.com/dexidp/dex/api/v2"
	"github.com/dexidp/dex/storage/memory"
)

// ReloadConfig reports failures in the response rather than as a gRPC error:
// the call itself succeeded, it is the reload that did not, and a caller that
// only checked err would think the new configuration was live.
func TestReloadConfig(t *testing.T) {
	tests := []struct {
		name        string
		reloadFunc  func(context.Context) error
		wantSuccess bool
		wantErrText string
	}{
		{
			name:        "not configured",
			reloadFunc:  nil,
			wantSuccess: false,
			wantErrText: "reload function not configured",
		},
		{
			name:        "reload succeeds",
			reloadFunc:  func(context.Context) error { return nil },
			wantSuccess: true,
		},
		{
			name:        "reload fails",
			reloadFunc:  func(context.Context) error { return errors.New("bad connector") },
			wantSuccess: false,
			wantErrText: "reload failed: bad connector",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logger := newLogger(t)
			d := NewAPI(memory.New(logger), logger, "test", nil, nil, nil, tc.reloadFunc)

			resp, err := d.ReloadConfig(t.Context(), &api.ReloadConfigReq{})
			if err != nil {
				t.Fatalf("ReloadConfig returned a gRPC error, want the failure in the response: %v", err)
			}
			if resp.GetSuccess() != tc.wantSuccess {
				t.Errorf("success: got %v, want %v", resp.GetSuccess(), tc.wantSuccess)
			}
			if resp.GetError() != tc.wantErrText {
				t.Errorf("error: got %q, want %q", resp.GetError(), tc.wantErrText)
			}
		})
	}
}
