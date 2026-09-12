//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestFullScanReconcilesAuthorizedScopes(t *testing.T) {
	t.Run("全量扫描-跨用户组织补齐更新跳过失败隔离且重复执行幂等", func(t *testing.T) {
		key := bytes.Repeat([]byte{9}, 32)
		store := newTestTokenStoreAt(t, t.TempDir())
		defer store.Close()
		for _, user := range []string{"alice", "bob", "limited", "orgadmin", "revoked"} {
			if err := store.Set(user, &UserToken{AccessToken: user + "-token"}); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.PutHook(context.Background(), HookCredential{Key: "team-hook", Secret: []byte("secret"), ScopeType: ScopeOrganization, ScopeName: "team", PrincipalUsername: "revoked", GiteaHookID: 1}); err != nil {
			t.Fatal(err)
		}
		if err := store.PutOrganizationHookAuthorizer(context.Background(), "team", "orgadmin", "team-hook"); err != nil {
			t.Fatal(err)
		}
		if err := store.PutOrganizationHookAuthorizer(context.Background(), "team", "limited", "team-hook"); err != nil {
			t.Fatal(err)
		}
		repositories := []RepoInfo{
			canonicalRepository(1, "alice", "missing", "https://attacker.invalid/wrong.git", true),
			canonicalRepository(2, "alice", "legacy", "", true),
			canonicalRepository(3, "alice", "outdated", "", true),
			canonicalRepository(4, "alice", "current", "", true),
			canonicalRepository(5, "alice", "no-pages", "", true),
			canonicalRepository(6, "alice", "bad", "", true),
			canonicalRepository(7, "bob", "site", "", true),
			canonicalRepository(8, "team", "docs", "", true),
			canonicalRepository(9, "team", "private", "", true),
			canonicalRepository(10, "alice", "alice", "", true),
			canonicalRepository(11, "alice", "alice.pages.test", "", true),
		}
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if token == "revoked-token" {
				w.WriteHeader(401)
				return
			}
			if r.URL.Path == "/api/v1/user/repos" || r.URL.Path == "/api/v1/orgs/team/repos" {
				owner := strings.TrimSuffix(token, "-token")
				if r.URL.Path == "/api/v1/orgs/team/repos" {
					if token != "orgadmin-token" && token != "limited-token" {
						t.Errorf("unexpected organization authorizer %q", token)
					}
					owner = "team"
				}
				var owned []RepoInfo
				for _, repo := range repositories {
					if token == "limited-token" && repo.Name == "private" {
						continue
					}
					if repo.Owner.Username == owner {
						owned = append(owned, repo)
					}
				}
				page, _ := strconv.Atoi(r.URL.Query().Get("page"))
				// Deliberately clamp pages to two rows to exercise pagination.
				start := (page - 1) * 2
				var batch []RepoInfo
				if start >= 0 && start < len(owned) {
					end := start + 2
					if end > len(owned) {
						end = len(owned)
					}
					batch = owned[start:end]
				}
				_ = json.NewEncoder(w).Encode(batch)
				return
			}
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/repos/"), "/")
			if len(parts) < 2 {
				w.WriteHeader(404)
				return
			}
			var repository *RepoInfo
			for i := range repositories {
				if repositories[i].Owner.Username == parts[0] && repositories[i].Name == parts[1] {
					repository = &repositories[i]
				}
			}
			if repository == nil {
				w.WriteHeader(404)
				return
			}
			expected := parts[0] + "-token"
			if parts[0] == "team" {
				expected = "orgadmin-token"
			}
			if token != expected {
				t.Errorf("cross-scope token for %s: %s", r.URL.Path, token)
				w.WriteHeader(403)
				return
			}
			if len(parts) == 2 {
				_ = json.NewEncoder(w).Encode(repository)
				return
			}
			if parts[1] == "no-pages" {
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "gh-pages", "commit": map[string]string{"id": testDeploymentRevision}})
		}))
		defer api.Close()
		config := &Config{PagesDir: t.TempDir(), Domain: "pages.test", GiteaAPIURL: api.URL, TokenEncryptionKey: key, EnableOrganizationHooks: true, MaxConcurrentDeploys: 2, AcquireTimeout: time.Second, CloneTimeout: time.Second, MaxRepositorySizeMB: 10, MaxSiteSizeMB: 10}
		verifier, err := NewRepositoryVerifierWithPublicURL(api.URL, "https://gitea.test", store)
		if err != nil {
			t.Fatal(err)
		}
		service := NewDeploymentService(config)
		gitBinary := filepath.Join(t.TempDir(), "git")
		script := "#!/bin/sh\n" + fakeGitRevisionCommand + "bad=false\nfor arg do\n case \"$arg\" in https://gitea.test/alice/bad.git) bad=true;; https://attacker.invalid/*) exit 88;; esac\n target=$arg\ndone\nmkdir -p \"$target\"\nprintf 'published' > \"$target/index.html\"\nif [ \"$bad\" = true ]; then ln -s /etc/passwd \"$target/leak\"; fi\n"
		if err := os.WriteFile(gitBinary, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		service.gitOps.gitBinary = gitBinary
		oldTime := time.Now().Add(-time.Hour).UTC()
		var current SiteTarget
		for _, repo := range repositories {
			if repo.Name == "missing" || repo.Owner.Username != "alice" {
				continue
			}
			var record *DeploymentRecord
			if repo.Name == "outdated" || repo.Name == "current" {
				sha := testDeploymentRevision
				if repo.Name == "outdated" {
					sha = strings.Repeat("f", 40)
				}
				record = &DeploymentRecord{RepositoryID: repo.ID, Owner: repo.Owner.Username, Repository: repo.Name, Revision: sha, UpdatedAt: oldTime}
			}
			target := createCatalogSite(t, config.PagesDir, repo.Owner.Username, repo.Name, record, key)
			if repo.Name == "current" {
				current = target
			}
		}
		scanner := NewPagesScanner(config, store, verifier, service)
		scanner.run(context.Background())
		status := scanner.Snapshot()
		if status.Checked != 11 || status.Deployed != 6 || status.Unchanged != 1 || status.NoBranch != 1 || status.Failed != 4 {
			t.Fatalf("unexpected scan result: %+v", status)
		}
		for _, repo := range repositories {
			target, err := NewSiteTarget(config.PagesDir, repo.Owner.Username, repo.Name, config.Domain)
			if err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(filepath.Join(target.Path(), "index.html"))
			if err != nil {
				t.Fatal(err)
			}
			if repo.Name == "bad" || repo.Name == "no-pages" || repo.Name == "current" || target.IsRoot() {
				if string(content) != "site" {
					t.Fatalf("existing %s was changed", repo.Name)
				}
			} else if string(content) != "published" {
				t.Fatalf("%s was not published", repo.Name)
			}
		}
		if record := readDeploymentRecord(current.Path(), key); record == nil || !record.UpdatedAt.Equal(oldTime) {
			t.Fatal("unchanged deployment time was rewritten")
		}
		second := NewPagesScanner(config, store, verifier, service)
		second.run(context.Background())
		if status := second.Snapshot(); status.Deployed != 0 || status.Unchanged != 7 || status.Failed != 4 {
			t.Fatalf("repeated scan is not idempotent: %+v", status)
		}
	})
}
