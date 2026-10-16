package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToolImagesPromotedAfterAllResults(t *testing.T) {
	image := map[string]any{"detail": "high", "image_url": "data:image/png;base64,AAAA"}
	raw, _ := json.Marshal([]any{map[string]any{"type": "text", "text": "截图"}, image})
	messages := []map[string]any{
		{"role": "assistant", "tool_calls": []any{
			map[string]any{"id": "a"}, map[string]any{"id": "b"},
		}},
		{"role": "tool", "tool_call_id": "a", "content": string(raw)},
		{"role": "tool", "tool_call_id": "b", "content": "done"},
	}
	got := EnsureUpstreamMessages(messages)
	if len(got) != 4 || got[1]["content"] != "截图" || got[2]["tool_call_id"] != "b" || got[3]["role"] != "user" {
		t.Fatalf("工具顺序或文字丢失: %#v", got)
	}
	parts := got[3]["content"].([]any)
	ref := parts[0].(map[string]any)["image_url"].(map[string]any)
	if ref["detail"] != "high" || ref["url"] != image["image_url"] {
		t.Fatalf("图片参数丢失: %#v", ref)
	}
	again, _ := json.Marshal(EnsureUpstreamMessages(got))
	first, _ := json.Marshal(got)
	if string(again) != string(first) {
		t.Fatal("重复归一化改变消息")
	}
	if messages[1]["content"] != string(raw) {
		t.Fatal("修改了调用方输入")
	}
}

func TestToolOutputOrdinaryJSONStaysText(t *testing.T) {
	for _, text := range []string{`{"path":"image.png"}`, `[{"text":"ordinary"}]`, `bad json`, ""} {
		if got := NormalizeToolOutput(text); got != text {
			t.Fatalf("普通工具文本被修改: %q", text)
		}
	}
}

func TestResponsesImagesRemainStructured(t *testing.T) {
	got := ResponsesContent([]any{
		map[string]any{"type": "input_text", "text": "看图"},
		map[string]any{"type": "input_image", "image_url": "https://example.invalid/x.png"},
	})
	raw, _ := json.Marshal(got)
	if !strings.Contains(string(raw), `"image_url":{"url":`) || !strings.Contains(string(raw), "看图") {
		t.Fatalf("多模态内容丢失: %s", raw)
	}
}
