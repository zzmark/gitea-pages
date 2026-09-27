package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newControlAppForTest(t *testing.T) *controlApp {
	t.Helper()
	t.Setenv("APP_ENV", "development")
	store := newTestTokenStore(t)
	base := &Config{Domain: "pages.test", PagesDir: t.TempDir(), CloneTimeout: time.Minute, AcquireTimeout: 30 * time.Second, MaxSiteSizeMB: 100, MaxRepositorySizeMB: 1024, MaxConcurrentDeploys: 4, EnableOrganizationHooks: true, OAuthRedirectURL: "http://localhost/oauth/callback", WebhookPublicURL: "http://localhost/webhook"}
	key, active, err := store.initSettings(settingsFromConfig(base))
	if err != nil {
		t.Fatal(err)
	}
	base.SessionSecret, base.MetadataSigningKey = key, key
	app := &controlApp{base: base, store: store}
	rt, err := app.build(active)
	if err != nil {
		t.Fatal(err)
	}
	app.current.Store(rt)
	return app
}

func TestControlAppSetupSaveReloadAndRestart(t *testing.T) {
	app := newControlAppForTest(t)
	if app.current.Load().oauth != nil {
		t.Fatal("fresh install unexpectedly configured")
	}
	for _, path := range []string{"/", "/config", "/status"} {
		w := httptest.NewRecorder()
		app.routes().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatalf("%s status=%d", path, w.Code)
		}
	}
	form := url.Values{"gitea_api_url": {"http://gitea:3000"}, "oauth_client_id": {"client"}, "oauth_client_secret": {"secret"}, "max_site_size_mb": {"100"}, "max_repository_size_mb": {"1024"}, "max_concurrent_deploys": {"4"}, "clone_timeout": {"1m"}, "acquire_timeout": {"30s"}, "enable_organization_hooks": {"on"}}
	r := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("save status=%d body=%s", w.Code, w.Body.String())
	}
	if app.current.Load().oauth != nil {
		t.Fatal("draft activated before reload")
	}
	w = httptest.NewRecorder()
	app.routes().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/config/reload", nil))
	if w.Code != http.StatusSeeOther || app.current.Load().oauth == nil {
		t.Fatalf("reload status=%d body=%s", w.Code, w.Body.String())
	}
	page := httptest.NewRecorder()
	app.routes().ServeHTTP(page, httptest.NewRequest("GET", "/config", nil))
	if strings.Contains(page.Body.String(), "value=\"secret\"") {
		t.Fatal("secret exposed in HTML")
	}
	dir := filepath.Dir(app.store.dbPath)
	if err := app.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewTokenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	key, settings, err := reopened.initSettings(RuntimeSettings{})
	if err != nil || len(key) != 32 || settings.OAuthClientID != "client" || settings.OAuthClientSecret != "secret" {
		t.Fatalf("restart settings=%#v error=%v", settings, err)
	}
}

func TestControlAppReloadRejectsIncompleteDraftAndKeepsActive(t *testing.T) {
	app := newControlAppForTest(t)
	if err := app.store.Set("alice", &UserToken{AccessToken: "old"}); err != nil {
		t.Fatal(err)
	}
	draft := app.current.Load().settings
	draft.GiteaAPIURL = "http://gitea:3000"
	if err := app.store.writeSettings("draft", draft); err != nil {
		t.Fatal(err)
	}
	if err := app.reload(); err == nil {
		t.Fatal("incomplete draft activated")
	}
	if app.current.Load().settings.GiteaAPIURL != "" {
		t.Fatal("active settings changed")
	}
	if app.store.Get("alice") == nil {
		t.Fatal("rejected reload cleared grant")
	}
}

func TestControlAppSwitchTargetClearsOldGrant(t *testing.T) {
	app := newControlAppForTest(t)
	settings := app.current.Load().settings
	settings.GiteaAPIURL = "http://gitea:3000"
	settings.OAuthClientID = "client"
	settings.OAuthClientSecret = "secret"
	if err := app.store.writeSettings("draft", settings); err != nil {
		t.Fatal(err)
	}
	if err := app.reload(); err != nil {
		t.Fatal(err)
	}
	if err := app.store.Set("alice", &UserToken{AccessToken: "old", RefreshToken: "refresh"}); err != nil {
		t.Fatal(err)
	}
	settings.GiteaAPIURL = "http://another:3000"
	if err := app.store.writeSettings("draft", settings); err != nil {
		t.Fatal(err)
	}
	if err := app.reload(); err != nil {
		t.Fatal(err)
	}
	if app.store.Get("alice") != nil {
		t.Fatal("old target grant retained")
	}
}
