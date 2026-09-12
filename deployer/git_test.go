package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestGitCloneDisablesInitialHTTPRedirects(t *testing.T) {
	gitBinary := filepath.Join(t.TempDir(), "git")
	script := `#!/bin/sh
redirects_disabled=false
for argument in "$@"; do
  if [ "$argument" = "http.followRedirects=false" ]; then
    redirects_disabled=true
  fi
  if [ "$argument" = "clone" ]; then
    [ "$redirects_disabled" = "true" ] && exit 0
    exit 42
  fi
done
exit 43
`
	if err := os.WriteFile(gitBinary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	cloneURL := mustHTTPSURL(t, "https://gitea.example.com/alice/site.git")
	if err := runGitClone(context.Background(), gitBinary, cloneURL, filepath.Join(t.TempDir(), "repository"), ""); err != nil {
		t.Fatalf("runGitClone() error = %v, want redirects disabled before clone", err)
	}
}

// This test fails if an authenticated clone creates its askpass executable in
// the temporary checkout directory, which is mounted noexec in production.
func TestGitCloneUsesImageAskPassForToken(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "askpass-path")
	gitBinary := filepath.Join(t.TempDir(), "git")
	script := "#!/bin/sh\nprintf '%s' \"$GIT_ASKPASS\" > " + shellQuote(marker) + "\n[ \"$" + gitAskPassTokenEnv + "\" = token-value ]\n"
	if err := os.WriteFile(gitBinary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}

	cloneURL := mustHTTPSURL(t, "https://gitea.example.com/alice/site.git")
	if err := runGitClone(context.Background(), gitBinary, cloneURL, filepath.Join(t.TempDir(), "repository"), "token-value"); err != nil {
		t.Fatalf("runGitClone() error = %v", err)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if want := "/usr/local/lib/gitea-pages/git-askpass"; string(got) != want {
		t.Fatalf("GIT_ASKPASS = %q, want image helper %q", got, want)
	}
}

// This would fail if a set-ID or sticky checkout entry were normalized and
// published instead of rejected before it reaches staging.
func TestCopyFilesRejectsSetIDAndStickyModes(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode os.FileMode
		dir  bool
	}{
		{"setuid file", os.ModeSetuid, false},
		{"setgid file", os.ModeSetgid, false},
		{"sticky directory", os.ModeSticky, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, dst := t.TempDir(), t.TempDir()
			path := filepath.Join(src, "entry")
			if tc.dir {
				if err := os.Mkdir(path, 0755); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte("content"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0755|tc.mode); err != nil {
				t.Fatal(err)
			}
			info, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode()&tc.mode == 0 {
				t.Fatalf("test filesystem did not retain requested mode %v: got %v", tc.mode, info.Mode())
			}

			err = (&GitOperations{maxSiteSizeMB: 1}).copyFiles(src, dst)
			if !errors.Is(err, ErrUnsafeCheckoutContent) {
				t.Fatalf("expected unsafe content rejection, got %v", err)
			}
		})
	}
}

func TestCopyFilesHiddenEntries(t *testing.T) {
	for _, tc := range []struct {
		name    string
		path    string
		exclude bool
	}{
		{name: "隐藏目录-复制普通文件", path: ".github/workflows/pages.yml"},
		{name: "隐藏目录-复制嵌套隐藏目录", path: "assets/.cache/.nested/index.html"},
		{name: "隐藏目录-保留原有允许目录", path: ".well-known/token"},
		{name: "隐藏文件-复制站点标记", path: ".nojekyll"},
		{name: "隐藏文件-复制任意名称", path: ".custom-pages-data"},
		{name: "隐藏文件-复制根目录文件", path: ".env"},
		{name: "隐藏文件-复制隐藏目录内文件", path: ".config/.env"},
		{name: "隐藏文件-复制Git配置文件", path: ".gitignore"},
		{name: "Git元数据-排除元数据文件", path: ".git", exclude: true},
		{name: "Git元数据-排除根目录", path: ".git/config", exclude: true},
		{name: "Git元数据-排除隐藏目录内元数据", path: ".cache/.git/config", exclude: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, dst := t.TempDir(), t.TempDir()
			relPath := filepath.FromSlash(tc.path)
			source := filepath.Join(src, relPath)
			if err := os.MkdirAll(filepath.Dir(source), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source, []byte("site content"), 0644); err != nil {
				t.Fatal(err)
			}

			err := (&GitOperations{maxSiteSizeMB: 1}).copyFiles(src, dst)
			if err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(filepath.Join(dst, relPath))
			if tc.exclude {
				if !os.IsNotExist(err) {
					t.Fatalf("excluded content was published: %v", err)
				}
			} else if err != nil || string(content) != "site content" {
				t.Fatalf("copied content = %q, error = %v", content, err)
			}
		})
	}
}

