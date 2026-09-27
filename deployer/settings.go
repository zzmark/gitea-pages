package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RuntimeSettings are the values an operator may change without restarting Docker.
// They are intentionally stored in plaintext in the local SQLite database.
type RuntimeSettings struct {
	GiteaAPIURL             string `json:"gitea_api_url"`
	GiteaPublicURL          string `json:"gitea_public_url"`
	OAuthClientID           string `json:"oauth_client_id"`
	OAuthClientSecret       string `json:"oauth_client_secret"`
	MaxSiteSizeMB           int64  `json:"max_site_size_mb"`
	MaxRepositorySizeMB     int64  `json:"max_repository_size_mb"`
	MaxConcurrentDeploys    int    `json:"max_concurrent_deploys"`
	CloneTimeout            string `json:"clone_timeout"`
	AcquireTimeout          string `json:"acquire_timeout"`
	EnableOrganizationHooks bool   `json:"enable_organization_hooks"`
}

func settingsFromConfig(c *Config) RuntimeSettings {
	return RuntimeSettings{c.GiteaAPIURL, c.GiteaPublicURL, c.OAuthClientID, c.OAuthClientSecret,
		c.MaxSiteSizeMB, c.MaxRepositorySizeMB, c.MaxConcurrentDeploys, c.CloneTimeout.String(),
		c.AcquireTimeout.String(), c.EnableOrganizationHooks}
}

func (s RuntimeSettings) apply(base *Config) *Config {
	c := *base
	c.GiteaAPIURL, c.GiteaPublicURL = s.GiteaAPIURL, s.GiteaPublicURL
	c.OAuthClientID, c.OAuthClientSecret = s.OAuthClientID, s.OAuthClientSecret
	c.MaxSiteSizeMB, c.MaxRepositorySizeMB = s.MaxSiteSizeMB, s.MaxRepositorySizeMB
	c.MaxConcurrentDeploys, c.EnableOrganizationHooks = s.MaxConcurrentDeploys, s.EnableOrganizationHooks
	c.CloneTimeout, _ = time.ParseDuration(s.CloneTimeout)
	c.AcquireTimeout, _ = time.ParseDuration(s.AcquireTimeout)
	return &c
}

func (s RuntimeSettings) validate(appEnv string) error {
	if err := validateConfiguredURL("GITEA_API_URL", s.GiteaAPIURL, appEnv, false); err != nil {
		return err
	}
	if err := validateConfiguredURL("GITEA_PUBLIC_URL", s.GiteaPublicURL, appEnv, false); err != nil {
		return err
	}
	if s.GiteaAPIURL == "" && (s.GiteaPublicURL != "" || s.OAuthClientID != "" || s.OAuthClientSecret != "") {
		return errors.New("Gitea API URL is required when OAuth fields are set")
	}
	if s.GiteaAPIURL != "" && (s.OAuthClientID == "" || s.OAuthClientSecret == "") {
		return errors.New("OAuth Client ID and Client Secret are required when Gitea API URL is set")
	}
	if s.MaxSiteSizeMB <= 0 || s.MaxRepositorySizeMB <= 0 || s.MaxConcurrentDeploys < 1 || s.MaxConcurrentDeploys > 32 {
		return errors.New("resource limits must be positive and concurrency must be between 1 and 32")
	}
	clone, err := time.ParseDuration(s.CloneTimeout)
	if err != nil || clone < 10*time.Second || clone > 10*time.Minute {
		return errors.New("clone timeout must be between 10s and 10m")
	}
	acquire, err := time.ParseDuration(s.AcquireTimeout)
	if err != nil || acquire <= 0 {
		return errors.New("acquire timeout must be positive")
	}
	return nil
}

