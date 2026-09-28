package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIndexAuthorizationButtonFollowsSavedGrant(t *testing.T) {
	store := newTestTokenStore(t)
	for _, test := range []struct {
		name       string
		configured bool
		grant      bool
		wantButton bool
	}{
		{"not configured", false, false, false},
		{"configured without grant", true, false, true},
		{"saved grant", true, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.grant {
				if err := store.Set("alice", &UserToken{AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
					t.Fatal(err)
				}
			} else {
				store.Delete("alice")
			}
			var config *OAuthConfig
			if test.configured {
				config = &OAuthConfig{ClientID: "client"}
			}
			h := NewWebHandler(config, store, "pages.test", "secret")
			w := httptest.NewRecorder()
			h.HandleIndex(w, httptest.NewRequest(http.MethodGet, "/", nil))
			if got := strings.Contains(w.Body.String(), `href="/oauth/start"`); got != test.wantButton {
				t.Fatalf("authorization button present = %v, want %v", got, test.wantButton)
			}
			for _, want := range []string{"页面管理运行配置", "后端保存授权", "站点与版本清单", "使用流程"} {
				if !strings.Contains(w.Body.String(), want) {
					t.Errorf("home page missing %q", want)
				}
			}
		})
	}
}
