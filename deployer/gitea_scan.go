package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (c *GiteaClient) scanAPI(ctx context.Context, path string, result any) error {
	if c.apiURL == "" || c.accessToken == "" {
		return ErrRepositoryAccess
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL+"/api/v1"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	response, err := noRedirectHTTPClient(nil, 10*time.Second, ErrUntrustedRepositoryAPI).Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return &GiteaAPIError{StatusCode: response.StatusCode}
	}
	return json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(result)
}

func (c *GiteaClient) IsAdministrator(ctx context.Context, username string) (bool, error) {
	var user struct {
		Login string `json:"login"`
		Admin bool   `json:"is_admin"`
	}
	if err := c.scanAPI(ctx, "/user", &user); err != nil {
		return false, err
	}
	return user.Admin && strings.EqualFold(user.Login, username), nil
}

// ListScopeRepositories requests every page, even when the server clamps the
// requested page size. List responses never choose clone URLs or credentials.
func (c *GiteaClient) ListScopeRepositories(ctx context.Context, principal HookPrincipal) ([]RepoInfo, error) {
	if _, err := validateComponent("scope", principal.ScopeName); err != nil {
		return nil, err
	}
	path := "/user/repos"
	if principal.ScopeType == ScopeOrganization {
		path = "/orgs/" + url.PathEscape(principal.ScopeName) + "/repos"
	} else if principal.ScopeType != ScopeUser {
		return nil, ErrRepositoryOutOfScope
	}
	seen := make(map[int64]bool)
	var repositories []RepoInfo
	for page := 1; ; page++ {
		var batch []RepoInfo
		if err := c.scanAPI(ctx, fmt.Sprintf("%s?page=%d&limit=50", path, page), &batch); err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			return repositories, nil
		}
		fresh := 0
		for _, repo := range batch {
			if repo.ID <= 0 {
				return nil, ErrRepositoryMismatch
			}
			if seen[repo.ID] {
				continue
			}
			seen[repo.ID] = true
			fresh++
			if strings.EqualFold(repo.Owner.Username, principal.ScopeName) {
				repositories = append(repositories, repo)
			}
		}
		if fresh == 0 {
			return nil, errors.New("Gitea repository pagination did not advance")
		}
	}
}

func (c *GiteaClient) PagesBranchRevision(ctx context.Context, owner, repository string) (string, error) {
	var branch struct {
		Name   string `json:"name"`
		Commit struct {
			ID string `json:"id"`
		} `json:"commit"`
	}
	err := c.scanAPI(ctx, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repository)+"/branches/gh-pages", &branch)
	var apiErr *GiteaAPIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if branch.Name != "gh-pages" || !gitRevisionPattern.MatchString(branch.Commit.ID) {
		return "", errors.New("invalid gh-pages branch response")
	}
	return branch.Commit.ID, nil
}
