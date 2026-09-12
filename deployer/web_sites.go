package main

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type siteListRow struct {
	Repository string
	URL        string
	Revision   string
	ShortSHA   string
	UpdatedAt  time.Time
	TimeText   string
	TimeISO    string
	Root       bool
}

type sitesPageData struct {
	Username string
	Query    string
	Error    string
	Total    int
	Sites    []siteListRow
	CanScan  bool
	ScanCSRF string
	Scan     ScanStatus
}

var sitesPage = template.Must(template.New("sites").Parse(sitesTemplate))

// HandleSites checks current repository access with the viewer's own OAuth
// token, including organization sites. A session or stored hook is not proof
// of repository access, and public responses must never cache this inventory.
func (h *WebHandler) HandleSites(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Vary", "Cookie")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cookie, _ := r.Cookie(sessionCookieName)
	username := ValidateSession(cookie, h.secret)
	if username == "" {
		h.showStatusLoginPrompt(w, r)
		return
	}
	if h.tokenStore == nil || h.oauthConfig == nil || h.oauthConfig.APIURL == "" {
		http.Error(w, "部署清单暂不可用", http.StatusServiceUnavailable)
		return
	}
	token := h.tokenStore.Get(username)
	if token == nil || token.AccessToken == "" || (!token.ExpiresAt.IsZero() && !token.ExpiresAt.After(time.Now())) {
		h.showStatusLoginPrompt(w, r)
		return
	}
	data := sitesPageData{Username: username, Query: strings.TrimSpace(r.URL.Query().Get("q"))}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if h.scanner != nil {
		data.CanScan, _ = NewGiteaClient(h.oauthConfig.APIURL, token.AccessToken).IsAdministrator(ctx, username)
		if data.CanScan {
			data.ScanCSRF = h.scanCSRF(cookie)
			data.Scan = h.scanner.Snapshot()
		}
	}
	rows, err := h.visibleSites(ctx, NewGiteaClient(h.oauthConfig.APIURL, token.AccessToken))
	status := http.StatusOK
	if err != nil {
		data.Error = "暂时无法读取部署清单或核对仓库权限，请稍后重试；授权失效时请重新授权。"
		status = http.StatusServiceUnavailable
	} else {
		data.Total = len(rows)
		for _, row := range rows {
			if strings.Contains(strings.ToLower(row.Repository+" "+row.URL+" "+row.Revision), strings.ToLower(data.Query)) {
				data.Sites = append(data.Sites, row)
			}
		}
	}
	var body bytes.Buffer
	if err := sitesPage.Execute(&body, data); err != nil {
		http.Error(w, "部署清单渲染失败", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body.Bytes())
	}
}

func (h *WebHandler) visibleSites(ctx context.Context, client *GiteaClient) ([]siteListRow, error) {
	sites, err := listDeployedSites(ctx, h.pagesDir, h.domain, h.metadataKey)
	if err != nil {
		return nil, err
	}
	var rows []siteListRow
	for _, site := range sites {
		names := []string{site.Repository}
		if site.Root && site.Record == nil {
			// Older root sites support both owner.DOMAIN and owner repository names.
			names = append(names, site.Owner)
		}
		for _, name := range names {
			repo, err := client.GetRepoInfoContext(ctx, site.Owner, name)
			var apiErr *GiteaAPIError
			if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusForbidden || apiErr.StatusCode == http.StatusNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if repo == nil || repo.ID <= 0 || !strings.EqualFold(repo.Owner.Username, site.Owner) || !strings.EqualFold(repo.Name, name) {
				return nil, ErrRepositoryMismatch
			}
			if site.Record != nil && repo.ID != site.Record.RepositoryID {
				continue // A recreated repository must not inherit the old inventory.
			}
			path := "/"
			if !site.Root {
				path += strings.ToLower(name) + "/"
			}
			row := siteListRow{
				Repository: repo.Owner.Username + "/" + repo.Name,
				URL:        (&url.URL{Scheme: "https", Host: strings.ToLower(site.Owner) + "." + h.domain, Path: path}).String(),
				Root:       site.Root,
			}
			if site.Record != nil {
				row.Revision = site.Record.Revision
				row.ShortSHA = row.Revision[:12]
				row.UpdatedAt = site.Record.UpdatedAt.UTC()
				row.TimeText = row.UpdatedAt.Format("2006-01-02 15:04:05 UTC")
				row.TimeISO = row.UpdatedAt.Format(time.RFC3339Nano)
			}
			rows = append(rows, row)
			break
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].UpdatedAt.Equal(rows[j].UpdatedAt) {
			return rows[i].Repository < rows[j].Repository
		}
		return rows[i].UpdatedAt.After(rows[j].UpdatedAt)
	})
	return rows, nil
}

