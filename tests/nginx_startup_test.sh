#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT

# Run the real renderer without Docker, relocating only its two fixed paths.
# Nginx parsing and HTTP forwarding are covered by nginx_test.sh in containers.
sed -e "s|/etc/nginx/nginx.conf.template|$repo_root/nginx/nginx.conf|g" \
    -e "s|/tmp/nginx.conf|$test_dir/nginx.conf|g" \
    "$repo_root/nginx/start-nginx.sh" > "$test_dir/start-nginx.sh"
cat > "$test_dir/nginx" <<'SH'
#!/bin/sh
test "$1" = '-c'
cat "$2"
SH
chmod 755 "$test_dir/nginx"
export PATH="$test_dir:$PATH"
export PAGES_DOMAIN=pages.example.com

assert_rendered() {
    local name=$1 expected=$2
    local rendered
    rendered=$(sh "$test_dir/start-nginx.sh" -T)
    grep -Fq "server $expected;" <<<"$rendered"
    grep -Fq 'server_name pages.example.com;' <<<"$rendered"
    grep -Fq 'proxy_set_header Host $host;' <<<"$rendered"
    ! grep -Fq '__PAGES_' <<<"$rendered"
    printf '通过：%s\n' "$name"
}

unset PAGES_DEPLOYER_UPSTREAM
assert_rendered '上游配置-未设置时保持默认值' 'deployer:8080'
PAGES_DEPLOYER_UPSTREAM='' assert_rendered '上游配置-空值使用默认地址' 'deployer:8080'
PAGES_DEPLOYER_UPSTREAM='pages-backend:9090' assert_rendered '上游配置-自定义服务名与端口' 'pages-backend:9090'
PAGES_DEPLOYER_UPSTREAM='project_deployer:8080' assert_rendered '上游配置-支持带下划线的网络名称' 'project_deployer:8080'
PAGES_DEPLOYER_UPSTREAM='127.0.0.1:65535' assert_rendered '上游配置-支持IPv4与最大端口' '127.0.0.1:65535'
