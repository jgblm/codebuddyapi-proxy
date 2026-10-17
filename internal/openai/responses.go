package openai

import (
	"encoding/json"
	"strings"

	"github.com/wnddd839/codebuddy-proxy/internal/provider"
)

// ---------------------------------------------------------------------------
// Responses API 入站：POST /v1/responses 请求体
// ---------------------------------------------------------------------------

// ResponsesRequest 覆盖 OpenAI Responses API 的创建参数。
// 目标客户端是 Codex CLI 及官方 openai SDK，字段与 OpenAI 文档对齐。
type ResponsesRequest struct {
	Model              string            `json:"model"`
	Instructions       string            `json:"instructions"`
	Input              any               `json:"input"` // string 或 item 数组
	Stream             bool              `json:"stream"`
	Background         bool              `json:"background"`
	Store              *bool             `json:"store"`
	PreviousResponseID string            `json:"previous_response_id"`
	Tools              []ResponsesTool   `json:"tools"`
	ToolChoice         any               `json:"tool_choice"`
	ParallelToolCalls  *bool             `json:"parallel_tool_calls"`
	Temperature        *float64          `json:"temperature"`
	TopP               *float64          `json:"top_p"`
	MaxOutputTokens    *int              `json:"max_output_tokens"`
	Reasoning          *ResponsesReason  `json:"reasoning"`
	Text               *ResponsesText    `json:"text"`
	Truncation         string            `json:"truncation"`
	User               string            `json:"user"`
	Metadata           map[string]string `json:"metadata"`
	Include            []string          `json:"include"`
	PromptCacheKey     string            `json:"prompt_cache_key"`
	ServiceTier        string            `json:"service_tier"`
	MaxToolCalls       *int              `json:"max_tool_calls"`
}

type ResponsesReason struct {
	Effort  string `json:"effort"`
	Summary string `json:"summary"`
}

type ResponsesText struct {
	Format *ResponsesTextFormat `json:"format"`
}

type ResponsesTextFormat struct {
	Type string `json:"type"` // text | json_object | json_schema
}

