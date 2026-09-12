package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestScanRepositoryPagination(t *testing.T) {
	t.Run("全量扫描-服务端缩小分页仍扫描全部且限制所有者", func(t *testing.T) {
		pages := 0
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			pages++
			if r.Header.Get("Authorization") != "Bearer scope-token" {
				t.Error("wrong scope token")
			}
			var repositories []RepoInfo
			switch r.URL.Query().Get("page") {
			case "1":
				repositories = []RepoInfo{canonicalRepository(1, "alice", "one", "", false)}
			case "2":
				repositories = []RepoInfo{canonicalRepository(2, "other", "foreign", "", false)}
			case "3":
				repositories = []RepoInfo{canonicalRepository(3, "alice", "three", "", false)}
			}
			_ = json.NewEncoder(w).Encode(repositories)
		}))
		defer api.Close()
		repositories, err := NewGiteaClient(api.URL, "scope-token").ListScopeRepositories(context.Background(), HookPrincipal{Username: "alice", ScopeName: "alice", ScopeType: ScopeUser})
		if err != nil || len(repositories) != 2 || pages != 4 {
			t.Fatalf("repositories = %v, pages = %d, error = %v", repositories, pages, err)
		}
	})
	t.Run("全量扫描-拒绝循环分页", func(t *testing.T) {
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode([]RepoInfo{canonicalRepository(1, "alice", "one", "", false)})
		}))
		defer api.Close()
		if _, err := NewGiteaClient(api.URL, "token").ListScopeRepositories(context.Background(), HookPrincipal{ScopeName: "alice", ScopeType: ScopeUser}); err == nil {
			t.Fatal("accepted non-advancing pagination")
		}
	})
}

func TestHandleScanAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name, method                       string
		noSession, nonAdmin, badCSRF, busy bool
		want                               int
	}{
		{name: "扫描入口-未登录拒绝", method: http.MethodPost, noSession: true, want: 401},
		{name: "扫描入口-普通用户不能触发全局任务", method: http.MethodPost, nonAdmin: true, want: 403},
		{name: "扫描入口-普通用户不能查看全局结果", method: http.MethodGet, nonAdmin: true, want: 403},
		{name: "扫描入口-拒绝跨会话伪造请求", method: http.MethodPost, badCSRF: true, want: 403},
		{name: "扫描入口-重复任务被拒绝", method: http.MethodPost, busy: true, want: 409},
		{name: "扫描入口-管理员启动任务并跳转", method: http.MethodPost, want: 303},
		{name: "扫描入口-管理员可查看进度", method: http.MethodGet, want: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestTokenStoreAt(t, t.TempDir())
			defer store.Close()
			if err := store.Set("alice", &UserToken{AccessToken: "admin-token"}); err != nil {
				t.Fatal(err)
			}
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/user" {
					_ = json.NewEncoder(w).Encode(map[string]any{"login": "alice", "is_admin": !tc.nonAdmin})
				} else {
					_, _ = w.Write([]byte("[]"))
				}
			}))
			defer api.Close()
			config := &Config{PagesDir: t.TempDir(), Domain: "pages.test", GiteaAPIURL: api.URL}
			verifier, err := NewRepositoryVerifierWithPublicURL(api.URL, "https://gitea.test", store)
			if err != nil {
				t.Fatal(err)
			}
			scanner := NewPagesScanner(config, store, verifier, NewDeploymentService(config))
			scanner.status.Running = tc.busy
			h := NewWebHandler(&OAuthConfig{APIURL: api.URL}, store, "pages.test", "scan-test-secret")
			h.scanner = scanner
			cookie := &http.Cookie{Name: sessionCookieName, Value: createTestSessionValue("alice", h.secret, time.Now().Unix())}
			csrf := h.scanCSRF(cookie)
			if tc.badCSRF {
				csrf = h.scanCSRF(&http.Cookie{Value: "another-session"})
			}
			r := httptest.NewRequest(tc.method, "/sites/scan", strings.NewReader(url.Values{"csrf": {csrf}}.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if !tc.noSession {
				r.AddCookie(cookie)
			}
			w := httptest.NewRecorder()
			h.HandleScan(w, r)
			if w.Code != tc.want {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatal("scan result is cacheable")
			}
			if tc.want == http.StatusSeeOther {
				if w.Header().Get("Location") != "/sites" {
					t.Fatal("missing redirect")
				}
				deadline := time.Now().Add(2 * time.Second)
				for scanner.Snapshot().Running && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if scanner.Snapshot().Running {
					t.Fatal("scan did not finish")
				}
			}
		})
	}
}

