package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wnddd839/codebuddy-proxy/internal/accounts"
	"github.com/wnddd839/codebuddy-proxy/internal/config"
)

type limitedUpstream struct {
	requests []map[string]any
}

func (transport *limitedUpstream) RoundTrip(req *http.Request) (*http.Response, error) {
	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		return nil, err
	}
	transport.requests = append(transport.requests, body)
	// 模拟 Issue 中只识别 max_tokens 的上游，未收到时生成完整答案。
	tokens, reason := 731, "stop"
	if limit, ok := body["max_tokens"].(float64); ok {
		tokens, reason = int(limit), "length"
	}
	frame, err := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"delta": map[string]any{"content": "answer"}, "finish_reason": reason}},
		"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": tokens, "total_tokens": 10 + tokens},
	})
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader("data: " + string(frame) + "\n\ndata: [DONE]\n\n")), Request: req,
	}, nil
}

func TestOutputLimitAcrossEndpoints(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, test := range []struct {
			name, path, fields string
			want               int
		}{
			{"chat_legacy", "/v1/chat/completions", `"max_tokens":16`, 16},
			{"chat_modern", "/v1/chat/completions", `"max_completion_tokens":8`, 8},
			{"chat_precedence", "/v1/chat/completions", `"max_tokens":50,"max_completion_tokens":4`, 4},
			{"chat_unlimited", "/v1/chat/completions", "", 0},
			{"responses", "/v1/responses", `"max_output_tokens":8`, 8},
			{"responses_unlimited", "/v1/responses", "", 0},
		} {
			t.Run(fmt.Sprintf("%s/stream=%v", test.name, stream), func(t *testing.T) {
				srv := testServerCfg(t, config.Config{RequireAPIKey: true, Site: "domestic"})
				account, _, err := srv.Svc.Pool.Upsert(accounts.CreateAccount(accounts.Account{
					Site: "domestic", BearerToken: "fixture", Enabled: true,
				}))
				if err != nil {
					t.Fatal(err)
				}
				srv.Svc.SetAPIKeys([]config.APIKeyBinding{{Key: "fixture-key", AccountID: account.ID}})
				transport := &limitedUpstream{}
				srv.Svc.Provider.HTTP = &http.Client{Transport: transport}
				input := `"messages":[{"role":"user","content":"write an essay"}]`
				responses := test.path == "/v1/responses"
				if responses {
					input = `"input":"write an essay"`
				}
				fields := ""
				if test.fields != "" {
					fields = test.fields + ","
				}
				body := fmt.Sprintf(`{"model":"auto","stream":%v,%s%s}`, stream, fields, input)
				req := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(body))
				req.Header.Set("Authorization", "Bearer fixture-key")
				rec := httptest.NewRecorder()
				srv.HTTP.Handler.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("status=%d: %s", rec.Code, rec.Body.String())
				}
				if len(transport.requests) != 1 {
					t.Fatalf("上限处理不应重试: %d", len(transport.requests))
				}
				upstream := transport.requests[0]
				for _, key := range []string{"max_tokens", "max_completion_tokens"} {
					value, exists := upstream[key]
					if test.want > 0 && value != float64(test.want) {
						t.Errorf("上游 %s=%v，期望 %d", key, value, test.want)
					} else if test.want == 0 && exists {
						t.Errorf("未指定上限却发送了 %s", key)
					}
				}
				var result map[string]any
				if !stream {
					if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
				} else {
					// 使用客户端可见的 SSE 终止帧，不只检查服务内部快照。
					for line := range strings.SplitSeq(rec.Body.String(), "\n") {
						data, ok := strings.CutPrefix(line, "data: ")
						if !ok || data == "[DONE]" {
							continue
						}
						var frame map[string]any
						if err := json.Unmarshal([]byte(data), &frame); err != nil {
							t.Fatal(err)
						}
						if responses {
							if response, ok := frame["response"].(map[string]any); ok {
								result = response
							}
						} else if choices, ok := frame["choices"].([]any); ok && len(choices) > 0 {
							if choices[0].(map[string]any)["finish_reason"] != nil {
								result = frame
							}
						}
					}
				}
				if result == nil {
					t.Fatal("缺少终止响应")
				}
				if responses {
					want := "completed"
					if test.want > 0 {
						want = "incomplete"
						details, _ := result["incomplete_details"].(map[string]any)
						if details["reason"] != "max_output_tokens" {
							t.Errorf("缺少输出截断原因: %#v", details)
						}
					}
					if result["status"] != want {
						t.Errorf("Responses status=%v，期望 %s", result["status"], want)
					}
					usage, _ := result["usage"].(map[string]any)
					wantTokens := test.want
					if wantTokens == 0 {
						wantTokens = 731
					}
					if usage["output_tokens"] != float64(wantTokens) {
						t.Errorf("输出 token 用量未保留: %#v", usage)
					}
					if stream && test.want > 0 && !strings.Contains(rec.Body.String(), "event: response.incomplete") {
						t.Error("截断时未发送 response.incomplete 事件")
					}
				} else {
					want := "stop"
					if test.want > 0 {
						want = "length"
					}
					choice := result["choices"].([]any)[0].(map[string]any)
					if choice["finish_reason"] != want {
						t.Errorf("finish_reason=%v，期望 %s", choice["finish_reason"], want)
					}
				}
			})
		}
	}
}
