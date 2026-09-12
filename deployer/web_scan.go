package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"
)

func (h *WebHandler) scanCSRF(cookie *http.Cookie) string {
	if cookie == nil || h.secret == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(h.secret))
	_, _ = mac.Write([]byte("gitea-pages/full-scan/v1:" + cookie.Value))
	return hex.EncodeToString(mac.Sum(nil))
}

// HandleScan is deliberately instance-admin-only. Neither request parameters
// nor the administrator's token select the credentials used for deployments.
func (h *WebHandler) HandleScan(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cookie, _ := r.Cookie(sessionCookieName)
	username := ValidateSession(cookie, h.secret)
	if username == "" || h.tokenStore == nil {
		http.Error(w, "请先登录", http.StatusUnauthorized)
		return
	}
	if h.scanner == nil || h.oauthConfig == nil {
		http.Error(w, "扫描服务未配置", http.StatusServiceUnavailable)
		return
	}
	token := h.tokenStore.Get(username)
	if token == nil || token.AccessToken == "" || (!token.ExpiresAt.IsZero() && !token.ExpiresAt.After(time.Now())) {
		http.Error(w, "请重新授权", http.StatusUnauthorized)
		return
	}
	admin, err := NewGiteaClient(h.oauthConfig.APIURL, token.AccessToken).IsAdministrator(r.Context(), username)
	if err != nil {
		http.Error(w, "无法核验管理员权限，请稍后重试", http.StatusServiceUnavailable)
		return
	}
	if !admin {
		http.Error(w, "全量扫描仅限 Gitea 管理员", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if r.ParseForm() != nil || !hmac.Equal([]byte(r.PostForm.Get("csrf")), []byte(h.scanCSRF(cookie))) {
			http.Error(w, "页面验证已失效，请刷新后重试", http.StatusForbidden)
			return
		}
		if !h.scanner.Start() {
			http.Error(w, "已有全量扫描正在运行，请返回清单查看进度", http.StatusConflict)
			return
		}
		http.Redirect(w, r, "/sites", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(h.scanner.Snapshot())
}
