# Gitea Pages 快速部署

在 Linux amd64/arm64 且支持 Docker Compose 的主机上，使用仓库根目录的
`docker-compose.yml` 和 `.env.example`。仅 Nginx 发布宿主机端口。

1. 复制 `.env.example` 为 `.env`，设置 `PAGES_DOMAIN`、`PAGES_DATA_DIR`、
   `PAGES_HTTP_PORT`、`PAGES_UID` 和 `PAGES_GID`。如需更改内部上游，设置
   `PAGES_DEPLOYER_UPSTREAM`。
2. 在 Gitea 创建机密 OAuth 应用，回调地址填
   `https://<PAGES_DOMAIN>/oauth/callback`。
3. 运行 `docker compose up -d --build`。新数据库不要求 Gitea 参数或密钥文件。
4. 由运维限制 Pages 控制域名为管理员可访问，然后在 `/config` 填写 Gitea API URL、
   可选的公开 URL、OAuth Client ID 和 Client Secret；保存并点击“手动重载”。
5. 访问 `/oauth/start` 完成一次授权。Deployer 将授权及刷新令牌保存在 SQLite，
   后端会定时续期，无需保持浏览器页面打开。

`/config`、`/status`、`/sites` 等页面不提供应用内管理员认证，入口访问控制由运维负责。
Webhook 仍使用各自的 HMAC 密钥验证。请备份 `gitea-pages-deployer-data` 数据卷与
`PAGES_DATA_DIR`。本版本不迁移旧数据库；安装时使用新数据卷。

`PAGES_DEPLOYER_UPSTREAM` 默认 `deployer:8080`，格式为 `主机名:容器监听端口`。
更改后运行 `docker compose up -d nginx` 重新创建 Nginx。若修改 Compose 服务名，
同步修改 `depends_on` 并确保两服务共享后端网络。
