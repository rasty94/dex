package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"google.golang.org/grpc"

	api "github.com/dexidp/dex/api/v2"
	dexvalkey "github.com/dexidp/dex/pkg/valkey"
)

// fakeVersion is a dex API whose GetVersion succeeds or fails on demand.
type fakeVersion struct {
	api.DexClient
	err error
}

func (f fakeVersion) GetVersion(context.Context, *api.VersionReq, ...grpc.CallOption) (*api.VersionResp, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &api.VersionResp{Server: "test"}, nil
}

func probeReadyz(t *testing.T, dexErr error, vk *dexvalkey.Client) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	readyz(&dexClient{api: fakeVersion{err: dexErr}}, vk, testLogger())(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	return rr
}

func TestReadyzWhenEverythingAnswers(t *testing.T) {
	m := miniredis.RunT(t)
	vk, err := dexvalkey.New(t.Context(), dexvalkey.Config{Addresses: []string{m.Addr()}})
	if err != nil {
		t.Fatal(err)
	}
	defer vk.Close()

	if rr := probeReadyz(t, nil, vk); rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200: %s", rr.Code, rr.Body)
	}
	// No Valkey configured is a valid setup, not a missing dependency.
	if rr := probeReadyz(t, nil, nil); rr.Code != http.StatusOK {
		t.Errorf("without valkey: status = %d, want 200", rr.Code)
	}
}

// Every page goes through dex's API, so a panel that cannot reach it, or whose
// token dex rejects, must not get traffic. The reason stays in the log: the
// probe is reachable through the balancer.
func TestReadyzFailsWithoutDex(t *testing.T) {
	rr := probeReadyz(t, errors.New("rpc error: code = Unauthenticated desc = invalid authorization token"), nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "dex") {
		t.Errorf("body %q does not say which dependency failed", body)
	}
	if strings.Contains(body, "token") {
		t.Errorf("body %q leaks the error to anyone who can reach the probe", body)
	}
}

// Sessions live in Valkey when it is configured: without it nobody can sign in.
func TestReadyzFailsWithoutValkey(t *testing.T) {
	m := miniredis.RunT(t)
	vk, err := dexvalkey.New(t.Context(), dexvalkey.Config{Addresses: []string{m.Addr()}})
	if err != nil {
		t.Fatal(err)
	}
	defer vk.Close()
	m.Close()

	rr := probeReadyz(t, nil, vk)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	if body := rr.Body.String(); !strings.Contains(body, "valkey") || strings.Contains(body, "dex") {
		t.Errorf("body %q should name valkey, and only valkey", body)
	}
}
