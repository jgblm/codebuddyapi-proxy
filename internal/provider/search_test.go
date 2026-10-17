package provider

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type searchTransport struct {
	mu     sync.Mutex
	paths  []string
	bodies []string
	reply  func(path, body string) *http.Response
}

func (t *searchTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	raw, _ := io.ReadAll(req.Body)
	t.mu.Lock()
	t.paths = append(t.paths, req.URL.Path)
	t.bodies = append(t.bodies, string(raw))
	t.mu.Unlock()
	if t.reply != nil {
		return t.reply(req.URL.Path, string(raw)), nil
	}
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     http.StatusText(http.StatusOK),
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(`{}`)),
		Request:    req,
	}, nil
}

func TestResolveProtocolDirectAgentToolEndpoint(t *testing.T) {
	cases := []struct {
		name string
		opts ChatOptions
		want string
	}{
		{
			name: "domestic codebuddy",
			opts: ChatOptions{Site: "domestic", Product: "codebuddy"},
			want: "https://copilot.tencent.com/agenttool/v1/search",
		},
		{
			name: "global codebuddy",
			opts: ChatOptions{Site: "global", Product: "codebuddy"},
			want: "https://www.codebuddy.ai/agenttool/v1/search",
		},
		{
			name: "domestic workbuddy",
			opts: ChatOptions{Site: "domestic", Product: "workbuddy"},
			want: "https://www.workbuddy.cn/agenttool/v1/search",
		},
		{
			name: "global workbuddy",
			opts: ChatOptions{Site: "global", Product: "workbuddy"},
			want: "https://www.workbuddy.ai/agenttool/v1/search",
		},
		{
			name: "custom chat endpoint keeps host",
			opts: ChatOptions{Site: "global", APIEndpoint: "https://www.codebuddy.ai/v2/chat/completions"},
			want: "https://www.codebuddy.ai/agenttool/v1/search",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveProtocolDirectAgentToolEndpoint(tc.opts, agentToolSearchPath); got != tc.want {
				t.Fatalf("endpoint=%q want %q", got, tc.want)
			}
		})
	}
}

func TestWebSearchParsesResultsAndFilters(t *testing.T) {
	transport := &searchTransport{reply: func(path, body string) *http.Response {
		header := make(http.Header)
		header.Set("Content-Type", "application/json")
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     http.StatusText(http.StatusOK),
			Header:     header,
			Body: io.NopCloser(strings.NewReader(
				`{"query":"q","type":"text2text","provider":"0","total_results":1,` +
					`"results":[{"title":"T","url":"https://example.com","snippet":"S"}],"response_time_ms":12}`)),
			Request: nil,
		}
	}}
	client := &Client{HTTP: &http.Client{Transport: transport}}
	result, err := client.WebSearch(t.Context(), ChatOptions{
		Site:        "domestic",
		Product:     "codebuddy",
		BearerToken: "tok",
		UserID:      "u1",
	}, WebSearchRequest{
		Query:          "q",
		MaxResults:     3,
		BlockedDomains: []string{"a.com", "b.com"},
		AllowedDomains: []string{"example.com"},
		Freshness:      "m3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalResults != 1 || len(result.Results) != 1 || result.Results[0].URL != "https://example.com" {
		t.Fatalf("result=%+v", result)
	}
	if len(transport.paths) != 1 || transport.paths[0] != agentToolSearchPath {
		t.Fatalf("paths=%v", transport.paths)
	}
	body := transport.bodies[0]
	for _, want := range []string{`"max_results":3`, `"freshness":"m3"`, `"allowed_domains":["example.com"]`, `-site:a.com -site:b.com`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}

func TestWebSearchUpstreamErrorBecomesChatError(t *testing.T) {
	transport := &searchTransport{reply: func(path, body string) *http.Response {
		header := make(http.Header)
		header.Set("Content-Type", "application/json")
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Status:     http.StatusText(http.StatusTooManyRequests),
			Header:     header,
			Body:       io.NopCloser(strings.NewReader(`{"code":15001,"msg":"quota_web_search"}`)),
			Request:    nil,
		}
	}}
	client := &Client{HTTP: &http.Client{Transport: transport}}
	_, err := client.WebSearch(t.Context(), ChatOptions{Site: "domestic"}, WebSearchRequest{Query: "q"})
	if err == nil {
		t.Fatal("want error")
	}
	chatErr, ok := errors.AsType[*ChatError](err)
	if !ok {
		t.Fatalf("err type=%T err=%v", err, err)
	}
	if chatErr.Status != http.StatusTooManyRequests || chatErr.RetryAfterHeader() != "" {
		t.Fatalf("chatErr=%+v", chatErr)
	}
}

