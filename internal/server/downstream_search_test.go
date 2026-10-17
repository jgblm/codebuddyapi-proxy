package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/wnddd839/codebuddy-proxy/internal/openai"
)

type downstreamSearchTransport struct {
	mu       sync.Mutex
	chats    []map[string]any
	searches int
}

func (upstream *downstreamSearchTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if req.URL.Path == "/agenttool/v1/search" {
		upstream.searches++
		return reviewResponse(req, http.StatusOK,
			`{"query":"Go release","results":[{"title":"Go","url":"https://go.dev/doc/devel/release","snippet":"release evidence"}],"total_results":1}`), nil
	}
	if !strings.Contains(req.URL.Path, "chat") {
		return reviewResponse(req, http.StatusOK, `{"code":0,"data":{}}`), nil
	}
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	upstream.chats = append(upstream.chats, body)
	content := `data: {"choices":[{"delta":{"content":"answer: https://go.dev/doc/devel/release"}}]}` + "\n\n"
	if hasDownstreamTool(body, openai.BuiltinWebSearchToolName) {
		content = fmt.Sprintf(
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"hosted_1","type":"function","function":{"name":%q,"arguments":"{\"query\":\"Go release\"}"}}]}}]}`,
			openai.BuiltinWebSearchToolName) + "\n\n"
	}
	content += `data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}` + "\n\ndata: [DONE]\n\n"
	return reviewResponse(req, http.StatusOK, content), nil
}

func hasDownstreamTool(body map[string]any, name string) bool {
	tools, _ := body["tools"].([]any)
	for _, value := range tools {
		tool, _ := value.(map[string]any)
		fn, _ := tool["function"].(map[string]any)
		if fn["name"] == name {
			return true
		}
	}
	return false
}

func TestDownstreamSearchRequiresExplicitOptIn(t *testing.T) {
	for _, test := range []struct {
		name, path, extra string
		wantSearch        bool
	}{
		{"chat omitted", "/v1/chat/completions", "", false},
		{"chat options", "/v1/chat/completions", `,"web_search_options":{}`, true},
		{"chat null", "/v1/chat/completions", `,"web_search_options":null`, false},
		{"chat tool", "/v1/chat/completions", `,"tools":[{"type":"web_search"}]`, true},
		{"chat preview", "/v1/chat/completions", `,"tools":[{"type":"web_search_preview"}]`, true},
		{"chat none", "/v1/chat/completions", `,"web_search_options":{},"tool_choice":"none"`, false},
		{"chat builtin none", "/v1/chat/completions", `,"tools":[{"type":"web_search"}],"tool_choice":"none"`, false},
		{"chat client function", "/v1/chat/completions", `,"tools":[{"type":"function","function":{"name":"web_search","parameters":{"type":"object"}}}]`, false},
		{"responses omitted", "/v1/responses", "", false},
		{"responses tool", "/v1/responses", `,"tools":[{"type":"web_search"}]`, true},
		{"responses preview", "/v1/responses", `,"tools":[{"type":"web_search_preview"}]`, true},
		{"responses duplicates", "/v1/responses", `,"tools":[{"type":"web_search"},{"type":"web_search_preview"}]`, true},
		{"responses none", "/v1/responses", `,"tools":[{"type":"web_search"}],"tool_choice":"none"`, false},
		{"responses client function", "/v1/responses", `,"tools":[{"type":"function","name":"web_search","parameters":{"type":"object"}}]`, false},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", test.name, stream), func(t *testing.T) {
				srv := testServer(t, true, "", "test-key")
				seedBothSites(t, srv)
				upstream := &downstreamSearchTransport{}
				srv.Svc.Provider.HTTP = &http.Client{Transport: upstream}
				input := `"messages":[{"role":"user","content":"Go release"}]`
				if test.path == "/v1/responses" {
					input = `"input":"Go release"`
				}
				body := fmt.Sprintf(`{"model":"auto",%s,"stream":%t%s}`, input, stream, test.extra)
				rec := doRequest(srv, newAuthRequest("POST", test.path, body, "test-key"))
				if rec.Code != http.StatusOK {
					t.Fatalf("status=%d: %s", rec.Code, rec.Body.String())
				}
				upstream.mu.Lock()
				defer upstream.mu.Unlock()
				wantChats, wantSearches := 1, 0
				if test.wantSearch {
					wantChats, wantSearches = 2, 1
				}
				if len(upstream.chats) != wantChats || upstream.searches != wantSearches {
					t.Fatalf("chats=%d searches=%d; want %d/%d", len(upstream.chats), upstream.searches, wantChats, wantSearches)
				}
				if hasDownstreamTool(upstream.chats[0], openai.BuiltinWebSearchToolName) != test.wantSearch {
					t.Fatalf("search declaration not respected: %+v", upstream.chats[0]["tools"])
				}
				if test.wantSearch {
					last := upstream.chats[1]
					if hasDownstreamTool(last, openai.BuiltinWebSearchToolName) {
						t.Fatal("hosted tool leaked into the answer round")
					}
					raw, _ := json.Marshal(last["messages"])
					if !strings.Contains(string(raw), "release evidence") {
						t.Fatalf("official search results not returned to model: %s", raw)
					}
				}
				if strings.Contains(rec.Body.String(), openai.BuiltinWebSearchToolName) {
					t.Fatal("client was asked to execute internal search")
				}
				if !strings.Contains(rec.Body.String(), "https://go.dev/doc/devel/release") {
					t.Fatalf("answer did not reach downstream: %s", rec.Body.String())
				}
			})
		}
	}
}

func TestChatSearchPreservesClientTools(t *testing.T) {
	srv := testServer(t, true, "", "test-key")
	seedBothSites(t, srv)
	upstream := &downstreamSearchTransport{}
	srv.Svc.Provider.HTTP = &http.Client{Transport: upstream}
	rec := doRequest(srv, newAuthRequest("POST", "/v1/chat/completions",
		`{"model":"auto","messages":[{"role":"user","content":"Go"}],"web_search_options":{},"tools":[{"type":"function","function":{"name":"shell","parameters":{"type":"object"}}}]}`, "test-key"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if len(upstream.chats) != 2 || !hasDownstreamTool(upstream.chats[0], "shell") || !hasDownstreamTool(upstream.chats[1], "shell") {
		t.Fatalf("client tools lost: %v", upstream.chats)
	}
}