func (s *TokenStore) initSettings(defaults RuntimeSettings) ([]byte, RuntimeSettings, error) {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS app_settings (name TEXT PRIMARY KEY, value BLOB NOT NULL)`); err != nil {
		return nil, RuntimeSettings{}, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, RuntimeSettings{}, err
	}
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO app_settings(name,value) VALUES('local_signing_key',?)`, key); err != nil {
		return nil, RuntimeSettings{}, err
	}
	if err := s.db.QueryRow(`SELECT value FROM app_settings WHERE name='local_signing_key'`).Scan(&key); err != nil {
		return nil, RuntimeSettings{}, err
	}
	if len(key) != 32 {
		return nil, RuntimeSettings{}, errors.New("invalid local signing key")
	}
	initial, err := json.Marshal(defaults)
	if err != nil {
		return nil, RuntimeSettings{}, err
	}
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO app_settings(name,value) VALUES('active',?)`, initial); err != nil {
		return nil, RuntimeSettings{}, err
	}
	active, err := s.readSettings("active")
	return key, active, err
}

func (s *TokenStore) readSettings(name string) (RuntimeSettings, error) {
	var raw []byte
	if err := s.db.QueryRow(`SELECT value FROM app_settings WHERE name=?`, name).Scan(&raw); err != nil {
		return RuntimeSettings{}, err
	}
	var value RuntimeSettings
	if err := json.Unmarshal(raw, &value); err != nil {
		return RuntimeSettings{}, err
	}
	return value, nil
}

func (s *TokenStore) writeSettings(name string, value RuntimeSettings) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO app_settings(name,value) VALUES(?,?) ON CONFLICT(name) DO UPDATE SET value=excluded.value`, name, raw)
	return err
}

type appRuntime struct {
	settings RuntimeSettings
	web      *WebHandler
	oauth    *OAuthHandler
	deployer *Deployer
}

type controlApp struct {
	base      *Config
	store     *TokenStore
	current   atomic.Pointer[appRuntime]
	mu        sync.Mutex
	refreshMu sync.Mutex
	routeMu   sync.RWMutex
}

func (a *controlApp) build(settings RuntimeSettings) (*appRuntime, error) {
	c := settings.apply(a.base)
	if c.OAuthRedirectURL == "" {
		c.OAuthRedirectURL = "https://" + c.Domain + "/oauth/callback"
	}
	if c.WebhookPublicURL == "" {
		callback, err := url.Parse(c.OAuthRedirectURL)
		if err != nil {
			return nil, err
		}
		callback.Path, callback.RawQuery, callback.Fragment = "/webhook", "", ""
		c.WebhookPublicURL = callback.String()
	}
	web := NewWebHandler(nil, a.store, c.Domain, string(c.SessionSecret))
	web.pagesDir, web.metadataKey = c.PagesDir, append([]byte(nil), c.MetadataSigningKey...)
	rt := &appRuntime{settings: settings, web: web}
	if c.GiteaAPIURL == "" || c.OAuthClientID == "" || c.OAuthClientSecret == "" {
		return rt, nil
	}
	if err := settings.validate(strings.ToLower(strings.TrimSpace(getEnvOrDefault("APP_ENV", "production")))); err != nil {
		return nil, err
	}
	publicURL := c.GiteaPublicURL
	if publicURL == "" {
		publicURL = c.GiteaAPIURL
	}
	verifier, err := NewRepositoryVerifierWithPublicURL(c.GiteaAPIURL, publicURL, a.store)
	if err != nil {
		return nil, err
	}
	service := NewDeploymentService(c)
	rt.deployer = NewWebhookDeployer(c, a.store, verifier, service)
	web.scanner = NewPagesScanner(c, a.store, verifier, service)
	oauthConfig := oauthConfigFromAppConfig(c)
	web.oauthConfig = oauthConfig
	rt.oauth = NewOAuthHandler(oauthConfig, a.store, c.WebhookPublicURL, string(c.SessionSecret))
	return rt, nil
}

func (a *controlApp) reload() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	draft, err := a.store.readSettings("draft")
	if errors.Is(err, sql.ErrNoRows) {
		draft = a.current.Load().settings
	} else if err != nil {
		return err
	}
	if err := draft.validate(strings.ToLower(strings.TrimSpace(getEnvOrDefault("APP_ENV", "production")))); err != nil {
		return err
	}
	next, err := a.build(draft)
	if err != nil {
		return err
	}
	previous := a.current.Load()
	clear := previous != nil && (previous.settings.GiteaAPIURL != draft.GiteaAPIURL || previous.settings.GiteaPublicURL != draft.GiteaPublicURL || previous.settings.OAuthClientID != draft.OAuthClientID || previous.settings.OAuthClientSecret != draft.OAuthClientSecret)
	a.routeMu.Lock()
	defer a.routeMu.Unlock()
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	if previous != nil && previous.web.scanner != nil {
		previous.web.scanner.Stop()
	}
	if err := a.store.activateSettings(draft, clear); err != nil {
		return err
	}
	a.current.Store(next)
	if next.oauth != nil {
		go a.refreshCurrent()
	}
	return nil
}

