package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRefreshAllTokensRenewsExpiringGrantAndPersistsRotation(t *testing.T) {
	dataDir := t.TempDir()
	store, err := NewTokenStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Set("alice", &UserToken{
		AccessToken: "old-access", RefreshToken: "old-refresh",
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse refresh request: %v", err)
		}
		if got := r.Form.Get("refresh_token"); got != "old-refresh" {
			t.Errorf("refresh token = %q, want old-refresh", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh","token_type":"bearer","expires_in":3600}`))
	}))
	defer server.Close()
	h := NewOAuthHandler(&OAuthConfig{ClientID: "client", ClientSecret: "secret", TokenURL: server.URL}, store, "", "")

	h.RefreshAllTokens()
	h.RefreshAllTokens()
	if got := calls.Load(); got != 1 {
		t.Fatalf("refresh calls = %d, want one renewal before expiry", got)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewTokenStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reloaded.Close() })
	got := reloaded.Get("alice")
	if got == nil || got.AccessToken != "new-access" || got.RefreshToken != "new-refresh" || !got.ExpiresAt.After(time.Now().Add(45*time.Minute)) {
		t.Fatalf("persisted grant was not renewed: %#v", got)
	}
}

func TestRefreshAllTokensRetriesTransientFailureWithoutLosingGrant(t *testing.T) {
	store := newTestTokenStore(t)
	if err := store.Set("alice", &UserToken{
		AccessToken: "old-access", RefreshToken: "old-refresh",
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "temporary outage", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`))
	}))
	defer server.Close()
	h := NewOAuthHandler(&OAuthConfig{ClientID: "client", ClientSecret: "secret", TokenURL: server.URL}, store, "", "")

	h.RefreshAllTokens()
	if got := store.Get("alice"); got.AccessToken != "old-access" || got.RefreshToken != "old-refresh" {
		t.Fatalf("failed refresh changed grant: %#v", got)
	}
	h.RefreshAllTokens()
	if got := store.Get("alice"); got.AccessToken != "new-access" || got.RefreshToken != "new-refresh" {
		t.Fatalf("retry did not renew grant: %#v", got)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("refresh calls = %d, want retry after failure", got)
	}
}

func TestRefreshAllTokensRejectsIncompleteResponseWithoutLosingGrant(t *testing.T) {
	store := newTestTokenStore(t)
	if err := store.Set("alice", &UserToken{
		AccessToken: "old-access", RefreshToken: "old-refresh",
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"","refresh_token":"new-refresh","expires_in":3600}`))
	}))
	defer server.Close()
	h := NewOAuthHandler(&OAuthConfig{ClientID: "client", ClientSecret: "secret", TokenURL: server.URL}, store, "", "")

	h.RefreshAllTokens()
	if got := store.Get("alice"); got.AccessToken != "old-access" || got.RefreshToken != "old-refresh" {
		t.Fatalf("incomplete response replaced usable grant: %#v", got)
	}
}
