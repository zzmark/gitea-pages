# Gitea Pages — current architecture contract

Use this document as the source of truth when changing Gitea Pages. It
supersedes the early prototype design.

## Deployment topology

- Nginx is the only service with a published host port. It terminates the
  public Pages control route `<DOMAIN>` and user subdomains under it.
- Deployer has no published host port. Nginx forwards its OAuth and webhook
  routes to Deployer on the internal `pages_backend` network.
- Deployer can reach Gitea through its dedicated egress network, but has no
  Docker socket, no SSH key, no privileged mode, no Linux capabilities, and a
  read-only root filesystem.
- Nginx mounts Pages data read-only. Deployer alone writes the Pages volume and
  publishes a new site atomically after validation.
- Both containers run as the configured non-root UID/GID, use restrictive
  tmpfs mounts, and have process, CPU, and memory limits.

## Authentication and authority

- A hook key identifies one registered Gitea hook. It does not authenticate a
  delivery by itself.
- Each hook has its own HMAC secret, which authenticates exactly its personal
  or organization scope. Hook credentials must never be shared across scopes.
- Treat every webhook payload as untrusted input. It cannot select an OAuth
  token or establish repository identity.
- Resolve the hook scope and repository through the Gitea API. Canonical Gitea
  metadata—not the payload's owner, repository ID, clone URL, or visibility
  field—is authoritative.
- Treat checked-out repository content as hostile static data: reject unsafe
  site targets and clone transports, prevent symlinks, strip Git metadata and
  executable permissions, apply size/time/concurrency limits, and publish only
  after the complete replacement is ready.

## OAuth and organization hooks

- OAuth grants are encrypted at rest with `TOKEN_ENCRYPTION_KEY_FILE`; session
  and OAuth client secrets are file-mounted Compose secrets, never ordinary
  environment values.
- Personal and organization hook registration is automatic. The approved
  administrator token pool supplies organization authorization when needed.
- `ENABLE_ORGANIZATION_HOOKS=true` is the approved default. Set it to `false`
  only for an installation intentionally restricted to personal repositories.
- Do not add global webhook secrets, payload-selected OAuth tokens, shared
  administrator access tokens, or any fallback HTTP authentication path.

## Unsupported legacy credentials

The runtime accepts only encrypted OAuth grants and per-hook credentials.
Installations using the historical plaintext token database or shared webhook
secret must start with a fresh Deployer data volume and complete OAuth again.
The normal HTTP handler never accepts the retired shared secret.

## Release requirements

The security workflow is mandatory on pull requests and the main branch. It
runs module-local Go race tests and coverage from `deployer/`, `govulncheck`,
Compose topology and containment checks, the non-root Nginx test, and Trivy
configuration/image scans. Do not invoke the historical top-level `tests/`
directory as a Go module; its executable tests are shell policy checks, while
the Go module and end-to-end regression suite live in `deployer/`.

Before a production release, record image digests and successful delivery IDs.
Keep forensic logs for suspected token compromise, forged-hook
attempts, deployment timeouts, or disk exhaustion; preserve existing sites by
stopping Deployer before any cleanup.