func TestWebFetchParsesContent(t *testing.T) {
	transport := &searchTransport{reply: func(path, body string) *http.Response {
		header := make(http.Header)
		header.Set("Content-Type", "application/json")
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     http.StatusText(http.StatusOK),
			Header:     header,
			Body: io.NopCloser(strings.NewReader(
				`{"url":"https://example.com","title":"Example","content":"hello",` +
					`"content_type":"text/markdown","content_length":5,"status_code":200}`)),
			Request: nil,
		}
	}}
	client := &Client{HTTP: &http.Client{Transport: transport}}
	result, err := client.WebFetch(t.Context(), ChatOptions{Site: "domestic"}, WebFetchRequest{
		URL:    "https://example.com",
		Prompt: "summarize",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "hello" || result.Title != "Example" || result.StatusCode != 200 {
		t.Fatalf("result=%+v", result)
	}
	if !strings.Contains(transport.bodies[0], `"prompt":"summarize"`) {
		t.Fatalf("body=%s", transport.bodies[0])
	}
}

func TestAgenticSearchCollectsSynthesis(t *testing.T) {
	sse := "event: iteration\ndata: {\"agent\":\"main\",\"turn\":1,\"phase\":\"start\"}\n\n" +
		"event: tool_call\ndata: {\"tool\":\"websearch\",\"call_id\":\"c1\"}\n\n" +
		"event: synthesis_delta\ndata: {\"delta\":\"partial\"}\n\n" +
		"event: done\ndata: {\"synthesis\":{\"content\":\"final answer\"},\"request_id\":\"r1\"}\n\n"
	transport := &searchTransport{reply: func(path, body string) *http.Response {
		header := make(http.Header)
		header.Set("Content-Type", "text/event-stream")
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     http.StatusText(http.StatusOK),
			Header:     header,
			Body:       io.NopCloser(strings.NewReader(sse)),
			Request:    nil,
		}
	}}
	client := &Client{HTTP: &http.Client{Transport: transport}}
	var seen []string
	result, err := client.AgenticSearch(t.Context(), ChatOptions{Site: "domestic"}, "研究", func(event AgenticSearchEvent) {
		seen = append(seen, event.Type)
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "final answer" {
		t.Fatalf("content=%q", result.Content)
	}
	if len(seen) != 4 || seen[3] != "done" {
		t.Fatalf("events=%v", seen)
	}
	if len(result.Events) != 4 {
		t.Fatalf("events=%+v", result.Events)
	}
	if !strings.Contains(transport.bodies[0], `"biz_via":"public"`) ||
		!strings.Contains(transport.bodies[0], `"search_mode":2`) {
		t.Fatalf("body=%s", transport.bodies[0])
	}
}

func TestAgenticSearchMissingSynthesisIsError(t *testing.T) {
	sse := "event: tool_call\ndata: {\"tool\":\"websearch\"}\n\n"
	transport := &searchTransport{reply: func(path, body string) *http.Response {
		header := make(http.Header)
		header.Set("Content-Type", "text/event-stream")
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     http.StatusText(http.StatusOK),
			Header:     header,
			Body:       io.NopCloser(strings.NewReader(sse)),
			Request:    nil,
		}
	}}
	client := &Client{HTTP: &http.Client{Transport: transport}}
	if _, err := client.AgenticSearch(t.Context(), ChatOptions{Site: "domestic"}, "研究", nil); err == nil {
		t.Fatal("want error when synthesis is missing")
	}
}
