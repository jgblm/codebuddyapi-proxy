package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/wnddd839/codebuddy-proxy/internal/openai"
)

// agentToolUpstream routes /agenttool/v1/* to canned JSON or SSE replies and
// records the bearer token so tests can assert pool routing.
type agentToolUpstream struct {
	mu      sync.Mutex
	paths   []string
	tokens  []string
	search  string
	fetch   string
	agentic string
}

func (t *agentToolUpstream) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.paths = append(t.paths, req.URL.Path)
	t.tokens = append(t.tokens, strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer "))
	t.mu.Unlock()
	header := make(http.Header)
	switch {
	case strings.HasSuffix(req.URL.Path, "/search"):
		header.Set("Content-Type", "application/json")
		body := t.search
		if body == "" {
			body = `{"query":"q","type":"text2text","provider":"0","total_results":1,` +
				`"results":[{"title":"T","url":"https://example.test/a","snippet":"S"}],"response_time_ms":9}`
		}
		return &http.Response{StatusCode: http.StatusOK, Status: http.StatusText(http.StatusOK), Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	case strings.HasSuffix(req.URL.Path, "/webfetch"):
		header.Set("Content-Type", "application/json")
		body := t.fetch
		if body == "" {
			body = `{"url":"https://example.test","title":"T","content":"hello","content_type":"text/markdown","content_length":5,"status_code":200}`
		}
		return &http.Response{StatusCode: http.StatusOK, Status: http.StatusText(http.StatusOK), Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	case strings.HasSuffix(req.URL.Path, "/agentic_search"):
		header.Set("Content-Type", "text/event-stream")
		body := t.agentic
		if body == "" {
			body = "event: iteration\ndata: {\"agent\":\"main\",\"turn\":1,\"phase\":\"start\"}\n\n" +
				"event: done\ndata: {\"synthesis\":{\"content\":\"final answer\"}}\n\n"
		}
		return &http.Response{StatusCode: http.StatusOK, Status: http.StatusText(http.StatusOK), Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	}
	header.Set("Content-Type", "text/event-stream")
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     http.StatusText(http.StatusOK),
		Header:     header,
		Body: io.NopCloser(strings.NewReader(
			"data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")),
		Request: req,
	}, nil
}

func (t *agentToolUpstream) lastPath() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.paths) == 0 {
		return ""
	}
	return t.paths[len(t.paths)-1]
}

