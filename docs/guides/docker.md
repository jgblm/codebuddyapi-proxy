# Docker 部署与镜像发布

## 本地构建

在仓库根目录执行（需要 Docker Engine 和 Compose v2）：

```bash
docker build --build-arg VERSION=dev -t buddy-proxy:dev .
```

镜像采用多阶段静态构建，运行时包含 CA 证书和时区数据，以 UID/GID
`10001:10001` 运行。构建上下文只允许必要源码，不包含 `.env`、账号 JSON、
`.local/`、`go-codebuddy/`、历史二进制或日志。

## Compose

在 shell 或本地 `.env` 设置以下变量。请自行生成强随机值，不要提交凭据：

```env
CODEBUDDY_PROXY_API_KEY=<your-random-api-key>
CODEBUDDY_PROXY_ADMIN_PASSWORD=<your-random-admin-password>
CODEBUDDY_SITE=domestic
CODEBUDDY_PRODUCT=workbuddy
```

从源码启动：

```bash
docker compose up -d --build
docker compose logs --tail=50
```

发行镜像发布成功后，也可直接拉取：

```bash
docker compose pull
docker compose up -d --no-build
```

默认镜像为 `ghcr.io/wnddd839/buddy-proxy:latest`。可用
`BUDDY_PROXY_IMAGE=ghcr.io/<owner>/<repo>:<version>` 覆盖，生产环境建议固定版本
或 digest，例如 `ghcr.io/wnddd839/buddy-proxy:v0.6`。镜像须等对应标签的
Docker release 工作流成功后才可拉取。

默认只在宿主机 `127.0.0.1:32126` 暴露服务。打开
`http://127.0.0.1:32126/direct-admin/`，用管理台密码登录并完成 OAuth。
远程部署请通过 SSH 隧道或有 HTTPS 及访问控制的反向代理访问，不要直接公开管理台。
容器里的监听地址为 `0.0.0.0`，不代表宿主机必须对外开放。

账号、用量和运行时配置保存在名为 `buddy-data` 的卷内：

| 文件 | 容器路径 |
|------|----------|
| OAuth 账号池 | `/data/proxy-accounts.json` |
| 用量记录 | `/data/proxy-usage.json` |
| 首次生成及管理台更新的配置 | `/data/proxy.env` |

Compose 要求 API Key 和管理台密码，默认启用 API 鉴权。镜像单独运行时，未提供
Key 会自动生成并持久化到 `/data/proxy.env`。环境变量优先于文件配置；如果
Compose 显式设置了 Key，更换 Key 时应同步更新 Compose 环境变量，以免重启后恢复旧值。

容器根文件系统只读，`/data` 为可写卷，`/tmp` 为临时文件系统。若改为宿主机
目录挂载，需先让该目录可供 UID `10001` 写入，并妥善备份，目录里含敏感凭据。
不要使用 `docker compose down -v` 清理仍需保留的账号卷。

健康检查只访问 `/health`，表示进程存活，不调用官方搜索，也不消耗搜索配额。
`/readyz` 用于上游可达性检查。退出时发送 SIGTERM，预留 20 秒供用量和账号刷盘。

## GitHub Container Registry release

工作流：`.github/workflows/docker-release.yml`。

- PR：在相关构建文件或源码变化时，运行格式检查、vet、race tests、单架构镜像构建，
  检查非 root、只读根文件系统、启动健康、API 鉴权及重建容器后的配置持久化。
- `v*` 标签：验证成功后向当前仓库的 GHCR 发布 `linux/amd64`、
  `linux/arm64`、`linux/arm/v7` 多架构镜像。
- 手动执行：普通分支只验证；在版本标签上执行时也会发布。
- 镜像标签包含完整 Git tag 及去掉 `v` 的版本（如 `v0.6`、`0.6`）；
  三段语义版本另生成 `major.minor` 标签。稳定版本更新
  `latest`，带 `-rc` 等预发行后缀的标签不更新 `latest`。
- 构建注入版本、commit 和 UTC 构建时间，并生成 provenance 与 SBOM。
- 使用工作流的 `GITHUB_TOKEN`，只有发布任务获 `packages: write` 权限，
  不需要 Docker Hub 密码。仓库需允许 Actions 写入 package；首次发布后如需匿名
  拉取，维护者应在 GHCR package 设置中将可见性设为 public。

该工作流发布容器镜像，不替代已有的二进制 `scripts/release.ps1` / `make release`。
提交这些文件不会立即发布；只有远端触发工作流才会上传镜像。

## 搜索与计费边界

Docker 部署沿用账号池的 CodeBuddy / WorkBuddy 官方 `/agenttool/v1/*` 接口，
没有接入独立的 DeepSeek API 或第三方搜索服务。搜索使用账号自己的 OAuth 权限，
受官方搜索配额及计费规则限制。`15001` 表明搜索配额相关错误，但不能仅凭该错误码
确定每次调用的积分单价；本次没有做扣费前后的账单对账，不额外估算搜索 Credit。
