package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestFetchJWKS_HostHeaderOverride confirms FetchJWKS sends the requested Host header while
// still dialing the URL's own address — the exact behavior a multi-tenant identity provider
// like Zitadel needs to resolve the right instance when reached via a network address (e.g. a
// Kubernetes-internal Service DNS name) that differs from its own configured external domain.
// See FetchJWKS's doc comment for the live bug this guards against.
func TestFetchJWKS_HostHeaderOverride(t *testing.T) {
	var gotHost string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{}})
	}))
	defer srv.Close()

	const wantHost = "localhost:8084"
	if _, err := FetchJWKS(context.Background(), srv.URL, wantHost); err != nil {
		t.Fatalf("FetchJWKS: %v", err)
	}
	if gotHost != wantHost {
		t.Errorf("server saw Host %q, want %q (override not applied)", gotHost, wantHost)
	}
}

// TestFetchJWKS_NoHostOverride confirms an empty hostHeader leaves the natural Host header
// (the URL's own host:port) untouched — the common case for a single-tenant/non-Zitadel IdP,
// or any deployment where the fetch URL already matches the IdP's own external domain.
func TestFetchJWKS_NoHostOverride(t *testing.T) {
	var gotHost string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{}})
	}))
	defer srv.Close()

	if _, err := FetchJWKS(context.Background(), srv.URL, ""); err != nil {
		t.Fatalf("FetchJWKS: %v", err)
	}
	wantHost := srv.Listener.Addr().String()
	if gotHost != wantHost {
		t.Errorf("server saw Host %q, want %q (natural URL host)", gotHost, wantHost)
	}
}
