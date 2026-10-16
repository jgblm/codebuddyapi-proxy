package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wnddd839/codebuddy-proxy/internal/accounts"
	"github.com/wnddd839/codebuddy-proxy/internal/config"
)

type contentCaptureTransport struct {
	messages []map[string]any
}

func (transport *contentCaptureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		return nil, err
	}
	transport.messages = body.Messages
	return &http.Response{
		StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}},
		Body:    io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")),
		Request: req,
	}, nil
}

func TestResponsesToolImagesReachUpstreamAsImages(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[stream], func(t *testing.T) {
			srv := testServerCfg(t, config.Config{RequireAPIKey: true, Site: "domestic"})
			account, _, err := srv.Svc.Pool.Upsert(accounts.CreateAccount(accounts.Account{
				Site: "domestic", BearerToken: "fixture", Enabled: true,
			}))
			if err != nil {
				t.Fatal(err)
			}
			srv.Svc.SetAPIKeys([]config.APIKeyBinding{{Key: "fixture-key", AccountID: account.ID}})
			transport := &contentCaptureTransport{}
			srv.Svc.Provider.HTTP = &http.Client{Transport: transport}
			image := "data:image/png;base64," + strings.Repeat("A", 220000)
			output, _ := json.Marshal([]any{map[string]any{"image_url": image, "detail": "high"}})
			body, _ := json.Marshal(map[string]any{
				"model": "auto", "stream": stream, "input": []any{
					map[string]any{"type": "message", "role": "user", "content": "看截图"},
					map[string]any{"type": "function_call", "call_id": "c1", "name": "view_image", "arguments": "{}"},
					map[string]any{"type": "function_call_output", "call_id": "c1", "output": string(output)},
				},
			})
			req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(string(body)))
			req.Header.Set("Authorization", "Bearer fixture-key")
			rec := httptest.NewRecorder()
			srv.HTTP.Handler.ServeHTTP(rec, req)
			if rec.Code != 200 {
				t.Fatalf("status=%d", rec.Code)
			}
			foundImage, foundTool := false, false
			for _, message := range transport.messages {
				if text, ok := message["content"].(string); ok && strings.Contains(text, image) {
					t.Fatal("上游收到 base64 文本")
				}
				if message["role"] == "tool" && message["tool_call_id"] == "c1" {
					foundTool = true
				}
				if parts, ok := message["content"].([]any); ok {
					for _, part := range parts {
						m, _ := part.(map[string]any)
						if m["type"] == "image_url" {
							foundImage = true
						}
					}
				}
			}
			if !foundImage || !foundTool {
				t.Fatal("上游消息缺少图片或工具关联")
			}
		})
	}
}