func (s *TokenStore) activateSettings(settings RuntimeSettings, clear bool) error {
	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if clear {
		for _, table := range []string{"user_tokens", "organization_hook_authorizers", "hook_credentials", "webhook_deliveries"} {
			if _, err := tx.Exec("DELETE FROM " + table); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(`UPDATE app_settings SET value=? WHERE name='active'`, raw); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if clear {
		s.tokens = make(map[string]*UserToken)
		s.registrationResults = make(map[string]*WebhookRegistrationResult)
	}
	return nil
}

func (a *controlApp) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/config", a.handleConfig)
	mux.HandleFunc("/config/reload", a.handleReload)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		a.routeMu.RLock()
		defer a.routeMu.RUnlock()
		a.current.Load().web.HandleIndex(w, r)
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		a.routeMu.RLock()
		defer a.routeMu.RUnlock()
		a.current.Load().web.HandleStatus(w, r)
	})
	mux.HandleFunc("/sites", func(w http.ResponseWriter, r *http.Request) {
		a.routeMu.RLock()
		defer a.routeMu.RUnlock()
		a.current.Load().web.HandleSites(w, r)
	})
	mux.HandleFunc("/sites/scan", func(w http.ResponseWriter, r *http.Request) {
		a.routeMu.RLock()
		defer a.routeMu.RUnlock()
		a.current.Load().web.HandleScan(w, r)
	})
	mux.HandleFunc("/webhook", func(w http.ResponseWriter, r *http.Request) {
		a.routeMu.RLock()
		defer a.routeMu.RUnlock()
		rt := a.current.Load()
		if rt.deployer == nil {
			http.Error(w, "Gitea is not configured", http.StatusServiceUnavailable)
			return
		}
		rt.deployer.HandleWebhook(w, r)
	})
	mux.HandleFunc("/oauth/start", func(w http.ResponseWriter, r *http.Request) {
		a.routeMu.RLock()
		defer a.routeMu.RUnlock()
		rt := a.current.Load()
		if rt.oauth == nil {
			http.Error(w, "Gitea is not configured", http.StatusServiceUnavailable)
			return
		}
		rt.oauth.HandleStart(w, r)
	})
	mux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		a.routeMu.RLock()
		defer a.routeMu.RUnlock()
		rt := a.current.Load()
		if rt.oauth == nil {
			http.Error(w, "Gitea is not configured", http.StatusServiceUnavailable)
			return
		}
		rt.oauth.HandleAuthorize(w, r)
	})
	mux.HandleFunc("/oauth/callback", func(w http.ResponseWriter, r *http.Request) {
		a.routeMu.RLock()
		defer a.routeMu.RUnlock()
		rt := a.current.Load()
		if rt.oauth == nil {
			http.Error(w, "Gitea is not configured", http.StatusServiceUnavailable)
			return
		}
		rt.oauth.HandleCallback(w, r)
	})
	return mux
}

var configPage = template.Must(template.New("config").Parse(`<!doctype html>
<html lang="zh-CN">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width,initial-scale=1">
    <title>Gitea Pages 配置</title>
    <style>
        body { font: 16px system-ui; max-width: 720px; margin: 2rem auto; padding: 0 1rem; }
        label { display: block; margin: 1rem 0; }
        input { display: block; width: 100%; padding: .5rem; box-sizing: border-box; }
        input[type=checkbox] { width: auto; }
        button { padding: .6rem 1rem; }
        nav a { margin-right: 1rem; }
        .notice { padding: 1rem; background: #eef; }
        code { overflow-wrap: anywhere; }
    </style>
</head>
<body>
    <nav>
        <a href="/">首页</a>
        <a href="/status">状态</a>
        <a href="/sites">站点与版本</a>
    </nav>

    <h1>运行配置</h1>
    <p class="notice">
        当前状态：{{if .Ready}}已连接配置{{else}}待配置 Gitea{{end}}。
        保存后点击“手动重载”才会生效。此页面须由运维限制为管理员访问。
    </p>
    {{if .Message}}
    <p class="notice">{{.Message}}</p>
    {{end}}

    <form method="post" action="/config">
        <label>Gitea API URL
            <input name="gitea_api_url" value="{{.Draft.GiteaAPIURL}}">
        </label>
        <label>Gitea Public URL（留空则同 API）
            <input name="gitea_public_url" value="{{.Draft.GiteaPublicURL}}">
        </label>
        <label>OAuth Client ID
            <input name="oauth_client_id" value="{{.Draft.OAuthClientID}}">
        </label>
        <label>OAuth Client Secret（留空保持当前值）
            <input type="password" name="oauth_client_secret" autocomplete="new-password">
        </label>
        <label>最大站点 MB
            <input type="number" name="max_site_size_mb" value="{{.Draft.MaxSiteSizeMB}}">
        </label>
        <label>最大仓库 MB
            <input type="number" name="max_repository_size_mb" value="{{.Draft.MaxRepositorySizeMB}}">
        </label>
        <label>最大并发部署
            <input type="number" name="max_concurrent_deploys" value="{{.Draft.MaxConcurrentDeploys}}">
        </label>
        <label>克隆超时
            <input name="clone_timeout" value="{{.Draft.CloneTimeout}}">
        </label>
        <label>等待部署槽位超时
            <input name="acquire_timeout" value="{{.Draft.AcquireTimeout}}">
        </label>
        <label>
            <input type="checkbox" name="enable_organization_hooks" {{if .Draft.EnableOrganizationHooks}}checked{{end}}>
            启用组织 Webhook
        </label>
        <button type="submit">保存草稿</button>
    </form>

    <form method="post" action="/config/reload">
        <button type="submit">手动重载</button>
    </form>
</body>
</html>`))