func TestSearchRouteReturnsResults(t *testing.T) {
	srv := testServer(t, true, "", "cbp_test")
	seedBothSites(t, srv)
	upstream := &agentToolUpstream{}
	srv.Svc.Provider.HTTP = &http.Client{Transport: upstream}

	req := httptest.NewRequest(http.MethodPost, "/v1/search", strings.NewReader(`{"query":"golang","max_results":3}`))
	req.Header.Set("Authorization", "Bearer cbp_test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.HTTP.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.HasSuffix(upstream.lastPath(), "/agenttool/v1/search") {
		t.Fatalf("path=%s", upstream.lastPath())
	}
	var payload struct {
		Object  string `json:"object"`
		Results []struct {
			URL string `json:"url"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Object != "search.result" || len(payload.Results) != 1 || payload.Results[0].URL != "https://example.test/a" {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestSearchRouteRequiresQuery(t *testing.T) {
	srv := testServer(t, true, "", "cbp_test")
	req := httptest.NewRequest(http.MethodPost, "/v1/search", strings.NewReader(`{"query":""}`))
	req.Header.Set("Authorization", "Bearer cbp_test")
	rec := httptest.NewRecorder()
	srv.HTTP.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWebFetchRouteRejectsNonHTTPURL(t *testing.T) {
	srv := testServer(t, true, "", "cbp_test")
	req := httptest.NewRequest(http.MethodPost, "/v1/webfetch", strings.NewReader(`{"url":"file:///etc/passwd"}`))
	req.Header.Set("Authorization", "Bearer cbp_test")
	rec := httptest.NewRecorder()
	srv.HTTP.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWebFetchRouteReturnsContent(t *testing.T) {
	srv := testServer(t, true, "", "cbp_test")
	seedBothSites(t, srv)
	upstream := &agentToolUpstream{}
	srv.Svc.Provider.HTTP = &http.Client{Transport: upstream}
	req := httptest.NewRequest(http.MethodPost, "/v1/webfetch", strings.NewReader(`{"url":"https://example.test"}`))
	req.Header.Set("Authorization", "Bearer cbp_test")
	rec := httptest.NewRecorder()
	srv.HTTP.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"content":"hello"`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestAgenticSearchRouteStreams(t *testing.T) {
	srv := testServer(t, true, "", "cbp_test")
	seedBothSites(t, srv)
	upstream := &agentToolUpstream{}
	srv.Svc.Provider.HTTP = &http.Client{Transport: upstream}
	req := httptest.NewRequest(http.MethodPost, "/v1/agentic_search", strings.NewReader(`{"query":"研究","stream":true}`))
	req.Header.Set("Authorization", "Bearer cbp_test")
	rec := httptest.NewRecorder()
	srv.HTTP.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	raw := rec.Body.String()
	for _, want := range []string{"event: iteration", "event: synthesis", "final answer", "data: [DONE]"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("stream missing %q: %s", want, raw)
		}
	}
}

func TestAgenticSearchRouteNonStream(t *testing.T) {
	srv := testServer(t, true, "", "cbp_test")
	seedBothSites(t, srv)
	upstream := &agentToolUpstream{}
	srv.Svc.Provider.HTTP = &http.Client{Transport: upstream}
	req := httptest.NewRequest(http.MethodPost, "/v1/agentic_search", strings.NewReader(`{"query":"研究"}`))
	req.Header.Set("Authorization", "Bearer cbp_test")
	rec := httptest.NewRecorder()
	srv.HTTP.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"content":"final answer"`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

// builtinSearchUpstream 第一轮要求 web_search，第二轮返回最终答案。
type builtinSearchUpstream struct {
	mu    sync.Mutex
	chats int
}

func (t *builtinSearchUpstream) RoundTrip(req *http.Request) (*http.Response, error) {
	header := make(http.Header)
	if strings.Contains(req.URL.Path, "/agenttool/v1/search") {
		header.Set("Content-Type", "application/json")
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     http.StatusText(http.StatusOK),
			Header:     header,
			Body: io.NopCloser(strings.NewReader(
				`{"query":"q","type":"text2text","total_results":1,"results":[{"title":"T","url":"https://example.test","snippet":"S"}]}`)),
			Request: req,
		}, nil
	}
	if !strings.Contains(req.URL.Path, "chat") {
		header.Set("Content-Type", "application/json")
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     http.StatusText(http.StatusOK),
			Header:     header,
			Body:       io.NopCloser(strings.NewReader(`{"models":[]}`)),
			Request:    req,
		}, nil
	}
	t.mu.Lock()
	t.chats++
	round := t.chats
	t.mu.Unlock()
	header.Set("Content-Type", "text/event-stream")
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\ndata: [DONE]\n\n"
	if round == 1 {
		body = "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"" + openai.BuiltinWebSearchToolName + "\",\"arguments\":\"{\\\"query\\\":\\\"golang\\\"}\"}}]}}]}\n\ndata: [DONE]\n\n"
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     http.StatusText(http.StatusOK),
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func TestResponsesBuiltinWebSearchRoundTrip(t *testing.T) {
	srv := testServer(t, true, "", "cbp_test")
	seedBothSites(t, srv)
	upstream := &builtinSearchUpstream{}
	srv.Svc.Provider.HTTP = &http.Client{Transport: upstream}
	body := `{"model":"auto","input":"latest go release","tools":[{"type":"web_search"}],"stream":false}`
	req := newAuthRequest(http.MethodPost, "/v1/responses", body, "cbp_test")
	rec := doRequest(srv, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if upstream.chats != 2 {
		t.Fatalf("chat rounds=%d want 2", upstream.chats)
	}
	if !strings.Contains(rec.Body.String(), `"answer"`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestResponsesBuiltinWebSearchStream(t *testing.T) {
	srv := testServer(t, true, "", "cbp_test")
	seedBothSites(t, srv)
	upstream := &builtinSearchUpstream{}
	srv.Svc.Provider.HTTP = &http.Client{Transport: upstream}
	body := `{"model":"auto","input":"latest go release","tools":[{"type":"web_search"}],"stream":true}`
	req := newAuthRequest(http.MethodPost, "/v1/responses", body, "cbp_test")
	rec := doRequest(srv, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	raw := rec.Body.String()
	for _, want := range []string{"response.created", "response.output_text.delta", "response.completed", "answer"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("stream missing %q: %s", want, raw)
		}
	}
}
