package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testDeploymentRevision = "0123456789abcdef0123456789abcdef01234567"
const fakeGitRevisionCommand = "if [ \"$1\" = '-C' ]; then printf '" + testDeploymentRevision + "\\n'; exit 0; fi\n"

func createCatalogSite(t *testing.T, root, owner, repo string, record *DeploymentRecord, key []byte) SiteTarget {
	t.Helper()
	target, err := NewSiteTarget(root, owner, repo, "pages.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target.Path(), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target.Path(), "index.html"), []byte("site"), 0644); err != nil {
		t.Fatal(err)
	}
	if record != nil {
		if err := writeDeploymentRecord(target.Path(), *record, key); err != nil {
			t.Fatal(err)
		}
	}
	return target
}

func TestDeploymentCatalog(t *testing.T) {
	key := bytes.Repeat([]byte{9}, 32)
	record := DeploymentRecord{RepositoryID: 1, Owner: "alice", Repository: "blog", Revision: testDeploymentRevision, UpdatedAt: time.Now().UTC()}
	t.Run("部署清单-重读保留版本和时间并兼容历史站点", func(t *testing.T) {
		root := t.TempDir()
		createCatalogSite(t, root, "alice", "blog", &record, key)
		createCatalogSite(t, root, "alice", "alice.pages.test", nil, key)
		if err := os.MkdirAll(filepath.Join(root, "alice", ".staging-unpublished"), 0755); err != nil {
			t.Fatal(err)
		}
		sites, err := listDeployedSites(context.Background(), root, "pages.test", key)
		if err != nil || len(sites) != 2 {
			t.Fatalf("sites = %#v, error = %v", sites, err)
		}
		if !sites[0].Root || sites[0].Record != nil || sites[1].Record == nil || *sites[1].Record != record {
			t.Fatalf("incorrect current or legacy record: %#v", sites)
		}
	})
	t.Run("部署记录-拒绝伪造内容和替换密钥", func(t *testing.T) {
		root := t.TempDir()
		target := createCatalogSite(t, root, "alice", "blog", &record, key)
		if readDeploymentRecord(target.Path(), bytes.Repeat([]byte{8}, 32)) != nil {
			t.Fatal("accepted wrong signing key")
		}
		path := filepath.Join(target.Path(), deploymentMetadataFile)
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		contents = bytes.ReplaceAll(contents, []byte(testDeploymentRevision), []byte(strings.Repeat("f", 40)))
		if err := os.WriteFile(path, contents, 0644); err != nil {
			t.Fatal(err)
		}
		if readDeploymentRecord(target.Path(), key) != nil {
			t.Fatal("accepted forged revision")
		}
	})
	t.Run("部署记录-不能冒充其他站点", func(t *testing.T) {
		root := t.TempDir()
		createCatalogSite(t, root, "bob", "blog", &record, key)
		sites, err := listDeployedSites(context.Background(), root, "pages.test", key)
		if err != nil || len(sites) != 1 || sites[0].Record != nil {
			t.Fatalf("accepted cross-site metadata: %#v, %v", sites, err)
		}
	})
	t.Run("部署记录-仓库不能提供内部记录文件或目录", func(t *testing.T) {
		for _, directory := range []bool{false, true} {
			src, dst := t.TempDir(), t.TempDir()
			path := filepath.Join(src, deploymentMetadataFile)
			if directory {
				if err := os.Mkdir(path, 0755); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte("forged"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := (&GitOperations{maxSiteSizeMB: 1}).copyFiles(src, dst); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dst, deploymentMetadataFile)); !os.IsNotExist(err) {
				t.Fatalf("copied reserved entry: %v", err)
			}
		}
	})
}
