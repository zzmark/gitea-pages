package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandleSites(t *testing.T) {
	key := bytes.Repeat([]byte{9}, 32)
	secret := "session-secret-for-sites-tests"
	for _, tc := range []struct {
		name       string
		query      string
		noSession  bool
		expired    bool
		apiFailure bool
		empty      bool
		wantStatus int
		want       []string
		unwanted   []string
	}{
		{name: "部署清单-未登录仅显示授权提示", noSession: true, wantStatus: 200, want: []string{"请先授权"}, unwanted: []string{"alice/blog", "team/docs", "bob/private"}},
		{name: "部署清单-过期令牌不能读取清单", expired: true, wantStatus: 200, want: []string{"请先授权"}, unwanted: []string{"alice/blog", "team/docs"}},
		{name: "部署清单-展示本人和有权限组织并隐藏其他仓库", wantStatus: 200, want: []string{"alice/blog", "team/docs", testDeploymentRevision[:12], "2026-09-12 03:04:05 UTC", "https://alice.pages.test/blog/", "未记录"}, unwanted: []string{"bob/private", "alice/recreated", "oauth-test-token"}},
		{name: "部署清单-搜索仓库", query: "docs", wantStatus: 200, want: []string{"team/docs", "匹配 1 个"}, unwanted: []string{"alice/blog", "bob/private"}},
		{name: "部署清单-搜索版本", query: testDeploymentRevision[:12], wantStatus: 200, want: []string{"alice/blog", "匹配 1 个"}, unwanted: []string{"team/docs"}},
		{name: "部署清单-搜索无结果", query: "missing", wantStatus: 200, want: []string{"没有匹配的站点"}, unwanted: []string{"alice/blog"}},
		{name: "部署清单-尚无部署展示空状态", empty: true, wantStatus: 200, want: []string{"暂无可查看的已部署站点", "共 0 个"}},
		{name: "部署清单-转义搜索内容", query: "<script>alert(1)</script>", wantStatus: 200, want: []string{"&lt;script&gt;"}, unwanted: []string{"<script>alert(1)</script>"}},
		{name: "部署清单-权限服务故障不输出部分清单", apiFailure: true, wantStatus: 503, want: []string{"暂时无法读取部署清单"}, unwanted: []string{"alice/blog", "team/docs", "bob/private"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			store := newTestTokenStoreAt(t, t.TempDir())
			t.Cleanup(func() { _ = store.Close() })
			expiresAt := time.Now().Add(time.Hour)
			if tc.expired {
				expiresAt = time.Now().Add(-time.Hour)
			}
			if err := store.Set("alice", &UserToken{AccessToken: "oauth-test-token", ExpiresAt: expiresAt}); err != nil {
				t.Fatal(err)
			}
			updatedAt := time.Date(2026, 9, 12, 3, 4, 5, 0, time.UTC)
			createCatalogSite(t, root, "alice", "blog", &DeploymentRecord{RepositoryID: 1, Owner: "alice", Repository: "blog", Revision: testDeploymentRevision, UpdatedAt: updatedAt}, key)
			createCatalogSite(t, root, "alice", "recreated", &DeploymentRecord{RepositoryID: 99, Owner: "alice", Repository: "recreated", Revision: testDeploymentRevision, UpdatedAt: updatedAt}, key)
			createCatalogSite(t, root, "alice", "alice.pages.test", nil, key)
			createCatalogSite(t, root, "team", "docs", nil, key)
			createCatalogSite(t, root, "bob", "private", nil, key)
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.noSession || tc.expired {
					t.Error("unauthenticated viewer called Gitea")
				}
				if r.Header.Get("Authorization") != "Bearer oauth-test-token" {
					t.Error("request did not use viewer's token")
				}
				if tc.apiFailure {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/repos/"), "/")
				if len(parts) != 2 || parts[0] == "bob" || parts[1] == "alice.pages.test" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				_ = json.NewEncoder(w).Encode(canonicalRepository(1, parts[0], parts[1], "", true))
			}))
			t.Cleanup(api.Close)
			h := NewWebHandler(&OAuthConfig{ClientID: "client", APIURL: api.URL}, store, "pages.test", secret)
			h.pagesDir, h.metadataKey = root, key
			if tc.empty {
				h.pagesDir = t.TempDir()
			}
			r := httptest.NewRequest(http.MethodGet, "/sites", nil)
			query := r.URL.Query()
			query.Set("q", tc.query)
			r.URL.RawQuery = query.Encode()
			if !tc.noSession {
				r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: createTestSessionValue("alice", secret, time.Now().Unix())})
			}
			w := httptest.NewRecorder()
			h.HandleSites(w, r)
			if w.Code != tc.wantStatus || w.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatalf("status = %d, headers = %v", w.Code, w.Header())
			}
			for _, want := range tc.want {
				if !strings.Contains(w.Body.String(), want) {
					t.Errorf("missing %q in response", want)
				}
			}
			for _, unwanted := range tc.unwanted {
				if strings.Contains(w.Body.String(), unwanted) {
					t.Errorf("unexpected %q in response", unwanted)
				}
			}
		})
	}
	t.Run("部署清单-拒绝写请求", func(t *testing.T) {
		w := httptest.NewRecorder()
		NewWebHandler(nil, nil, "pages.test", secret).HandleSites(w, httptest.NewRequest(http.MethodPost, "/sites", nil))
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d", w.Code)
		}
	})
}
