# 下游如何使用官方搜索

## 默认策略：仅显式声明才搜索

**普通 Chat / Responses 请求不会自动检索。** 仅在本次请求明确启用搜索时，
代理才向模型提供内部搜索工具。模型按需决定是否调用，而不是每条消息强制扣费搜索。
仅在提示词中写“帮我搜索”，但不声明搜索能力，不会开启官方搜索接口。

客户端仍使用同一组配置：

```text
Base URL: http://127.0.0.1:32126/v1
API Key: 你的代理 API Key
Model: auto 或 /v1/models 返回的模型
```

按客户端使用的协议，在**请求体**中添加以下声明。无需新增搜索 API Key，
也不需要客户端自己执行搜索 function。

## Chat Completions 客户端

在支持“附加请求参数 / Extra body”的客户端中设置：

```json
{"web_search_options": {}}
```

完整示例：

```bash
curl http://127.0.0.1:32126/v1/chat/completions \
  -H "Authorization: Bearer $CODEBUDDY_PROXY_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "auto",
    "messages": [{"role":"user","content":"搜索最近 Go 版本发布说明，并附来源链接"}],
    "web_search_options": {},
    "stream": true
  }'
```

本代理也接受 Chat 扩展声明 `tools: [{"type":"web_search"}]`（或
`web_search_preview`），但这不是标准 Chat function 的形状；优先使用
`web_search_options: {}`。目前该字段仅作为本代理的启用声明，不解释
`search_context_size`、`user_location` 等提供商特有子参数。

## Responses / Codex 类客户端

在 `/v1/responses` 请求中启用内置工具：

```json
{
  "model": "auto",
  "input": "搜索最近 Go 版本发布说明，并附来源链接",
  "tools": [{"type": "web_search"}],
  "stream": true
}
```

使用 Codex 等客户端时，启用其内置联网搜索功能，并确认实际发出的请求含
`tools: [{"type":"web_search"}]` 或 `web_search_preview`。仅配置 Base URL
不能自动替客户端启用该功能。

## 代理内部执行，客户端接收普通答案

```text
下游显式声明 → 模型提出检索词 → 代理调用官方 /agenttool/v1/search
             → 标题 / URL / 摘要回填模型 → 正常 Chat 或 Responses 答案
```

- 客户端不会收到要求它执行 `buddy_proxy_builtin_web_search` 的调用。
- 客户端原有的普通 function 工具仍归客户端负责；自己定义一个名叫
  `web_search` 的 function 不等于声明了本代理的官方搜索。
  若模型首轮同时请求内部搜索和客户端 function，当前仅执行搜索，
  客户端 function 需模型在拿到搜索结果后的末轮重新提出。
- 流式会立即发首帧并保活，最终答案聚合后回放；非流式返回完整 JSON。
- 一次请求只执行一轮模型提出的搜索调用；同一轮可以有多个查询，
  并不是保证只计费一次。固定账号 Key 的限制同样作用于内部搜索。
- 标题、URL 和摘要会回填模型；是否在正文引用来源取决于模型，建议在提示词中要求
  附来源。当前不保证原生引用卡片、`web_search_call` item 或 `url_citation`。

## 关闭与兼容边界

- 不需要联网的请求：省略声明，或 Chat 传 `web_search_options: null`。
- 即使声明搜索，`tool_choice: "none"` 也会禁止代理提供及执行内部搜索工具。
- NewAPI 等中转层必须保留 `web_search_options` 或内置搜索 `tools`。
  若中转层过滤了字段，代理不会擅自开启搜索；需要在渠道配置中允许透传。
- 不能发送附加请求体或内置工具的客户端，无法仅靠 Base URL 使用内置搜索，
  需使用其支持的工具适配器，或显式调用 `/v1/search`。
- `/v1/models` 仍是模型目录，不会让所有客户端自动显示“联网搜索”按钮。

需要获取搜索原始结果而非模型作答时，直接调用：

```json
POST /v1/search
{"query":"Go 最新版本发布说明","max_results":5}
```

网页正文用 `/v1/webfetch`；金融/投资多步研究用 `/v1/agentic_search`。
具体字段见 [HTTP API](../api/http.md)。

上述调用使用当前 CodeBuddy / WorkBuddy 账号池 OAuth，沿用官方配额及计费规则，
没有接入独立 DeepSeek API 或第三方搜索服务。积分单价和实际扣费需以官方账单为准，
代理不把缺失的搜索 Credit 估算成已验证费用。
