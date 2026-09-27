package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTokenStoreGetReturnsCopy(t *testing.T) {
	store := newTestTokenStore(t)
	if err := store.Set("alice", &UserToken{AccessToken: "original"}); err != nil {
		t.Fatal(err)
	}
	got := store.Get("alice")
	got.AccessToken = "modified"
	if store.Get("alice").AccessToken != "original" {
		t.Fatal("store leaked mutable token")
	}
}

func TestTokenStorePersistsPlaintextGrant(t *testing.T) {
	dir := t.TempDir()
	store := newTestTokenStoreAt(t, dir)
	if err := store.Set("alice", &UserToken{AccessToken: "fixture-access-token", RefreshToken: "fixture-refresh-token"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "tokens.db"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "fixture-access-token") {
		t.Fatal("grant was not saved as plaintext")
	}
	reloaded := newTestTokenStoreAt(t, dir)
	if token := reloaded.Get("alice"); token == nil || token.RefreshToken != "fixture-refresh-token" {
		t.Fatalf("token = %#v", token)
	}
}

func TestTokenStoreRefusesOldEncryptedSchema(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "tokens.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE user_tokens_v2 (username TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	store, err := NewTokenStore(dir)
	if err == nil {
		_ = store.Close()
		t.Fatal("old database accepted")
	}
	if !strings.Contains(err.Error(), "remove tokens.db") {
		t.Fatalf("error = %v", err)
	}
}

func TestTokenStoreRefusesUnmarkedPlaintextSchema(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "tokens.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE user_tokens (username TEXT PRIMARY KEY, access_token TEXT NOT NULL, refresh_token TEXT, token_type TEXT, expires_at DATETIME, created_at DATETIME NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	store, err := NewTokenStore(dir)
	if err == nil {
		_ = store.Close()
		t.Fatal("unmarked old database accepted")
	}
	if !strings.Contains(err.Error(), "remove tokens.db") {
		t.Fatalf("error = %v", err)
	}
}

func TestTokenStoreUpdateTokenPersistsRefreshedToken(t *testing.T) {
	dir := t.TempDir()
	store := newTestTokenStoreAt(t, dir)
	if err := store.Set("alice", &UserToken{AccessToken: "old", RefreshToken: "refresh"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateToken("alice", func(token UserToken) UserToken { token.AccessToken = "new"; return token }); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	reloaded := newTestTokenStoreAt(t, dir)
	if token := reloaded.Get("alice"); token == nil || token.AccessToken != "new" || token.RefreshToken != "refresh" {
		t.Fatalf("token = %#v", token)
	}
}

func TestTokenStoreSetReturnsPersistenceFailureWithoutUpdatingMemory(t *testing.T) {
	store := newTestTokenStore(t)
	_ = store.Close()
	if err := store.Set("alice", &UserToken{AccessToken: "access"}); err == nil {
		t.Fatal("closed store accepted write")
	}
	if store.Get("alice") != nil {
		t.Fatal("memory changed after failure")
	}
}

func newTestTokenStore(t *testing.T) *TokenStore {
	t.Helper()
	return newTestTokenStoreAt(t, t.TempDir())
}
func newTestTokenStoreAt(t *testing.T, dir string) *TokenStore {
	t.Helper()
	store, err := NewTokenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
