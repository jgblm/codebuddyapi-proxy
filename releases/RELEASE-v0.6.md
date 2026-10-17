# v0.6 · 官方搜索与 Docker 发布

发布日期：2026-10-17

## 官方搜索接入下游

- Chat Completions：请求添加 `"web_search_options": {}`。
- Responses：请求添加 `"tools": [{"type":"web_search"}]`，兼容 `web_search_preview`。
- 仅显式声明才允许模型按需搜索；普通请求、null 声明及 `tool_choice:"none"` 不自动搜索。
- 代理内部使用 CodeBuddy / WorkBuddy 账号 OAuth 调用官方搜索，回填结果后返回普通 JSON 或 SSE 答案，客户端无需执行内部搜索工具。
- 独立接口：`POST /v1/search`、`/v1/webfetch`、`/v1/agentic_search`，兼容省略 `/v1` 的别名；多步研究接口面向金融/投资研究。
- 固定账号限制同样适用于内部搜索；模型用量汇总两轮，流式立即发首帧并保活，最终答案聚合后回放。

配置与示例：[下游搜索接入](https://github.com/wnddd839/buddy-proxy/blob/v0.6/docs/guides/search.md)。
经 NewAPI 等中转时，需允许透传声明字段。仅配置 Base URL 不会自动启用搜索。

## Docker / GHCR

- 新增多阶段、非 root Dockerfile 和持久化 Compose；默认只绑定宿主机本地端口。
- 版本标签触发验证及多架构发布：`linux/amd64`、`linux/arm64`、`linux/arm/v7`。
- 目标镜像：`ghcr.io/wnddd839/buddy-proxy:v0.6`（工作流成功后可用），另有 `0.6` / `latest` 标签。
- 工作流校验启动、健康、鉴权、非 root 和配置持久化，发布附带 provenance 与 SBOM。

部署说明：[Docker 指南](https://github.com/wnddd839/buddy-proxy/blob/v0.6/docs/guides/docker.md)。

## 仓库整理

本地设计稿、开发规范、工作计划及审查材料不再纳入版本控制，保留在开发机，
不随新版本源码发布。不重写既有 Git 历史。

## 升级与边界

覆盖旧二进制后重启，无需迁移账号池。七个平台二进制、配置模板及 `SHA256SUMS.txt`
随本次 Release 提供。

搜索全部使用官方账号权限及配额，没有接入独立 DeepSeek API 或第三方搜索服务。
实际积分扣费尚未通过账单前后对账验证，不估算缺失的搜索 Credit。
内置搜索每次请求最多执行一轮，该轮可以包含多个查询。暂不保证原生引用卡片，
`web_search_call` item 或 `url_citation`。首轮混合的客户端 function 调用需模型末轮重发。
