package provider_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/wnddd839/codebuddy-proxy/internal/config"
	"github.com/wnddd839/codebuddy-proxy/internal/provider"
)

type outputLimitTransport struct {
	body map[string]any
}

func (transport *outputLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := json.NewDecoder(req.Body).Decode(&transport.body); err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n")),
		Request:    req,
	}, nil
}

func TestOutputLimitUsesUpstreamMaxTokens(t *testing.T) {
	for _, limit := range []int{0, -1, 4, 16, 200} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			transport := &outputLimitTransport{}
			client := provider.NewClient(config.Config{})
			client.HTTP = &http.Client{Transport: transport}
			_, err := client.Complete(t.Context(), provider.ChatOptions{
				Model: "auto", MaxCompletionTokens: limit,
				Messages: []map[string]any{{"role": "user", "content": "hello"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"max_tokens", "max_completion_tokens"} {
				got, exists := transport.body[key]
				if limit > 0 {
					if !exists || got != float64(limit) {
						t.Errorf("%s = %v，期望 %d", key, got, limit)
					}
				} else if exists {
					t.Errorf("未设置正数上限时不应发送 %s", key)
				}
			}
		})
	}
}

func TestOutputLimitFingerprintIncludesEffectiveField(t *testing.T) {
	got := provider.DescribeUpstreamBody(map[string]any{
		"max_tokens": 8, "max_completion_tokens": 8,
	})
	if got["max_tokens"] != "8" || got["max_completion_tokens"] != "8" {
		t.Fatalf("排障指纹缺少上限字段: %#v", got)
	}
}
