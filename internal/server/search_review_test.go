package server

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wnddd839/codebuddy-proxy/internal/accounts"
	"github.com/wnddd839/codebuddy-proxy/internal/config"
	"github.com/wnddd839/codebuddy-proxy/internal/openai"
	"github.com/wnddd839/codebuddy-proxy/internal/provider"
)

type reviewTransportFunc func(*http.Request) (*http.Response, error)

func (fn reviewTransportFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func reviewResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func TestBuiltinSearchPreservesFixedAccountAndTotalUsage(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/chat/completions"} {
		t.Run(path, func(t *testing.T) { testFixedAccountSearchUsage(t, path) })
	}
}

func testFixedAccountSearchUsage(t *testing.T, path string) {
	t.Helper()
	srv := testServerCfg(t, config.Config{RequireAPIKey: true, APIKey: "main", Site: "domestic"})
	fixed, _, err := srv.Svc.Pool.Upsert(accounts.CreateAccount(accounts.Account{
		Site: "domestic", BearerToken: "token-fixed", Enabled: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := srv.Svc.Pool.Upsert(accounts.CreateAccount(accounts.Account{
		Site: "domestic", BearerToken: "token-other", Enabled: true,
	})); err != nil {
		t.Fatal(err)
	}
	srv.Svc.SetAPIKeys([]config.APIKeyBinding{{Key: "fixed", AccountID: fixed.ID}})
	var mu sync.Mutex
	var searchTokens []string
	chats := 0
	srv.Svc.Provider.HTTP = &http.Client{Transport: reviewTransportFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		if req.URL.Path == "/agenttool/v1/search" {
			token := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
			searchTokens = append(searchTokens, token)
			return reviewResponse(req, http.StatusTooManyRequests, `{"msg":"rate limited"}`), nil
		}
		if strings.Contains(req.URL.Path, "chat") {
			chats++
			body := `data: {"choices":[{"delta":{"content":"answer"}}]}` + "\n\n"
			if chats == 1 {
				body = `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"` + openai.BuiltinWebSearchToolName + `","arguments":"{\"query\":\"Go\"}"}}]}}]}` + "\n\n"
			}
			body += `data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"credit":1}}` + "\n\ndata: [DONE]\n\n"
			return reviewResponse(req, http.StatusOK, body), nil
		}
		return reviewResponse(req, http.StatusOK, `{"code":0,"data":{}}`), nil
	})}
	body := `{"model":"auto","input":"Go news","tools":[{"type":"web_search"}]}`
	if path == "/v1/chat/completions" {
		body = `{"model":"auto","messages":[{"role":"user","content":"Go news"}],"web_search_options":{}}`
	}
	rec := doRequest(srv, newAuthRequest("POST", path, body, "fixed"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(searchTokens) != 1 || searchTokens[0] != "token-fixed" {
		t.Fatalf("fixed-key search changed account: %v", searchTokens)
	}
	var result struct {
		Usage struct {
			Input  int `json:"input_tokens"`
			Output int `json:"output_tokens"`
			Total  int `json:"total_tokens"`
			Prompt int `json:"prompt_tokens"`
			Tokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if path == "/v1/chat/completions" {
		result.Usage.Input, result.Usage.Output = result.Usage.Prompt, result.Usage.Tokens
	}
	if result.Usage.Input != 20 || result.Usage.Output != 4 || result.Usage.Total != 24 {
		t.Fatalf("first model round missing from usage: %+v", result.Usage)
	}
}

func TestBuiltinSearchFlushesCreatedBeforeUpstreamCompletes(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/chat/completions"} {
		t.Run(path, func(t *testing.T) { testSearchFlushesBeforeUpstream(t, path) })
	}
}

func testSearchFlushesBeforeUpstream(t *testing.T, path string) {
	t.Helper()
	srv := testServer(t, true, "", "secret")
	seedBothSites(t, srv)
	release := make(chan struct{})
	defer close(release)
	srv.Svc.Provider.HTTP = &http.Client{Transport: reviewTransportFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "chat") {
			select {
			case <-release:
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
		}
		return reviewResponse(req, http.StatusOK, `{"code":0,"data":{}}`), nil
	})}
	httpSrv := httptest.NewServer(srv.HTTP.Handler)
	defer httpSrv.Close()
	client := &http.Client{Timeout: 3 * time.Second}
	body := `{"model":"auto","input":"news","tools":[{"type":"web_search"}],"stream":true}`
	if path == "/v1/chat/completions" {
		body = `{"model":"auto","messages":[{"role":"user","content":"news"}],"web_search_options":{},"stream":true}`
	}
	req, err := http.NewRequestWithContext(t.Context(), "POST", httpSrv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("SSE headers waited for upstream: %v", err)
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil {
		t.Fatalf("first SSE frame: %v", err)
	}
	if path == "/v1/responses" && strings.TrimSpace(line) != "event: response.created" {
		t.Fatalf("missing immediate created frame: %q %v", line, err)
	}
	if path == "/v1/chat/completions" && !strings.Contains(line, `"role":"assistant"`) {
		t.Fatalf("missing immediate chat role frame: %q", line)
	}
}

func TestSumSearchUsagePreservesCacheAndKnownCredits(t *testing.T) {
	got := sumSearchUsage(
		provider.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12, CacheReadInputTokens: 4, Credit: new(1.5)},
		provider.Usage{PromptTokens: 20, CompletionTokens: 3, TotalTokens: 23, PromptCacheHitTokens: 5, Credit: new(2.5)},
	)
	if got.TotalTokens != 35 || got.CachedTokens() != 9 || got.Credit == nil || *got.Credit != 4 {
		t.Fatalf("usage=%+v", got)
	}
	if sumSearchUsage(provider.Usage{Credit: new(1.5)}, provider.Usage{}).Credit != nil {
		t.Fatal("unknown charge must not be reported as a known total")
	}
}
