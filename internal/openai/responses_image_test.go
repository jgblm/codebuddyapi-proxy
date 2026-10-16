package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wnddd839/codebuddy-proxy/internal/provider"
)

func TestResponsesToolImageNotSerializedAsText(t *testing.T) {
	image := "data:image/png;base64," + strings.Repeat("A", 220000)
	raw, _ := json.Marshal([]any{map[string]any{"image_url": image, "detail": "high"}})
	req := ResponsesRequest{Input: []ResponsesInputItem{
		{Type: "function_call", CallID: "c1", Name: "view_image", Arguments: "{}"},
		{Type: "function_call_output", CallID: "c1", Output: string(raw)},
	}}
	messages, _, _ := req.ToMessages()
	upstream := provider.EnsureUpstreamMessages(messages)
	if len(upstream) != 3 || upstream[1]["tool_call_id"] != "c1" || upstream[2]["role"] != "user" {
		t.Fatalf("工具与图片关联错误: %d", len(upstream))
	}
	for _, message := range upstream {
		if text, ok := message["content"].(string); ok && strings.Contains(text, image) {
			t.Fatal("base64 仍在文本上下文内")
		}
	}
}

func TestResponsesPureImageInputSurvives(t *testing.T) {
	req := ResponsesRequest{Input: []ResponsesInputItem{{Type: "message", Role: "user", Content: []any{
		map[string]any{"type": "input_image", "image_url": "https://example.invalid/image.png"},
	}}}}
	messages, _, _ := req.ToMessages()
	if len(messages) != 1 {
		t.Fatal("纯图片输入被丢弃")
	}
}
