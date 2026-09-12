package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// This reserved file travels with the atomic publication. Its signature keeps
// repository-supplied files (including pre-upgrade files) from forging records.
const deploymentMetadataFile = ".gitea-pages-deployment.json"

var gitRevisionPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

type DeploymentRecord struct {
	RepositoryID int64     `json:"repository_id"`
	Owner        string    `json:"owner"`
	Repository   string    `json:"repository"`
	Revision     string    `json:"revision"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type signedDeploymentRecord struct {
	Record    json.RawMessage `json:"record"`
	Signature []byte          `json:"signature"`
}

func deploymentSignature(key, record []byte) []byte {
	derive := hmac.New(sha256.New, key)
	_, _ = derive.Write([]byte("gitea-pages/deployment-metadata/v1"))
	mac := hmac.New(sha256.New, derive.Sum(nil))
	_, _ = mac.Write(record)
	return mac.Sum(nil)
}

func writeDeploymentRecord(staging string, record DeploymentRecord, key []byte) error {
	if len(key) != 32 {
		return errors.New("deployment metadata requires the token encryption key")
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	envelope, err := json.Marshal(signedDeploymentRecord{data, deploymentSignature(key, data)})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(staging, deploymentMetadataFile), envelope, 0644)
}

func readDeploymentRecord(sitePath string, key []byte) *DeploymentRecord {
	if len(key) != 32 {
		return nil
	}
	root, err := os.OpenRoot(sitePath)
	if err != nil {
		return nil
	}
	defer root.Close()
	info, err := root.Lstat(deploymentMetadataFile)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16*1024 {
		return nil
	}
	file, err := root.Open(deploymentMetadataFile)
	if err != nil {
		return nil
	}
	defer file.Close()
	var envelope signedDeploymentRecord
	if json.NewDecoder(io.LimitReader(file, 16*1024)).Decode(&envelope) != nil ||
		!hmac.Equal(envelope.Signature, deploymentSignature(key, envelope.Record)) {
		return nil
	}
	var record DeploymentRecord
	if json.Unmarshal(envelope.Record, &record) != nil || record.RepositoryID <= 0 ||
		!gitRevisionPattern.MatchString(record.Revision) || record.UpdatedAt.IsZero() {
		return nil
	}
	return &record
}

func checkoutRevision(ctx context.Context, gitBinary, checkout string) (string, error) {
	cmd := exec.CommandContext(ctx, gitBinary, "-C", checkout, "rev-parse", "--verify", "HEAD^{commit}")
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/tmp/gitea-pages-home", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("read deployed Git revision: %w", err)
	}
	revision := strings.TrimSpace(string(output))
	if !gitRevisionPattern.MatchString(revision) {
		return "", errors.New("invalid deployed Git revision")
	}
	return revision, nil
}

type deployedSite struct {
	Owner      string
	Repository string
	Root       bool
	Record     *DeploymentRecord
}

// Only published two-level directories are candidates. Staging directories,
// symlinks and files are excluded; deleted sites disappear without DB cleanup.
func listDeployedSites(ctx context.Context, pagesDir, domain string, key []byte) ([]deployedSite, error) {
	if pagesDir == "" {
		return nil, errors.New("Pages directory is not configured")
	}
	if err := rejectSymlinkedAncestors(pagesDir); err != nil {
		return nil, err
	}
	owners, err := os.ReadDir(pagesDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var sites []deployedSite
	for _, owner := range owners {
		if !owner.IsDir() {
			continue
		}
		if _, err := validateComponent("owner", owner.Name()); err != nil {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(pagesDir, owner.Name()))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			repo := entry.Name()
			if repo == "_root" {
				repo = owner.Name() + "." + domain
			}
			target, err := NewSiteTarget(pagesDir, owner.Name(), repo, domain)
			if err != nil || filepath.Base(target.Path()) != entry.Name() {
				continue
			}
			site := deployedSite{Owner: owner.Name(), Repository: repo, Root: target.IsRoot()}
			record := readDeploymentRecord(target.Path(), key)
			if record != nil {
				recordedTarget, err := NewSiteTarget(pagesDir, record.Owner, record.Repository, domain)
				if err == nil && recordedTarget.Path() == target.Path() {
					site.Record = record
					site.Repository = record.Repository
				}
			}
			sites = append(sites, site)
		}
	}
	return sites, nil
}
