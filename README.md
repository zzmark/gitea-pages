# Gitea Pages - Actionless Static Site Hosting

[![CI, Security Scan, and Image Publish](https://github.com/kekxv/gitea-pages/actions/workflows/security.yml/badge.svg)](https://github.com/kekxv/gitea-pages/actions/workflows/security.yml)
[![Deployer Image](https://img.shields.io/badge/ghcr.io%2Fkekxv%2Fgitea-pages%2Fdeployer-latest)](https://github.com/kekxv/gitea-pages/pkgs/container/deployer)
[![Nginx Image](https://img.shields.io/badge/ghcr.io%2Fkekxv%2Fgitea-pages%2Fnginx-latest)](https://github.com/kekxv/gitea-pages/pkgs/container/nginx)

[English](#english) | [中文](#中文)

---

<a name="english"></a>

## English

A GitHub Pages-like static site hosting system for Gitea. Automatically deploys sites when code is pushed to the `gh-pages` branch.

New to the codebase? Start with the [Chinese project guide](docs/project-guide.md) for an architecture diagram, request flows, module map, and test commands.

### Features

- **Zero User Action**: Push to `gh-pages` branch → automatic deployment
- **Automatic Cleanup**: Delete `gh-pages` branch → site automatically removed
- **Wildcard Domain Routing**: `username.pages.yourdomain.com` and `username.pages.yourdomain.com/repo`
- **Security Hardened**: Non-root containers, symlink blocking, path traversal protection
- **Private Repo Support**: OAuth2 user authorization
- **Scoped Webhook Registration**: Each Gitea hook has an independent key and HMAC secret
- **Contained Topology**: Nginx is the only host-published service; Deployer has no Docker socket or SSH key
- **Deployment Inventory**: Browse accessible deployed sites, deployed commits, and update times at `/sites`
- **Cold-start Recovery**: Administrators can scan authorized repositories and deploy missing or outdated sites
- **Hidden-file Compatibility**: Hidden files and directories are accepted during deployment, with reserved metadata excluded

### Quick Start

#### Using Pre-built Images (Recommended)

```bash
# Create docker-compose.yml
curl -O https://raw.githubusercontent.com/kekxv/gitea-pages/main/docker-compose.yml
curl -O https://raw.githubusercontent.com/kekxv/gitea-pages/main/.env.example
cp .env.example .env
# Edit .env with your settings

# The Compose file names GHCR images, so this path does not need the source
# build contexts. Create the three secret files, then pull and run.
docker compose pull
docker compose up -d
```

Pre-built images available at:
- **Deployer**: `ghcr.io/kekxv/gitea-pages/deployer:latest`
- **Nginx**: `ghcr.io/kekxv/gitea-pages/nginx:latest`

#### Building from Source

```bash
git clone https://github.com/kekxv/gitea-pages.git
cd gitea-pages
docker compose up -d --build
```

#### Configure the Nginx Upstream

Set the internal Deployer address in `.env` when using a different service name or network alias:

```dotenv
PAGES_DEPLOYER_UPSTREAM=pages-backend:8080
```

The default is `deployer:8080`. Use the container's listening address as `host:port`, without `http://` or a path. Nginx renders this value into its configuration at startup. Apply changes with `docker compose up -d nginx`; changing the address does not require rebuilding the image. If you rename the Compose service itself, also update `depends_on` and keep both services on the backend network.

### OAuth2 Configuration (Recommended)

Users can self-authorize to enable automatic webhook registration and private repo access.

#### Step 1: Create OAuth2 Application in Gitea

1. Login to Gitea
2. Go to **Settings → Applications → OAuth2 Applications**
3. Click **Create OAuth2 Application**
4. Fill in:
   - **Application Name**: `Gitea Pages`
   - **Redirect URI**: `https://pages.yourdomain.com/oauth/callback`
   - **Confidential Client**: **YES** (Important!)
5. Copy **Client ID** and **Client Secret**

#### Step 2: Configure Deployer

Set the client ID in `.env`; keep the client secret in the
`PAGES_OAUTH_CLIENT_SECRET_HOST_FILE` file described by `.env.example`. Compose
uses `PAGES_DOMAIN` as the complete Pages domain (for example,
`pages.yourdomain.com`) for public callback and webhook URLs:
```bash
PAGES_OAUTH_CLIENT_ID=your-client-id
# Optional when the browser and Git clone address is the same as
# PAGES_GITEA_API_URL. Deployments always clone from this public address.
PAGES_GITEA_PUBLIC_URL=https://gitea.example.com
```

#### Step 3: User Authorization

1. Visit `https://pages.yourdomain.com`
2. Click **"Authorize Gitea Pages"**
3. Login to Gitea and approve the authorization
4. A scoped webhook is automatically registered for your authorized personal
   and organization scopes

### Permission Explanation

When users authorize Gitea Pages, the following permissions are requested:

| Permission | Scope | Purpose |
|------------|-------|---------|
| Read User Info | `read:user` | Get username to identify site ownership |
| Manage User Settings | `write:user` | Register a personal-scope webhook |
| Read Repositories | `read:repository` | Clone repository code for deployment |
| Manage Organization Webhooks | `write:organization` | Automatically register organization hooks through the approved administrator token pool |

Users can revoke authorization anytime in Gitea **Settings → Applications → OAuth2 Applications**.
Organization hooks are automatic by default through the approved administrator
token pool. Keep `PAGES_ENABLE_ORGANIZATION_HOOKS=true` in `.env` for this architecture; set
it to `false` only when an installation intentionally serves personal scopes.

### Private Repository Support

With OAuth2 authorization, private repositories are automatically supported. The deployer uses the user's OAuth token to clone private repos when deploying their sites.

#### Create Your Site

**Root Site (username.pages.domain.com):**
```bash
# Repository name format: username.<complete Pages domain>
git init yourname.pages.example.com
cd yourname.pages.example.com
git checkout -b gh-pages
echo "<html><body>Hello from root!</body></html>" > index.html
git add . && git commit -m "Initial site"
git remote add origin https://gitea.example.com/username/yourname.pages.example.com.git
git push -u origin gh-pages
```
Site available at: `https://username.pages.example.com/`

**Subdirectory Site (username.pages.domain.com/repo):**
```bash
git init my-site
cd my-site
git checkout -b gh-pages
echo "<html><body>Hello!</body></html>" > index.html
git add . && git commit -m "Initial site"
git remote add origin https://gitea.example.com/username/my-site.git
git push -u origin gh-pages
```
Site available at: `https://username.pages.example.com/my-site`

### Deployment Inventory and Cold-start Scan

After OAuth login, open `https://pages.yourdomain.com/sites` or select **已部署站点** on the home page. The inventory shows only deployed repositories you currently have permission to access, including organization repositories. Search by repository, site address, or commit.

Each entry includes a site link, repository, root/subdirectory type, deployed Git commit, and UTC update time. The version comes from the actual deployed checkout. Older sites without deployment records show **未记录** until their next successful deployment.

To load existing `gh-pages` branches without another push:

1. Log in as a **Gitea instance administrator**, open `/sites`, and click **全量扫描并补全部署**.
2. The background job scans repositories belonging to users and organizations already authorized in this Pages instance. Disabling organization hooks limits scanning to personal scopes.
3. Missing sites, missing records, and versions differing from `gh-pages` are deployed. Matching versions are skipped. Repositories without that branch leave existing sites untouched; ownership conflicts are reported without overwriting sites.

The page shows progress and failures. Only one scan runs per process; startup does not trigger it automatically. Closing the page does not stop the job, but restarting Deployer interrupts it and clears progress. Run it again to complete remaining deployments. See [deployment inventory and scan details](docs/deployed-sites.md) (Chinese).

### Hidden Files and Deployment Records

Deployment accepts hidden files and directories without an allowlist. Git metadata (`.git`) is excluded, and the site-root `.gitea-pages-deployment.json` is reserved for the system's signed deployment record; repository content cannot replace it. Symlinks, unsafe paths, special files, and publication size limits remain checked.

**Nginx still blocks HTTP access to hidden paths**, including the deployment record. Accepting hidden content during deployment does not make paths such as `/.well-known/` publicly accessible. Back up the internal deployment record with the Pages data and retain its corresponding token-encryption key.

### Legacy Installation Compatibility

Historical plaintext token databases and shared webhook credentials are unsupported. Offline credential migration/rollback and automatic legacy credential upgrades have been removed. Installations using those formats must start with a fresh Deployer data volume and complete OAuth again. Preserve published sites separately through `PAGES_DATA_DIR`; see [security operations](docs/security.md).

### Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                         Gitea Server                         │
│    signed, per-hook delivery → <DOMAIN>/webhook              │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│ Nginx (only host port) → private backend → Deployer          │
│  • forwards control UI, /sites, OAuth, and /webhook           │
│  • serves Pages data read-only                               │
│  • Deployer verifies per-hook HMAC and Gitea metadata        │
│  • Deployer publishes untrusted static content atomically    │
│  • Deployer has no Docker socket, SSH key, or host port      │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                     Published Pages volume                   │
│  • root: username.pages.domain → _root/                      │
│  • subsite: username.pages.domain/repo → repo/               │
└─────────────────────────────────────────────────────────────┘
```

### Security Features

| Feature | Implementation |
|---------|---------------|
| Non-root containers | UID/GID 1000 (`pagesuser`) |
| No-new-privileges | `security_opt: no-new-privileges:true` |
| Read-only root filesystem | `read_only: true` with tmpfs |
| Symlink blocking | `disable_symlinks on` + code filter |
| Path traversal protection | Input sanitization |
| Git metadata and hidden paths | `.git` excluded from deployment; Nginx blocks hidden HTTP paths |
| Webhook authentication | Per-hook key and HMAC-SHA256 secret; Gitea metadata is canonical |
| Site size limit | `PAGES_MAX_SITE_SIZE_MB` in `.env` (default 100MB) |
| Private repo support | OAuth2 user tokens |
| Network exposure | Only Nginx publishes a port; Deployer is private to Compose networks |

### Directory Structure

```
gitea-pages/
├── .env.example           # Environment configuration
├── docker-compose.yml     # Container orchestration
├── nginx/
│   ├── Dockerfile
│   ├── nginx.conf         # Runtime configuration template
│   └── start-nginx.sh     # Domain and upstream substitution
├── deployer/
│   ├── Dockerfile
│   ├── main.go            # Entry point
│   ├── handler.go         # Webhook handler
│   ├── git.go             # Git operations
│   ├── gitea.go           # Gitea API client
│   ├── oauth.go           # OAuth2 handler
│   ├── web.go             # Web UI
│   ├── web_sites.go       # Permission-filtered deployment inventory
│   ├── web_scan.go        # Administrator scan endpoints
│   ├── deployment_catalog.go # Signed version and time records
│   ├── pages_scan.go      # Full-scan deployment orchestration
│   ├── gitea_scan.go      # Repository enumeration and branch lookup
│   └── security.go        # Security utilities
├── docs/deployed-sites.md # Inventory and recovery details
└── examples/quickstart/   # Hardened deployment guide
```

### Testing

Run on Linux or WSL. Go race tests require Go and a C compiler; container checks require a running Docker engine.

```bash
# Go unit and end-to-end regression tests
(cd deployer && go test -race ./...)

# Nginx startup template checks (no Docker required)
bash tests/nginx_startup_test.sh

# Compose and Nginx containment checks, from the repository root
bash tests/compose_security_test.sh
bash tests/nginx_test.sh
```

### License

MIT License

---

<a name="中文"></a>

## 中文

为 Gitea 实现类似 GitHub Pages 的静态网站托管系统。向 `gh-pages` 分支推送代码后自动部署。

初次了解项目？先看[项目导览](docs/project-guide.md)：从整体架构、业务流程逐步到代码模块、配置和验证方式。

### 功能特性

- **零用户操作**：推送代码到 `gh-pages` 分支 → 自动部署
- **自动清理**：删除 `gh-pages` 分支 → 自动删除站点
- **泛域名路由**：支持 `username.pages.yourdomain.com` 和 `username.pages.yourdomain.com/repo`
- **安全加固**：非 root 容器、阻止软链接、路径遍历防护
- **私有仓库支持**：OAuth2 用户授权
- **范围化 Webhook 注册**：每个 Gitea hook 均拥有独立 key 和 HMAC secret
- **隔离部署**：仅 Nginx 暴露宿主机端口；Deployer 不持有 Docker socket 或 SSH 密钥
- **部署清单**：在 `/sites` 查看有权访问的已部署站点、部署版本和更新时间
- **冷启动补部署**：管理员可扫描已授权仓库，补齐缺失站点并更新版本
- **隐藏文件兼容**：部署允许普通隐藏文件和目录，排除保留的元数据

### 快速开始

#### 使用预构建镜像（推荐）

```bash
# 创建 docker-compose.yml
curl -O https://raw.githubusercontent.com/kekxv/gitea-pages/main/docker-compose.yml
curl -O https://raw.githubusercontent.com/kekxv/gitea-pages/main/.env.example
cp .env.example .env
# 编辑 .env 填入你的配置

# Compose 文件已指定 GHCR 镜像，此流程不需要源码构建上下文。
# 按 .env.example 创建三个密钥文件后，拉取并运行。
docker compose pull
docker compose up -d
```

预构建镜像地址：
- **Deployer**: `ghcr.io/kekxv/gitea-pages/deployer:latest`
- **Nginx**: `ghcr.io/kekxv/gitea-pages/nginx:latest`

#### 从源码构建

```bash
git clone https://github.com/kekxv/gitea-pages.git
cd gitea-pages
docker compose up -d --build
```

#### 配置 Nginx 上游地址

需要使用其他 Deployer 服务名或网络别名时，在 `.env` 中设置内部地址：

```dotenv
PAGES_DEPLOYER_UPSTREAM=pages-backend:8080
```

默认值为 `deployer:8080`，格式为 `主机名:容器监听端口`，不含 `http://` 或路径。Nginx 启动时将该值写入配置；修改后执行 `docker compose up -d nginx` 重新创建容器，无需因地址变化重新构建镜像。若修改 Compose 服务名，还需同步调整 `depends_on`，并确保两个服务共享后端网络。

### OAuth2 配置（推荐）

用户可以自助授权，启用自动 webhook 注册和私有仓库访问。

#### 步骤 1：在 Gitea 创建 OAuth2 应用

1. 登录 Gitea
2. 进入 **设置 → 应用 → OAuth2 应用**
3. 点击 **创建 OAuth2 应用**
4. 填写：
   - **应用名称**：`Gitea Pages`
   - **重定向 URI**：`https://pages.yourdomain.com/oauth/callback`
   - **机密客户端**：**是**（重要！）
5. 复制 **客户端 ID** 和 **客户端密钥**

#### 步骤 2：配置 Deployer

在 `.env` 中设置客户端 ID；客户端密钥必须保存在 `.env.example` 所述的
`PAGES_OAUTH_CLIENT_SECRET_HOST_FILE` 文件中。`PAGES_DOMAIN` 是完整的 Pages 域名（例如
`pages.yourdomain.com`），Compose 据此使用公开回调和 webhook 地址：
```bash
PAGES_OAUTH_CLIENT_ID=你的客户端ID
# 与 PAGES_GITEA_API_URL 相同时可省略。
PAGES_GITEA_PUBLIC_URL=https://gitea.example.com
```

#### 步骤 3：用户授权

1. 访问 `https://pages.yourdomain.com`
2. 点击 **"授权 Gitea Pages"**
3. 登录 Gitea 并批准授权
4. 系统会为已授权的个人和组织范围自动注册独立 webhook

### 权限说明

用户授权 Gitea Pages 时，请求以下权限：

| 权限 | Scope | 用途 |
|------|-------|------|
| 读取用户信息 | `read:user` | 获取用户名以标识站点所有权 |
| 管理用户设置 | `write:user` | 注册个人范围的 webhook |
| 读取仓库 | `read:repository` | 克隆仓库代码进行部署 |
| 管理组织 Webhook | `write:organization` | 通过已批准的管理员 token 池自动注册组织 webhook |

用户可随时在 Gitea **设置 → 应用 → OAuth2 应用** 中撤销授权。
组织 webhook 通过已批准的管理员 token 池默认自动注册。请在 `.env` 中保持
`PAGES_ENABLE_ORGANIZATION_HOOKS=true`；只有明确只服务个人范围时才设为 `false`。

### 私有仓库支持

通过 OAuth2 授权，私有仓库自动获得支持。部署时 Deployer 使用用户的 OAuth token 克隆私有仓库。

#### 创建站点

**根目录站点 (username.pages.domain.com)：**
```bash
# 仓库名格式：username.<完整 Pages 域名>
git init yourname.pages.example.com
cd yourname.pages.example.com
git checkout -b gh-pages
echo "<html><body>根目录站点</body></html>" > index.html
git add . && git commit -m "初始化站点"
git remote add origin https://gitea.example.com/username/yourname.pages.example.com.git
git push -u origin gh-pages
```
访问地址：`https://username.pages.example.com/`

**子目录站点 (username.pages.domain.com/repo)：**
```bash
git init my-site
cd my-site
git checkout -b gh-pages
echo "<html><body>子目录站点</body></html>" > index.html
git add . && git commit -m "初始化站点"
git remote add origin https://gitea.example.com/username/my-site.git
git push -u origin gh-pages
```
访问地址：`https://username.pages.example.com/my-site`

### 部署清单与冷启动扫描

完成 OAuth 登录后，访问 `https://pages.yourdomain.com/sites`，或点击首页的 **已部署站点**。清单仅展示当前用户有权访问的已部署仓库，包括组织仓库，支持按仓库名称、站点地址和 commit 搜索。

每条记录包含站点链接、仓库、根站点/子站点类型、实际部署的 Git commit 和 UTC 更新时间。版本来自已部署的工作区；旧站点缺少部署记录时显示 **未记录**，下次成功部署后补齐。

已有大量 `gh-pages` 分支、希望无需再次推送即可加载时：

1. 使用 **Gitea 实例管理员** 身份登录，进入 `/sites`，点击 **全量扫描并补全部署**。
2. 后台任务扫描本 Pages 实例中已授权用户及组织的仓库；关闭组织 hook 功能时仅扫描个人范围。
3. 缺少站点、缺少记录或版本与 `gh-pages` 不一致时重新部署；版本一致则跳过。没有该分支时保留现有站点，归属冲突时报告错误并保留原站点。

页面展示进度和失败详情，每个进程同时只允许一个扫描任务。程序启动时不会自动扫描；关闭页面不影响任务，但重启 Deployer 会中断扫描并清空进度，可再次点击按钮续补。详见[部署清单与扫描说明](docs/deployed-sites.md)。

### 隐藏文件与部署记录

部署允许隐藏文件和目录，无需逐项加入白名单。Git 元数据（`.git`）不复制；站点根目录的 `.gitea-pages-deployment.json` 是系统保留的签名部署记录，仓库中的同名内容不会覆盖它。软链接、不安全路径、特殊文件和发布大小限制仍然接受检查。

**Nginx 仍禁止通过 HTTP 访问隐藏路径**，包括部署记录文件。因此，允许部署隐藏内容并不代表 `/.well-known/` 等路径已开放访问。备份 Pages 数据时应保留内部部署记录及其对应的令牌加密密钥。

### 旧版安装兼容性

历史明文 token 数据库和共享 webhook 凭据不再受支持，离线凭据迁移、回滚及旧凭据自动升级能力已移除。使用这些旧格式的实例需要以新的 Deployer 数据卷启动，并重新完成 OAuth 授权。已发布站点通过 `PAGES_DATA_DIR` 单独保留；详见[安全运维说明](docs/security.md)。

### 架构图

```
┌─────────────────────────────────────────────────────────────┐
│                         Gitea Server                         │
│    每个 hook 的签名投递 → <DOMAIN>/webhook                   │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│ Nginx（唯一宿主机端口）→ 私有后端网络 → Deployer              │
│  • 控制页面、/sites、OAuth 和 /webhook 转发到 Deployer       │
│  • Nginx 以只读方式提供 Pages 数据                           │
│  • Deployer 校验每个 hook 的 HMAC 和 Gitea 元数据            │
│  • Deployer 原子发布不可信静态内容                           │
│  • Deployer 没有 Docker socket、SSH 密钥或宿主机端口          │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                      已发布的 Pages 卷                       │
│  • 根目录: username.pages.domain → _root/                   │
│  • 子站点: username.pages.domain/repo → repo/               │
└─────────────────────────────────────────────────────────────┘
```

### 安全特性

| 特性 | 实现方式 |
|------|---------|
| 非 root 容器 | UID/GID 1000 (`pagesuser`) |
| 禁止提权 | `security_opt: no-new-privileges:true` |
| 只读根文件系统 | `read_only: true` + tmpfs |
| 阻止软链接 | `disable_symlinks on` + 代码过滤 |
| 路径遍历防护 | 输入净化 |
| Git 元数据与隐藏路径 | 部署排除 `.git`；Nginx 禁止 HTTP 访问隐藏路径 |
| Webhook 鉴权 | 每个 hook 独立 key 和 HMAC-SHA256 secret；Gitea 元数据为准 |
| 站点大小限制 | `.env` 中的 `PAGES_MAX_SITE_SIZE_MB`（默认 100MB） |
| 私有仓库支持 | OAuth2 用户令牌 |
| 网络暴露 | 仅 Nginx 发布端口；Deployer 仅存在于 Compose 私有网络 |

### 目录结构

```
gitea-pages/
├── .env.example           # 环境变量配置
├── docker-compose.yml     # 容器编排
├── nginx/
│   ├── Dockerfile
│   ├── nginx.conf         # 运行时配置模板
│   └── start-nginx.sh     # 替换域名和上游地址
├── deployer/
│   ├── Dockerfile
│   ├── main.go            # 入口
│   ├── handler.go         # Webhook 处理
│   ├── git.go             # Git 操作
│   ├── gitea.go           # Gitea API 客户端
│   ├── oauth.go           # OAuth2 处理
│   ├── web.go             # Web UI
│   ├── web_sites.go       # 按仓库权限过滤的部署清单
│   ├── web_scan.go        # 管理员扫描接口
│   ├── deployment_catalog.go # 签名版本和时间记录
│   ├── pages_scan.go      # 全量扫描与补部署
│   ├── gitea_scan.go      # 仓库枚举和分支查询
│   └── security.go        # 安全工具函数
├── docs/deployed-sites.md # 清单与冷启动恢复说明
└── examples/quickstart/   # 安全部署指南
```

### 测试

在 Linux 或 WSL 中执行。Go 竞态测试需要 Go 和 C 编译器；容器检查需要已启动的 Docker 引擎。

```bash
# Go 单元和端到端回归测试
(cd deployer && go test -race ./...)

# Nginx 启动模板检查（无需 Docker）
bash tests/nginx_startup_test.sh

# 在仓库根目录执行 Compose 与 Nginx 隔离检查
bash tests/compose_security_test.sh
bash tests/nginx_test.sh
```

### 许可证

MIT License