const sitesTemplate = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>已部署站点 · Gitea Pages</title><style>
*{box-sizing:border-box}body{margin:0;background:#f9fafb;color:#1f2937;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}.container{max-width:1100px;margin:auto;padding:24px}.header{padding:32px;border-radius:16px;background:linear-gradient(135deg,#3b82f6,#1d4ed8);color:#fff}.header h1{margin:20px 0 12px;font-size:30px}.header p{margin:0;line-height:1.7}.nav{display:flex;gap:20px;flex-wrap:wrap}.nav a{color:#fff}.panel{margin-top:24px;padding:24px;background:#fff;border:1px solid #e5e7eb;border-radius:16px}.toolbar{display:flex;gap:12px;align-items:center;flex-wrap:wrap}form{display:flex;gap:8px;flex:1}input{min-width:0;flex:1;padding:11px;border:1px solid #cbd5e1;border-radius:8px;font:inherit}button,.refresh{padding:11px 16px;border:0;border-radius:8px;background:#2563eb;color:#fff;font:inherit;text-decoration:none;cursor:pointer}label{display:block;margin-bottom:8px;font-weight:600}.count,.muted{color:#64748b;font-size:14px;line-height:1.6}.table-wrap{overflow-x:auto}table{width:100%;border-collapse:collapse;margin:16px 0}th,td{text-align:left;padding:18px 12px;border-bottom:1px solid #e5e7eb;vertical-align:top}th{font-size:13px;color:#64748b}td a{color:#2563eb;overflow-wrap:anywhere}.site-name{font-weight:600}.site-url{display:block;font-size:13px;margin-top:6px}code{background:#eff6ff;border-radius:5px;padding:4px 6px}.empty{padding:40px 12px;text-align:center;color:#64748b}.error{padding:20px;background:#fef2f2;color:#b91c1c;border-radius:8px}.badge{display:inline-block;margin-top:8px;padding:3px 8px;border-radius:20px;background:#f1f5f9;color:#475569;font-size:12px}time{white-space:nowrap;font-size:14px}@media(max-width:600px){.container{padding:12px}.header,.panel{padding:20px}.header h1{font-size:25px}table{min-width:680px}.toolbar form{flex-basis:100%}}
button:disabled{opacity:.6;cursor:wait}.scan-summary{line-height:1.8}.scan-issues{color:#b91c1c;overflow-wrap:anywhere}.scan-panel h2{margin-top:0;font-size:20px}
</style></head><body><main class="container">
<header class="header"><nav class="nav"><a href="/">← 首页</a><a href="/status">授权状态</a></nav><h1>已部署站点</h1><p>{{.Username}} · 仅展示您当前有权访问的仓库站点，按最近更新时间排列。</p></header>
{{if .CanScan}}<section class="panel scan-panel" aria-label="冷启动全量扫描"><h2>冷启动加载</h2><p class="muted">扫描实例中所有已授权用户和组织的 gh-pages 分支，补部署缺失站点，更新版本落后或缺少记录的站点。已是最新版本的站点跳过，没有该分支时保留已有站点。</p><form method="post" action="/sites/scan"><input type="hidden" name="csrf" value="{{.ScanCSRF}}"><button id="scan-button" type="submit" {{if .Scan.Running}}disabled{{end}}>{{if .Scan.Running}}扫描进行中…{{else}}全量扫描并补全部署{{end}}</button></form>
<p id="scan-progress" class="scan-summary" role="status" aria-live="polite">{{if .Scan.StartedAt.IsZero}}尚未执行扫描。{{else}}{{if .Scan.Running}}正在扫描{{else}}扫描结束{{end}} · 作用域 {{.Scan.ScopesDone}} / {{.Scan.Scopes}} · 已检查 {{.Scan.Checked}} 个仓库 · 已部署 {{.Scan.Deployed}} · 已是最新 {{.Scan.Unchanged}} · 无分支 {{.Scan.NoBranch}} · 失败 {{.Scan.Failed}}{{if .Scan.Current}} · 当前：{{.Scan.Current}}{{end}}{{end}}</p>
{{if .Scan.Issues}}<details class="scan-issues" open><summary>失败详情（最多显示 50 条）</summary><ul>{{range .Scan.Issues}}<li>{{.}}</li>{{end}}</ul></details>{{end}}</section>{{end}}
<section class="panel" aria-label="部署清单"><label for="site-search">搜索站点、仓库或版本</label><div class="toolbar"><form action="/sites" method="get"><input id="site-search" name="q" type="search" placeholder="输入名称、地址或 commit" value="{{.Query}}"><button type="submit">搜索</button></form><a class="refresh" href="/sites">刷新清单</a></div>
{{if .Error}}<p class="error" role="alert">{{.Error}} <a href="/oauth/start">重新授权</a></p>{{else}}
<p class="count">共 {{.Total}} 个已部署站点{{if .Query}}，匹配 {{len .Sites}} 个{{end}}</p>
{{if .Sites}}<div class="table-wrap"><table><thead><tr><th scope="col">站点 / 仓库</th><th scope="col">已部署版本</th><th scope="col">更新时间</th></tr></thead><tbody>
{{range .Sites}}<tr><td><span class="site-name">{{.Repository}}</span><a class="site-url" href="{{.URL}}" target="_blank" rel="noopener noreferrer">{{.URL}}</a><span class="badge">{{if .Root}}根站点{{else}}子站点{{end}}</span></td><td>{{if .Revision}}<code title="{{.Revision}}">{{.ShortSHA}}</code><div class="muted">gh-pages</div>{{else}}<span class="muted">未记录</span>{{end}}</td><td>{{if .TimeISO}}<time datetime="{{.TimeISO}}">{{.TimeText}}</time>{{else}}<span class="muted">未记录</span>{{end}}</td></tr>{{end}}
</tbody></table></div>{{else}}<p class="empty">{{if .Query}}没有匹配的站点，请调整搜索条件。{{else}}暂无可查看的已部署站点。向仓库的 gh-pages 分支推送内容并成功部署后，站点将在此显示。{{end}}</p>{{end}}
<p class="muted">版本为实际发布内容的 Git commit；时间为本次成功发布记录的生成时间（UTC）。历史站点缺少记录时显示“未记录”，下一次成功部署后补齐。</p>{{end}}</section></main>
{{if .Scan.Running}}<script>
(function pollScan(){setTimeout(async function(){try{const response=await fetch('/sites/scan',{credentials:'same-origin',cache:'no-store'});if(!response.ok)throw new Error();const scan=await response.json();if(!scan.running){location.reload();return;}document.getElementById('scan-progress').textContent='正在扫描 · 作用域 '+scan.scopesDone+' / '+scan.scopes+' · 已检查 '+scan.checked+' 个仓库 · 已部署 '+scan.deployed+' · 已是最新 '+scan.unchanged+' · 无分支 '+scan.noBranch+' · 失败 '+scan.failed+' · 当前：'+scan.current;pollScan();}catch(error){document.getElementById('scan-progress').textContent='暂时无法获取扫描进度，后台任务仍会继续。请刷新页面重试。';}},3000);})();
</script>{{end}}</body></html>`