func TestScanSharesDeploymentLock(t *testing.T) {
	t.Run("全量扫描-发布路径属于另一仓库时保留现有站点", func(t *testing.T) {
		key := bytes.Repeat([]byte{9}, 32)
		config := &Config{PagesDir: t.TempDir(), Domain: "pages.test", TokenEncryptionKey: key, MaxConcurrentDeploys: 1, AcquireTimeout: time.Second}
		record := DeploymentRecord{RepositoryID: 1, Owner: "alice", Repository: "alice.pages.test", Revision: testDeploymentRevision, UpdatedAt: time.Now().UTC()}
		target := createCatalogSite(t, config.PagesDir, "alice", "alice.pages.test", &record, key)
		_, err := NewDeploymentService(config).Reconcile(context.Background(), VerifiedRepository{ID: 2, Owner: "alice", Name: "alice"}, target, func(context.Context) (string, error) { return testDeploymentRevision, nil })
		if err != ErrScanTargetConflict {
			t.Fatalf("error = %v", err)
		}
		if got := readDeploymentRecord(target.Path(), key); got == nil || *got != record {
			t.Fatal("conflicting deployment changed existing record")
		}
	})
	t.Run("全量扫描-取得Webhook共享锁后重新检查版本", func(t *testing.T) {
		key := bytes.Repeat([]byte{9}, 32)
		config := &Config{PagesDir: t.TempDir(), Domain: "pages.test", TokenEncryptionKey: key, MaxConcurrentDeploys: 1, AcquireTimeout: time.Second}
		service := NewDeploymentService(config)
		target := createCatalogSite(t, config.PagesDir, "alice", "site", nil, key)
		release, err := service.limiter.Acquire(context.Background(), target.Path())
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		checked := make(chan struct{})
		result := make(chan string, 1)
		go func() {
			status, err := service.Reconcile(context.Background(), VerifiedRepository{ID: 1, Owner: "alice", Name: "site"}, target, func(context.Context) (string, error) { close(checked); return testDeploymentRevision, nil })
			if err != nil {
				result <- err.Error()
			} else {
				result <- status
			}
		}()
		select {
		case <-checked:
			t.Fatal("checked branch before obtaining target lock")
		case <-time.After(20 * time.Millisecond):
		}
		if err := writeDeploymentRecord(target.Path(), DeploymentRecord{RepositoryID: 1, Owner: "alice", Repository: "site", Revision: testDeploymentRevision, UpdatedAt: time.Now().UTC()}, key); err != nil {
			t.Fatal(err)
		}
		release()
		select {
		case status := <-result:
			if status != scanUnchanged {
				t.Fatalf("status = %s", status)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("scan remained blocked")
		}
	})
}

func TestScanPanel(t *testing.T) {
	for _, tc := range []struct {
		name           string
		admin, running bool
	}{
		{"扫描页面-管理员可见按钮", true, false},
		{"扫描页面-普通用户不可见全局操作", false, false},
		{"扫描页面-运行时禁用按钮并展示进度", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestTokenStoreAt(t, t.TempDir())
			defer store.Close()
			if err := store.Set("alice", &UserToken{AccessToken: "token"}); err != nil {
				t.Fatal(err)
			}
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"login": "alice", "is_admin": tc.admin})
			}))
			defer api.Close()
			h := NewWebHandler(&OAuthConfig{APIURL: api.URL}, store, "pages.test", "test-secret")
			h.pagesDir = t.TempDir()
			h.scanner = &PagesScanner{status: ScanStatus{Running: tc.running, StartedAt: time.Now().UTC(), Checked: 12, Deployed: 3}}
			request := httptest.NewRequest(http.MethodGet, "/sites", nil)
			cookie := &http.Cookie{Name: sessionCookieName, Value: createTestSessionValue("alice", h.secret, time.Now().Unix())}
			request.AddCookie(cookie)
			w := httptest.NewRecorder()
			h.HandleSites(w, request)
			if w.Code != 200 {
				t.Fatalf("status = %d", w.Code)
			}
			if visible := strings.Contains(w.Body.String(), `action="/sites/scan"`); visible != tc.admin {
				t.Fatalf("scan button visibility = %v", visible)
			}
			if tc.admin && !strings.Contains(w.Body.String(), h.scanCSRF(cookie)) {
				t.Fatal("missing session-bound form token")
			}
			if tc.running && (!strings.Contains(w.Body.String(), "disabled") || !strings.Contains(w.Body.String(), "已检查 12 个仓库") || !strings.Contains(w.Body.String(), "pollScan")) {
				t.Fatal("missing running state")
			}
		})
	}
}
