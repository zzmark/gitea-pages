package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GetCommitUpdatedAtContext reads the committer time of the exact deployed SHA.
// Repository metadata is checked by the caller before this request is made.
func (c *GiteaClient) GetCommitUpdatedAtContext(ctx context.Context, owner, repo, revision string) (time.Time, error) {
	if !gitRevisionPattern.MatchString(revision) {
		return time.Time{}, fmt.Errorf("invalid commit revision")
	}
	endpoint := fmt.Sprintf("%s/api/v1/repos/%s/%s/git/commits/%s",
		c.apiURL, url.PathEscape(owner), url.PathEscape(repo), revision)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return time.Time{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	resp, err := noRedirectHTTPClient(nil, 5*time.Second, ErrUntrustedRepositoryAPI).Do(req)
	if err != nil {
		return time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return time.Time{}, &GiteaAPIError{StatusCode: resp.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		return time.Time{}, fmt.Errorf("invalid commit response size")
	}
	var result struct {
		SHA    string `json:"sha"`
		Commit struct {
			Committer struct {
				Date string `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(data, &result); err != nil || !strings.EqualFold(result.SHA, revision) {
		return time.Time{}, fmt.Errorf("commit response does not match deployed revision")
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, result.Commit.Committer.Date)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid commit timestamp: %w", err)
	}
	return updatedAt.UTC(), nil
}
