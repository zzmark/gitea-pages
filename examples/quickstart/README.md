# Hardened deployment quickstart / 安全部署快速开始

The former all-in-one local example was removed because it published Deployer
directly, placed credentials in environment files, and instructed operators to
use legacy shared credentials. It is not compatible with the hardened runtime.

Use the repository-root `docker-compose.yml` and `.env.example` instead. That
topology publishes only Nginx; `DOMAIN` is the complete Pages domain and the
public OAuth callback and webhook endpoint are both served at `https://<DOMAIN>/`.
Deployer remains private on the Compose backend network and receives secrets only through the configured
secret files.

1. Copy `.env.example` to `.env` and set `DOMAIN`, the Gitea URLs, and the
   OAuth client ID.
2. Create the session, token-encryption, and OAuth-client-secret files exactly
   as documented in `.env.example`; restrict each to mode `0600`.
3. Register `https://<DOMAIN>/oauth/callback` as the Gitea OAuth callback
   and use `https://<DOMAIN>/webhook` as the hook target.
4. Start the root Compose stack with `docker compose up -d` and complete OAuth
   from `https://<DOMAIN>/`.
5. Keep `ENABLE_ORGANIZATION_HOOKS=true` for the approved automatic
   organization-hook flow backed by the administrator token pool.

Historical plaintext token databases and shared webhook credentials are not
supported. Start with a fresh Deployer data volume and complete OAuth again.
See [`docs/security.md`](../../docs/security.md) for security and incident
response procedures.

To use a different internal Deployer service name or network alias, set
`PAGES_DEPLOYER_UPSTREAM=pages-backend:8080` in `.env`. The default is
`deployer:8080`. Use a hostname or IPv4 address and the container's listening
port, without an HTTP scheme or path. Recreate Nginx with
`docker compose up -d nginx` after changing this value. If you rename the
Compose service itself, also update `depends_on`; both services must share
the backend network.

---

旧版的一体化本地示例已删除：它直接暴露 Deployer、在环境文件中保存凭据，并且
指导使用已废弃的共享凭据，与加固后的运行时不兼容。

请改用仓库根目录的 `docker-compose.yml` 和 `.env.example`。该拓扑只发布
Nginx；`DOMAIN` 是完整 Pages 域名，公开 OAuth 回调和 webhook 端点均为
`https://<DOMAIN>/`。Deployer
仅位于 Compose 后端私有网络，并且只通过配置的 secret 文件读取密钥。

1. 将 `.env.example` 复制为 `.env`，设置 `DOMAIN`、Gitea URL 和 OAuth 客户端 ID。
2. 按 `.env.example` 所述创建会话、令牌加密和 OAuth 客户端密钥文件，并将每个
   文件权限设为 `0600`。
3. 在 Gitea 中注册 `https://<DOMAIN>/oauth/callback`，并使用
   `https://<DOMAIN>/webhook` 作为 hook 目标。
4. 在仓库根目录运行 `docker compose up -d`，然后从
   `https://<DOMAIN>/` 完成 OAuth。
5. 保持 `ENABLE_ORGANIZATION_HOOKS=true`，以使用管理员 token 池支持的自动组织
   hook 流程。

历史明文 token 数据库和共享 webhook 凭据不再受支持。请使用新的 Deployer
数据卷启动，并让用户重新完成 OAuth。安全与事件响应流程请参见
[`docs/security.md`](../../docs/security.md)。

### 配置 Deployer 内部上游地址

在 `.env` 中指定服务名或网络别名和容器内部监听端口：

```dotenv
PAGES_DEPLOYER_UPSTREAM=pages-backend:8080
```

默认值为 `deployer:8080`。支持主机名或 IPv4 地址，不包含 `http://` 或路径。
启动脚本直接将上游地址写入 Nginx 配置；更改变量后执行
`docker compose up -d nginx` 重新创建容器即可，无需因地址变化重新构建镜像。
若重命名 Compose 中的服务键，还需同步调整 `depends_on`；目标服务必须能从
Nginx 的后端网络访问。