func TestRemoveGitDir(t *testing.T) {
	// Create temp directory with .git folder
	tempDir := t.TempDir()
	gitDir := filepath.Join(tempDir, ".git")

	// Test: .git doesn't exist
	err := RemoveGitDir(gitDir)
	if err != nil {
		t.Errorf("RemoveGitDir on non-existent dir should succeed: %v", err)
	}

	// Create .git directory
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatalf("Failed to create .git dir: %v", err)
	}

	// Add some files inside .git
	testFile := filepath.Join(gitDir, "config")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Test: remove existing .git
	err = RemoveGitDir(gitDir)
	if err != nil {
		t.Errorf("RemoveGitDir failed: %v", err)
	}

	// Verify .git is removed
	if _, err := os.Stat(gitDir); !os.IsNotExist(err) {
		t.Errorf(".git directory should be removed")
	}
}

func TestNewGitOperations(t *testing.T) {
	config := &Config{
		PagesDir:      "/var/www/pages",
		MaxSiteSizeMB: 100,
	}
	gitOps := NewGitOperations(config)

	if gitOps.pagesDir != "/var/www/pages" {
		t.Errorf("Expected pagesDir /var/www/pages, got %s", gitOps.pagesDir)
	}
	if gitOps.maxSiteSizeMB != 100 {
		t.Errorf("Expected maxSiteSizeMB 100, got %d", gitOps.maxSiteSizeMB)
	}
}

func TestCalculateDirSize(t *testing.T) {
	// Create temp directory with known files
	tempDir := t.TempDir()

	// Create files with known sizes
	file1 := filepath.Join(tempDir, "file1.txt")
	file2 := filepath.Join(tempDir, "file2.txt")
	subDir := filepath.Join(tempDir, "subdir")
	file3 := filepath.Join(subDir, "file3.txt")

	// Create files
	os.WriteFile(file1, []byte("12345"), 0644) // 5 bytes
	os.WriteFile(file2, []byte("789"), 0644)   // 3 bytes
	os.Mkdir(subDir, 0755)
	os.WriteFile(file3, []byte("abcdef"), 0644) // 6 bytes

	// Calculate size
	size, err := CalculateDirSize(tempDir)
	if err != nil {
		t.Errorf("CalculateDirSize failed: %v", err)
	}

	expectedSize := int64(5 + 3 + 6) // 14 bytes
	if size != expectedSize {
		t.Errorf("Expected size %d, got %d", expectedSize, size)
	}
}

func TestCalculateDirSizeWithSymlink(t *testing.T) {
	tempDir := t.TempDir()

	// Create regular file
	file := filepath.Join(tempDir, "regular.txt")
	os.WriteFile(file, []byte("content"), 0644)

	// Create symlink (should be skipped in size calculation)
	symlink := filepath.Join(tempDir, "link")
	os.Symlink(file, symlink)

	size, err := CalculateDirSize(tempDir)
	if err != nil {
		t.Errorf("CalculateDirSize failed: %v", err)
	}

	// Should only count the regular file (7 bytes)
	if size != 7 {
		t.Errorf("Expected size 7 (symlink skipped), got %d", size)
	}
}
