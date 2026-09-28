#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

ci_workflow=.github/workflows/security.yml
publish_workflow=.github/workflows/publish.yml
security_doc=docs/security.md

workflow_count=$(find .github/workflows -maxdepth 1 -type f -name '*.yml' | wc -l | tr -d ' ')
if test "$workflow_count" -ne 2 || test ! -f "$ci_workflow" || test ! -f "$publish_workflow"; then
    printf 'the repository must expose separate PR verification and push publishing workflows\n' >&2
    exit 1
fi

require_file() {
    test -f "$1" || { printf 'missing required file: %s\n' "$1" >&2; exit 1; }
}

require_text() {
    local file=$1
    local text=$2
    grep -Fq -- "$text" "$file" || { printf 'missing %q in %s\n' "$text" "$file" >&2; exit 1; }
}

require_file "$ci_workflow"
require_file "$publish_workflow"
require_file "$security_doc"
if test -e tests/integration_test.go; then
    printf 'historical non-module Go test harness must not remain in tests/\n' >&2
    exit 1
fi

# Third-party actions must use immutable commit references, not mutable tags.
if grep -E '^ *uses: [^@]+@v?[0-9]+(\.[0-9]+)*([[:space:]]*(#.*)?)$' "$ci_workflow" "$publish_workflow"; then
    printf 'workflow contains a mutable action reference\n' >&2
    exit 1
fi
for workflow in "$ci_workflow" "$publish_workflow"; do
    grep -Eq '^ *uses: [^@]+@[0-9a-f]{40}( +#.*)?$' "$workflow" || {
        printf 'workflow contains no commit-pinned action reference: %s\n' "$workflow" >&2
        exit 1
    }
done

require_text "$ci_workflow" 'pull_request:'
require_text "$ci_workflow" 'branches: [main]'
require_text "$ci_workflow" 'GOVULNCHECK_VERSION:'
require_text "$ci_workflow" 'TRIVY_VERSION:'
require_text "$ci_workflow" 'working-directory: deployer'
require_text "$ci_workflow" 'go test -race -coverprofile=coverage.out ./...'
require_text "$ci_workflow" 'govulncheck ./...'
require_text "$ci_workflow" 'docker compose --env-file .env.example config --quiet'
require_text "$ci_workflow" 'bash tests/compose_security_test.sh'
require_text "$ci_workflow" 'bash tests/nginx_test.sh'
require_text "$ci_workflow" 'trivy config --exit-code 1 --severity HIGH,CRITICAL .'
require_text "$ci_workflow" 'DEPLOYER_IMAGE: gitea-pages-deployer'
require_text "$ci_workflow" 'NGINX_IMAGE: gitea-pages-nginx'
require_text "$ci_workflow" 'trivy image --exit-code 1 --severity CRITICAL ${DEPLOYER_IMAGE}:${GITHUB_SHA}'
require_text "$ci_workflow" 'trivy image --exit-code 1 --severity CRITICAL ${NGINX_IMAGE}:${GITHUB_SHA}'
require_text "$publish_workflow" 'push:'
require_text "$publish_workflow" 'branches: [main]'
require_text "$publish_workflow" "tags: ['**']"
require_text "$publish_workflow" 'packages: write'
require_text "$publish_workflow" 'docker buildx build --platform linux/amd64,linux/arm64 --push'
if grep -Eq '^  push:|packages: write|docker buildx build .* --push' "$ci_workflow"; then
    printf 'PR verification workflow must not publish images\n' >&2
    exit 1
fi
if grep -Eq '^  pull_request:|go test |govulncheck |trivy ' "$publish_workflow"; then
    printf 'publishing workflow must not run PR verification\n' >&2
    exit 1
fi
if grep -Eq 'codecov/|metadata-action|build-push-action|setup-qemu-action|setup-buildx-action|login-action' "$ci_workflow" "$publish_workflow"; then
    printf 'workflow retains redundant third-party build, metadata, or coverage actions\n' >&2
    exit 1
fi
if grep -Fq 'cd tests' "$ci_workflow" "$publish_workflow"; then
    printf 'workflow invokes the historical non-module tests directory\n' >&2
    exit 1
fi

for text in \
    'hook key is an identifier' \
    'hook secret authenticates one scope' \
    'OAuth tokens never come from payload identity' \
    'Gitea API metadata is canonical' \
    'Static repository content is untrusted' \
    'no Docker socket or SSH key' \
    'Credentials and control access' \
    'Suspected token compromise' \
    'Forged-hook response' \
    'Disk-exhaustion response' \
    'Unsupported legacy credentials'; do
    require_text "$security_doc" "$text"
done

require_text README.md 'ENABLE_ORGANIZATION_HOOKS=true'
require_text README.md 'administrator token pool'
require_text AI.md 'Unsupported legacy credentials'
require_text AI.md 'ENABLE_ORGANIZATION_HOOKS=true'
require_text AI.md 'administrator token pool'
user_facing_paths=(README.md AI.md docs/security.md .env.example examples)
if rg -n -i \
    -e 'write:admin' \
    -e 'GITEA_ACCESS_TOKEN' \
    -e 'WEBHOOK_SECRET[=[:space:]]' \
    -e 'WEBHOOK_PUBLIC_URL=http://deployer:8080' \
    -e 'https?://[^[:space:]]*deployer[^[:space:]]*:8080' \
    -e 'https?://(localhost|[^[:space:]]*deployer[^[:space:]]*):8080/(oauth|webhook)' \
    -e '\./test\.sh' \
    -e '\./cleanup\.sh' \
    -e 'system[- ]wide[^[:cntrl:]]*hook' \
    -e '所有仓库' \
    -e 'SSH private-key mount' \
    "${user_facing_paths[@]}"; then
    printf 'user-facing documentation or examples still contain a retired credential or public Deployer route\n' >&2
    exit 1
fi
