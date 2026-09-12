package main

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

type ScanStatus struct {
	Running    bool      `json:"running"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	Scopes     int       `json:"scopes"`
	ScopesDone int       `json:"scopesDone"`
	Checked    int       `json:"checked"`
	Deployed   int       `json:"deployed"`
	Unchanged  int       `json:"unchanged"`
	NoBranch   int       `json:"noBranch"`
	Failed     int       `json:"failed"`
	Current    string    `json:"current"`
	Issues     []string  `json:"issues"`
}

type scanScope struct {
	name       string
	principals []HookPrincipal
}

type scanRepositoryCandidate struct {
	repository RepoInfo
	principal  HookPrincipal
}

type PagesScanner struct {
	mu          sync.Mutex
	status      ScanStatus
	config      *Config
	store       *TokenStore
	verifier    *GiteaRepositoryVerifier
	deployments *DeploymentService
}

func NewPagesScanner(config *Config, store *TokenStore, verifier *GiteaRepositoryVerifier, deployments *DeploymentService) *PagesScanner {
	return &PagesScanner{config: config, store: store, verifier: verifier, deployments: deployments}
}

func (s *PagesScanner) Snapshot() ScanStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.status
	status.Issues = append([]string(nil), status.Issues...)
	return status
}

// Start admits one instance-wide job; disconnecting the browser does not
// cancel work. Re-running after restart safely skips matching publications.
func (s *PagesScanner) Start() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status.Running {
		return false
	}
	s.status = ScanStatus{Running: true, StartedAt: time.Now().UTC()}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
		defer cancel()
		s.run(ctx)
	}()
	return true
}

func (s *PagesScanner) update(update func(*ScanStatus)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	update(&s.status)
}

func (s *PagesScanner) failure(name, message string) {
	s.update(func(status *ScanStatus) {
		status.Failed++
		if len(status.Issues) < 50 {
			status.Issues = append(status.Issues, name+"："+message)
		}
	})
}

// Authorizations are read from the encrypted user store and the explicitly
// registered organization scopes, never inferred from an admin's visibility.
func (s *PagesScanner) scopes(ctx context.Context) ([]scanScope, error) {
	users := s.store.List()
	sort.Strings(users)
	var scopes []scanScope
	for _, user := range users {
		scopes = append(scopes, scanScope{name: user, principals: []HookPrincipal{{Username: user, ScopeType: ScopeUser, ScopeName: user}}})
	}
	if !s.config.EnableOrganizationHooks {
		return scopes, nil
	}
	rows, err := s.store.db.QueryContext(ctx, `
		SELECT scope_name, principal_username FROM hook_credentials WHERE scope_type = 'organization'
		UNION SELECT organization_name, username FROM organization_hook_authorizers
		ORDER BY 1, 2`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var organization *scanScope
	for rows.Next() {
		var name, user string
		if err := rows.Scan(&name, &user); err != nil {
			return nil, err
		}
		if organization == nil || organization.name != name {
			if organization != nil {
				scopes = append(scopes, *organization)
			}
			organization = &scanScope{name: name}
		}
		organization.principals = append(organization.principals, HookPrincipal{Username: user, ScopeType: ScopeOrganization, ScopeName: name})
	}
	if organization != nil {
		scopes = append(scopes, *organization)
	}
	return scopes, rows.Err()
}

func (s *PagesScanner) run(ctx context.Context) {
	defer s.update(func(status *ScanStatus) {
		status.Running = false
		status.Current = ""
		status.FinishedAt = time.Now().UTC()
	})
	scopes, err := s.scopes(ctx)
	if err != nil {
		s.failure("扫描", "无法读取已授权作用域")
		return
	}
	s.update(func(status *ScanStatus) { status.Scopes = len(scopes) })
	for _, scope := range scopes {
		if ctx.Err() != nil {
			s.failure("扫描", "任务超时或已停止，可重新扫描")
			return
		}
		s.update(func(status *ScanStatus) { status.Current = scope.name })
		repositories := make(map[int64]scanRepositoryCandidate)
		loaded := false
		for _, candidate := range scope.principals {
			// Union all approved grants: a still-valid former administrator may
			// now see only part of an organization's private repositories.
			token := s.verifier.usableToken(candidate.Username)
			if token == "" {
				continue
			}
			listed, err := NewGiteaClient(s.config.GiteaAPIURL, token).ListScopeRepositories(ctx, candidate)
			if err == nil {
				loaded = true
				for _, repository := range listed {
					repositories[repository.ID] = scanRepositoryCandidate{repository, candidate}
				}
			}
		}
		if !loaded {
			s.failure(scope.name, "仓库枚举失败，请检查该作用域的授权及 Gitea 连接")
		} else {
			ordered := make([]scanRepositoryCandidate, 0, len(repositories))
			for _, repository := range repositories {
				ordered = append(ordered, repository)
			}
			sort.Slice(ordered, func(i, j int) bool { return ordered[i].repository.Name < ordered[j].repository.Name })
			conflicts := s.ambiguousRoots(ctx, ordered)
			for _, repository := range ordered {
				if ctx.Err() != nil {
					break
				}
				if conflicts[repository.repository.ID] {
					s.update(func(status *ScanStatus) { status.Checked++ })
					s.failure(repository.repository.Owner.Username+"/"+repository.repository.Name, "多个仓库可能对应同一根站点，无法确定归属，已保留现状")
					continue
				}
				s.scanRepository(ctx, repository.principal, repository.repository)
			}
		}
		s.update(func(status *ScanStatus) { status.ScopesDone++ })
	}
	if ctx.Err() != nil {
		s.failure("扫描", "任务超时或已停止，可重新扫描")
	}
}

// Legacy root sites have no trusted repository identity. If both supported
// root aliases contain gh-pages, never let alphabetical scan order pick one.
func (s *PagesScanner) ambiguousRoots(ctx context.Context, candidates []scanRepositoryCandidate) map[int64]bool {
	groups := make(map[string][]scanRepositoryCandidate)
	for _, candidate := range candidates {
		repo := candidate.repository
		target, err := NewSiteTarget(s.config.PagesDir, repo.Owner.Username, repo.Name, s.config.Domain)
		if err == nil && target.IsRoot() && readDeploymentRecord(target.Path(), s.config.TokenEncryptionKey) == nil {
			groups[target.Path()] = append(groups[target.Path()], candidate)
		}
	}
	conflicts := make(map[int64]bool)
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		branches, uncertain := 0, false
		for _, candidate := range group {
			repo := candidate.repository
			verified, err := s.verifier.Verify(ctx, candidate.principal, PayloadRepository{ID: repo.ID, OwnerUsername: repo.Owner.Username, Name: repo.Name})
			if err != nil {
				uncertain = true
				continue
			}
			revision, err := NewGiteaClient(s.config.GiteaAPIURL, verified.AccessToken).PagesBranchRevision(ctx, verified.Owner, verified.Name)
			if err != nil {
				uncertain = true
			} else if revision != "" {
				branches++
			}
		}
		if uncertain || branches > 1 {
			for _, candidate := range group {
				conflicts[candidate.repository.ID] = true
			}
		}
	}
	return conflicts
}

func (s *PagesScanner) scanRepository(ctx context.Context, principal HookPrincipal, candidate RepoInfo) {
	name := candidate.Owner.Username + "/" + candidate.Name
	s.update(func(status *ScanStatus) { status.Checked++; status.Current = name })
	// Validate path components before using listing fields in API requests.
	if _, err := NewSiteTarget(s.config.PagesDir, candidate.Owner.Username, candidate.Name, s.config.Domain); err != nil {
		s.failure(name, "站点路径不受支持或不安全")
		return
	}
	repository, err := s.verifier.Verify(ctx, principal, PayloadRepository{ID: candidate.ID, OwnerUsername: candidate.Owner.Username, Name: candidate.Name})
	if err != nil {
		s.failure(name, "仓库身份或访问权限核验失败")
		return
	}
	target, err := NewSiteTarget(s.config.PagesDir, repository.Owner, repository.Name, s.config.Domain)
	if err != nil {
		s.failure(name, "站点路径不安全")
		return
	}
	result, err := s.deployments.Reconcile(ctx, *repository, target, func(ctx context.Context) (string, error) {
		return NewGiteaClient(s.config.GiteaAPIURL, repository.AccessToken).PagesBranchRevision(ctx, repository.Owner, repository.Name)
	})
	if err != nil {
		message := "检测或部署失败；请检查仓库内容、授权及资源限制"
		if errors.Is(err, ErrScanTargetConflict) {
			message = "发布路径已属于另一仓库，保留现有站点"
		}
		s.failure(name, message)
		return
	}
	s.update(func(status *ScanStatus) {
		switch result {
		case scanDeployed:
			status.Deployed++
		case scanUnchanged:
			status.Unchanged++
		case scanNoBranch:
			status.NoBranch++
		}
	})
}

var ErrScanTargetConflict = errors.New("scan target belongs to another repository")

const (
	scanDeployed  = "deployed"
	scanUnchanged = "unchanged"
	scanNoBranch  = "no-branch"
)

// Compare the branch and deployed record under the SAME target lock used by
// webhooks. The clone also runs under that lock and records its actual HEAD.
func (s *DeploymentService) Reconcile(ctx context.Context, repo VerifiedRepository, target SiteTarget, branchRevision func(context.Context) (string, error)) (string, error) {
	release, err := s.acquire(ctx, target.Path())
	if err != nil {
		return "", err
	}
	defer release()
	if err := s.gitOps.validateTarget(target); err != nil {
		return "", err
	}
	revision, err := branchRevision(ctx)
	if err != nil {
		return "", err
	}
	if revision == "" {
		return scanNoBranch, nil
	}
	record := readDeploymentRecord(target.Path(), s.gitOps.metadataKey)
	if record != nil {
		if record.RepositoryID != repo.ID || !strings.EqualFold(record.Owner, repo.Owner) || !strings.EqualFold(record.Repository, repo.Name) {
			return "", ErrScanTargetConflict
		}
		if record.Revision == revision {
			return scanUnchanged, nil
		}
	}
	if repo.SizeBytes > s.maxRepositorySizeBytes {
		return "", ErrRepositoryTooLarge
	}
	cloneCtx, cancel := context.WithTimeout(ctx, s.cloneTimeout)
	defer cancel()
	if err := s.gitOps.Deploy(cloneCtx, repo, target); err != nil {
		return "", err
	}
	return scanDeployed, nil
}
