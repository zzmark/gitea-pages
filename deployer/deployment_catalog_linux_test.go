//go:build linux

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeploymentCatalogPublication(t *testing.T) {
	t.Run("部署版本-从真实Git工作区读取完整提交", func(t *testing.T) {
		root := t.TempDir()
		for _, args := range [][]string{
			{"init", "-q"},
			{"-c", "user.name=Pages Test", "-c", "user.email=pages@example.test", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", "部署版本测试"},
		} {
			cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git: %v: %s", err, output)
			}
		}
		want, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		got, err := checkoutRevision(context.Background(), "git", root)
		if err != nil || got != strings.TrimSpace(string(want)) {
			t.Fatalf("revision = %q, error = %v, want %s", got, err, want)
		}
	})
	t.Run("部署清单-成功发布记录实际版本且失败保留旧记录删除后消失", func(t *testing.T) {
		root := t.TempDir()
		key := bytes.Repeat([]byte{7}, 32)
		target, err := NewSiteTarget(root, "alice", "blog", "pages.test")
		if err != nil {
			t.Fatal(err)
		}
		g := &GitOperations{pagesDir: root, maxSiteSizeMB: 1, metadataKey: key, gitBinary: securityE2ENormalGit(t, filepath.Join(t.TempDir(), "calls"))}
		repo := VerifiedRepository{ID: 42, Owner: "alice", Name: "blog", CloneURL: mustHTTPSURL(t, "https://gitea.test/alice/blog.git")}
		before := time.Now().UTC()
		if err := g.Deploy(context.Background(), repo, target); err != nil {
			t.Fatal(err)
		}
		record := readDeploymentRecord(target.Path(), key)
		if record == nil || record.RepositoryID != 42 || record.Revision != testDeploymentRevision || record.UpdatedAt.Before(before) || record.UpdatedAt.After(time.Now()) {
			t.Fatalf("invalid successful deployment record: %#v", record)
		}
		g.gitBinary = fakeGitWithSymlink(t)
		if err := g.Deploy(context.Background(), repo, target); err == nil {
			t.Fatal("unsafe deployment succeeded")
		}
		if after := readDeploymentRecord(target.Path(), key); after == nil || *after != *record {
			t.Fatalf("failed deployment changed record: %#v", after)
		}
		if err := g.RemoveSite(target); err != nil {
			t.Fatal(err)
		}
		sites, err := listDeployedSites(context.Background(), root, "pages.test", key)
		if err != nil || len(sites) != 0 {
			t.Fatalf("deleted site remains in inventory: %#v, %v", sites, err)
		}
	})
	t.Run("部署清单-跳过符号链接目录", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Symlink(t.TempDir(), filepath.Join(root, "alice")); err != nil {
			t.Fatal(err)
		}
		sites, err := listDeployedSites(context.Background(), root, "pages.test", nil)
		if err != nil || len(sites) != 0 {
			t.Fatalf("symlink owner was listed: %#v, %v", sites, err)
		}
	})
}