// ResponsesTool 同时兼容 Responses 扁平形态
// （{type:"function", name, parameters}）与 chat 嵌套形态。
type ResponsesTool struct {
	Type        string           `json:"type"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Parameters  json.RawMessage  `json:"parameters"`
	Strict      *bool            `json:"strict"`
	Function    *ToolFunctionDef `json:"function"`
}

type ToolFunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      *bool           `json:"strict"`
}

// ResponsesInputItem 是 input 数组里的单个条目。
// 宽松解析：type/role/content 各客户端写法不一，尽量吸收。
type ResponsesInputItem struct {
	Type      string `json:"type"`
	Role      string `json:"role"`
	Content   any    `json:"content"` // string 或 part 数组
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Output    any    `json:"output"` // string 或 part 数组
	ID        string `json:"id"`
	Status    string `json:"status"`
	Summary   any    `json:"summary"` // reasoning item
}

// ToMessages 把 Responses 请求转换为 Chat Completions 风格的
// messages / tools / toolChoice，供内部网关复用。
func (r *ResponsesRequest) ToMessages() (messages []map[string]any, tools any, toolChoice any) {
	messages = make([]map[string]any, 0, 8)

	if inst := strings.TrimSpace(r.Instructions); inst != "" {
		messages = append(messages, map[string]any{"role": "system", "content": inst})
	}

	switch input := r.Input.(type) {
	case nil:
		// 无 input，仅 instructions
	case string:
		if strings.TrimSpace(input) != "" {
			messages = append(messages, map[string]any{"role": "user", "content": input})
		}
	default:
		for _, item := range toItemList(r.Input) {
			messages = appendItemAsMessages(messages, item)
		}
	}

	if len(r.Tools) > 0 {
		chatTools := make([]map[string]any, 0, len(r.Tools))
		searchAdded := false
		for _, t := range r.Tools {
			if t.IsBuiltinWebSearch() {
				if SearchDisabled(r.ToolChoice) || searchAdded {
					continue
				}
				searchAdded = true
			}
			if fn := t.toChatTool(); fn != nil {
				chatTools = append(chatTools, fn)
			}
			// 其他尚未支持的内置工具不会转给上游。
		}
		if len(chatTools) > 0 {
			tools = chatTools
		}
	}

	toolChoice = normalizeResponsesToolChoice(r.ToolChoice)
	return provider.PromoteToolImages(messages), tools, toolChoice
}

// ReasoningEffort 提取 reasoning.effort，兼容缺省。
func (r *ResponsesRequest) ReasoningEffort() string {
	if r.Reasoning == nil {
		return ""
	}
	return strings.TrimSpace(r.Reasoning.Effort)
}

// toItemList 把 input 的任意 JSON 形态收敛为 item 切片。
func toItemList(input any) []ResponsesInputItem {
	raw, err := json.Marshal(input)
	if err != nil {
		return nil
	}
	var items []ResponsesInputItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	return items
}

// appendItemAsMessages 把单个 Responses item 展开为 chat messages。
func appendItemAsMessages(messages []map[string]any, item ResponsesInputItem) []map[string]any {
	typ := strings.ToLower(strings.TrimSpace(item.Type))
	// 无 type 时按 role 推断为 message（Codex 常省略 type）。
	if typ == "" && item.Role != "" {
		typ = "message"
	}
	switch typ {
	case "message":
		role := normalizeResponsesRole(item.Role)
		content := responsesContentToChat(item.Content)
		if content == nil {
			return messages
		}
		return append(messages, map[string]any{"role": role, "content": content})
	case "function_call":
		// 历史 assistant 工具调用：转成 assistant 消息携带 tool_calls，
		// 保证后续 function_call_output 能被上游关联。
		callID := item.CallID
		if callID == "" {
			callID = item.ID
		}
		return append(messages, map[string]any{
			"role":    "assistant",
			"content": nil,
			"tool_calls": []map[string]any{{
				"id":   callID,
				"type": "function",
				"function": map[string]any{
					"name":      item.Name,
					"arguments": item.Arguments,
				},
			}},
		})
	case "function_call_output":
		msg := map[string]any{
			"role":    "tool",
			"content": provider.NormalizeToolOutput(item.Output),
		}
		if item.CallID != "" {
			msg["tool_call_id"] = item.CallID
		}
		return append(messages, msg)
	case "reasoning":
		// 历史推理项不回放给上游（上下文已由后续消息携带）。
		return messages
	case "item_reference":
		// previous_response_id 引用：无服务端存储，无法解析，跳过。
		return messages
	default:
		// 未知类型按 message 尝试，避免丢用户输入。
		if item.Role != "" {
			if content := responsesContentToChat(item.Content); content != nil {
				return append(messages, map[string]any{
					"role":    normalizeResponsesRole(item.Role),
					"content": content,
				})
			}
		}
		return messages
	}
}

// normalizeResponsesRole 把 Responses role 映射为 chat role。
// Responses 里 developer 等价于 system。
func normalizeResponsesRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "developer", "system":
		return "system"
	case "assistant":
		return "assistant"
	case "tool":
		return "tool"
	default:
		return "user"
	}
}

// responsesContentToChat 把 Responses message.content 转成 chat content。
// user 侧输入多为 [{type:"input_text",text}]，assistant 侧为 [{type:"output_text",text}]。
func responsesContentToChat(content any) any {
	content = provider.ResponsesContent(content)
	switch c := content.(type) {
	case nil:
		return nil
	case string:
		if strings.TrimSpace(c) == "" {
			return nil
		}
		return c
	}
	return content
}

// MaxTokens 提取 max_output_tokens。
func (r *ResponsesRequest) MaxTokens() int {
	if r.MaxOutputTokens != nil && *r.MaxOutputTokens > 0 {
		return *r.MaxOutputTokens
	}
	return 0
}

// BuiltinWebSearchToolName 是代理在 Chat / Responses 显式搜索声明与上游
// chat function 工具之间使用的桥接名称。
const BuiltinWebSearchToolName = "buddy_proxy_builtin_web_search"

// IsBuiltinWebSearch 报告该 tool 是否为 OpenAI 内置联网搜索工具。
// Codex 等客户端使用 type=web_search；部分版本用 web_search_preview。
func (t ResponsesTool) IsBuiltinWebSearch() bool {
	if t.Function != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(t.Type)) {
	case "web_search", "web_search_preview":
		return true
	}
	return false
}

// HasBuiltinWebSearch 报告请求是否要求代理执行内置联网搜索。
func (r *ResponsesRequest) HasBuiltinWebSearch() bool {
	if SearchDisabled(r.ToolChoice) {
		return false
	}
	for _, tool := range r.Tools {
		if tool.IsBuiltinWebSearch() {
			return true
		}
	}
	return false
}

// WithoutBuiltinWebSearch 从已转换的 chat tools 中移除内置搜索桥接工具。
// 代理仅执行一轮官方搜索；回填结果后不再给模型搜索工具，避免循环扣费。
func WithoutBuiltinWebSearch(tools any) any {
	list, ok := tools.([]map[string]any)
	if !ok {
		return tools
	}
	out := make([]map[string]any, 0, len(list))
	for _, tool := range list {
		fn, _ := tool["function"].(map[string]any)
		name, _ := fn["name"].(string)
		if strings.EqualFold(strings.TrimSpace(name), BuiltinWebSearchToolName) {
			continue
		}
		out = append(out, tool)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// toChatTool 把 Responses 扁平 tool 转成 chat 的 {type:"function", function:{...}}。
func (t ResponsesTool) toChatTool() map[string]any {
	// 已是 chat 嵌套风格
	if t.Function != nil {
		fn := map[string]any{"name": t.Function.Name}
		if t.Function.Description != "" {
			fn["description"] = t.Function.Description
		}
		if len(t.Function.Parameters) > 0 {
			var params any
			if err := json.Unmarshal(t.Function.Parameters, &params); err == nil {
				fn["parameters"] = params
			}
		}
		if t.Function.Strict != nil {
			fn["strict"] = *t.Function.Strict
		}
		return map[string]any{"type": "function", "function": fn}
	}
	typ := strings.ToLower(strings.TrimSpace(t.Type))
	if t.IsBuiltinWebSearch() {
		// 上游 chat 不认识内置 web_search。桥接成保留名称的 function 工具，
		// 由代理在服务端执行后回填结果（见 server/responses_search.go）。
		return map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        BuiltinWebSearchToolName,
				"description": "Search the public web. Call this when the answer needs current facts, news, prices, or sources. Provide one concise query.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "The search query.",
						},
						"allowed_domains": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "Optional domain allowlist.",
						},
						"freshness": map[string]any{
							"type":        "string",
							"description": "Optional recency filter: d1-d30, m1-m12, or y1-y5.",
						},
					},
					"required": []string{"query"},
				},
			},
		}
	}
	if typ != "function" || t.Name == "" {
		// file_search / computer_use 等其他内置工具，跳过。
		return nil
	}
	fn := map[string]any{"name": t.Name}
	if t.Description != "" {
		fn["description"] = t.Description
	}
	if len(t.Parameters) > 0 {
		var params any
		if err := json.Unmarshal(t.Parameters, &params); err == nil {
			fn["parameters"] = params
		}
	}
	if t.Strict != nil {
		fn["strict"] = *t.Strict
	}
	return map[string]any{"type": "function", "function": fn}
}

// normalizeResponsesToolChoice 把 Responses tool_choice 转成 chat 格式。
// 支持 "auto"|"none"|"required" 字符串，以及 {type:"function",name} 对象。
func normalizeResponsesToolChoice(choice any) any {
	switch c := choice.(type) {
	case nil:
		return nil
	case string:
		switch strings.ToLower(c) {
		case "auto", "none", "required":
			return strings.ToLower(c)
		default:
			return "auto"
		}
	case map[string]any:
		typ, _ := c["type"].(string)
		if strings.EqualFold(typ, "function") {
			name, _ := c["name"].(string)
			if name == "" {
				if fn, ok := c["function"].(map[string]any); ok {
					name, _ = fn["name"].(string)
				}
			}
			if name != "" {
				return map[string]any{
					"type":     "function",
					"function": map[string]any{"name": name},
				}
			}
		}
		return "auto"
	}
	return nil
}
