package openai

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResponsesBuiltinWebSearchBridgedToFunction(t *testing.T) {
	var req ResponsesRequest
	if err := json.Unmarshal([]byte(`{
		"model":"auto",
		"input":"latest news",
		"tools":[
			{"type":"web_search"},
			{"type":"file_search"},
			{"type":"function","name":"shell","parameters":{"type":"object"}}
		]
	}`), &req); err != nil {
		t.Fatal(err)
	}
	if !req.HasBuiltinWebSearch() {
		t.Fatal("expected builtin web_search to be detected")
	}
	_, tools, _ := req.ToMessages()
	list, ok := tools.([]map[string]any)
	if !ok {
		t.Fatalf("tools=%T %+v", tools, tools)
	}
	var names []string
	for _, tool := range list {
		fn, _ := tool["function"].(map[string]any)
		name, _ := fn["name"].(string)
		names = append(names, name)
	}
	// web_search 桥接成 function；file_search 丢弃；客户端 function 原样保留。
	if len(names) != 2 || names[0] != BuiltinWebSearchToolName || names[1] != "shell" {
		t.Fatalf("names=%v", names)
	}
	raw, _ := json.Marshal(list[0])
	if !strings.Contains(string(raw), `"query"`) {
		t.Fatalf("bridged tool missing query parameter: %s", raw)
	}
}

func TestResponsesNoBuiltinWebSearch(t *testing.T) {
	var req ResponsesRequest
	if err := json.Unmarshal([]byte(`{"model":"auto","input":"hi"}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.HasBuiltinWebSearch() {
		t.Fatal("unexpected builtin web_search detection")
	}
	_, tools, _ := req.ToMessages()
	if tools != nil {
		t.Fatalf("tools=%+v", tools)
	}
}

func TestResponsesWebSearchPreviewIsDetected(t *testing.T) {
	var req ResponsesRequest
	if err := json.Unmarshal([]byte(`{"model":"auto","input":"x","tools":[{"type":"web_search_preview"}]}`), &req); err != nil {
		t.Fatal(err)
	}
	if !req.HasBuiltinWebSearch() {
		t.Fatal("web_search_preview should be detected")
	}
}

func TestRemovingBuiltinSearchPreservesClientWebSearchFunction(t *testing.T) {
	req := &ResponsesRequest{Tools: []ResponsesTool{
		{Type: "web_search"},
		{Type: "function", Name: "web_search"},
	}}
	_, tools, _ := req.ToMessages()
	filtered := WithoutBuiltinWebSearch(tools).([]map[string]any)
	if len(filtered) != 1 || filtered[0]["function"].(map[string]any)["name"] != "web_search" {
		t.Fatalf("client function was treated as a server builtin: %v", filtered)
	}
}
