package openai

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChatSearchToolsExplicitOnlyAndNoMutation(t *testing.T) {
	client := map[string]any{"type": "function", "function": map[string]any{"name": "web_search"}}
	tools := []map[string]any{client}
	before, _ := json.Marshal(tools)
	got, enabled, err := ChatSearchTools(tools, false, nil)
	if err != nil || enabled {
		t.Fatalf("ordinary function enabled hosted search: %v %v", enabled, err)
	}
	raw, _ := json.Marshal(got)
	if string(raw) != string(before) {
		t.Fatalf("ordinary tools changed: %s", raw)
	}
	got, enabled, err = ChatSearchTools(tools, true, nil)
	if err != nil || !enabled {
		t.Fatalf("explicit search not enabled: %v %v", enabled, err)
	}
	list := got.([]map[string]any)
	if len(list) != 2 {
		t.Fatalf("client tool not preserved: %+v", list)
	}
	raw, _ = json.Marshal(tools)
	if string(raw) != string(before) {
		t.Fatal("input tools mutated")
	}
}

func TestChatSearchRejectsMalformedToolsWhenRequested(t *testing.T) {
	for _, tools := range []any{map[string]any{"type": "function"}, []any{"not-a-tool"}} {
		if _, _, err := ChatSearchTools(tools, true, nil); err == nil {
			t.Fatalf("malformed tools accepted: %+v", tools)
		}
	}
}

func TestResponsesToolChoiceNoneRemovesHostedSearch(t *testing.T) {
	req := &ResponsesRequest{Input: "news", ToolChoice: "none", Tools: []ResponsesTool{
		{Type: "web_search"}, {Type: "function", Name: "shell"},
	}}
	if req.HasBuiltinWebSearch() {
		t.Fatal("tool_choice none must disable hosted execution")
	}
	_, tools, _ := req.ToMessages()
	raw, _ := json.Marshal(tools)
	if strings.Contains(string(raw), BuiltinWebSearchToolName) || !strings.Contains(string(raw), "shell") {
		t.Fatalf("incorrect tool filtering: %s", raw)
	}
}
