# Repository Guidelines

## Project Structure & Module Organization

- `deployer/` is the Go module: webhook handling, OAuth, Gitea API access, SQLite storage, and atomic site publication. Go tests live beside implementation files as `*_test.go`; `web.go` contains the control-page UI.
- `nginx/` contains the static-serving configuration, startup script, and container image.
- `tests/` contains executable Bash policy/integration checks and `testdata/` fixtures, not a Go module.
- `docker-compose.yml` defines service topology; `.env.example` documents configuration. Read `AI.md` for the architecture contract, `docs/security.md` for release operations, and `examples/quickstart/` for deployment setup.

## Build, Test, and Development Commands

Use the Go version pinned in `.github/workflows/security.yml` (currently 1.26.7). Run shell checks on Linux or WSL with Docker available.

From `deployer/`:

```bash
go build ./...                              # Compile the service
go vet ./...                                # Check common Go mistakes
go test -race -coverprofile=coverage.out ./... # Run tests with race detection and coverage
govulncheck ./...                            # Scan reachable dependency vulnerabilities
```

Install the CI-pinned `govulncheck` version before scanning. From the repository root:

```bash
docker compose --env-file .env.example config --quiet
bash tests/security_release_gate_test.sh
bash tests/compose_security_test.sh
bash tests/nginx_test.sh
docker compose up -d --build
```

These validate configuration, release policy, containment, and Nginx behavior, then build and start the stack. Before starting, copy `.env.example` to `.env`, configure Gitea/domain settings, and create the documented secret files.

## Coding Style & Naming Conventions

Format Go with `gofmt` using tabs and standard Go naming: exported `PascalCase`, unexported `camelCase`, and descriptive underscore-separated filenames. Preserve `_linux.go` and `_unsupported.go` platform boundaries. Match existing YAML indentation; use four-space Bash indentation and `set -euo pipefail`. Use `go vet`; no separate lint configuration is present.

## Testing Guidelines

Use Go's standard `testing` package, descriptive `TestBehavior` names, and table-driven subtests. Add regressions for changed authorization, path handling, publication, or migration behavior. Run on Linux to exercise platform-specific filesystem tests. CI collects coverage without a numeric minimum and requires vulnerability, configuration, and image scans to pass.

## Commit & Pull Request Guidelines

Follow history's Conventional Commit prefixes: `fix:`, `test:`, `refactor:`, `ci:`, and `chore:`. Keep commits focused. PRs should explain the behavior change, link relevant issues, report verification, and include screenshots for control-page changes. Document migration and rollback impacts when applicable.

## Security & Configuration

Keep secrets in file-mounted Compose secrets. Preserve scoped webhook HMAC verification, canonical Gitea metadata, and atomic publication of untrusted static content. Only Nginx publishes a host port; Deployer must remain unprivileged and private.