func (a *controlApp) handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form", http.StatusBadRequest)
			return
		}
		a.mu.Lock()
		draft, err := a.store.readSettings("draft")
		if errors.Is(err, sql.ErrNoRows) {
			draft = a.current.Load().settings
		} else if err != nil {
			a.mu.Unlock()
			http.Error(w, "Database error", 500)
			return
		}
		draft.GiteaAPIURL = strings.TrimSpace(r.FormValue("gitea_api_url"))
		draft.GiteaPublicURL = strings.TrimSpace(r.FormValue("gitea_public_url"))
		draft.OAuthClientID = strings.TrimSpace(r.FormValue("oauth_client_id"))
		if r.FormValue("clear_oauth_client_secret") == "on" || draft.OAuthClientID == "" {
			draft.OAuthClientSecret = ""
		} else if secret := r.FormValue("oauth_client_secret"); secret != "" {
			draft.OAuthClientSecret = secret
		}
		parseInt := func(name string) (int64, error) { return strconv.ParseInt(r.FormValue(name), 10, 64) }
		draft.MaxSiteSizeMB, err = parseInt("max_site_size_mb")
		if err == nil {
			draft.MaxRepositorySizeMB, err = parseInt("max_repository_size_mb")
		}
		if err == nil {
			var n int64
			n, err = parseInt("max_concurrent_deploys")
			draft.MaxConcurrentDeploys = int(n)
		}
		draft.CloneTimeout, draft.AcquireTimeout = strings.TrimSpace(r.FormValue("clone_timeout")), strings.TrimSpace(r.FormValue("acquire_timeout"))
		draft.EnableOrganizationHooks = r.FormValue("enable_organization_hooks") == "on"
		if err == nil {
			err = a.store.writeSettings("draft", draft)
		}
		a.mu.Unlock()
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid settings: %v", err), 400)
			return
		}
		http.Redirect(w, r, "/config?saved=1", http.StatusSeeOther)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Method not allowed", 405)
		return
	}
	draft, err := a.store.readSettings("draft")
	if errors.Is(err, sql.ErrNoRows) {
		draft = a.current.Load().settings
	} else if err != nil {
		http.Error(w, "Database error", 500)
		return
	}
	message := ""
	if r.URL.Query().Has("saved") {
		message = "草稿已保存。"
	}
	if r.URL.Query().Has("reloaded") {
		message = "配置已重载。"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = configPage.Execute(w, struct {
		Draft   RuntimeSettings
		Ready   bool
		Message string
	}{draft, a.current.Load().oauth != nil, message})
}

func (a *controlApp) handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method not allowed", 405)
		return
	}
	if err := a.reload(); err != nil {
		http.Error(w, fmt.Sprintf("Reload failed: %v", err), 400)
		return
	}
	http.Redirect(w, r, "/config?reloaded=1", http.StatusSeeOther)
}

func (a *controlApp) refreshLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		a.refreshCurrent()
	}
}

func (a *controlApp) refreshCurrent() {
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	if oauth := a.current.Load().oauth; oauth != nil {
		oauth.RefreshAllTokens()
	}
}
