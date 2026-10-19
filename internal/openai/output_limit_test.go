package openai

import (
	"testing"

	"github.com/wnddd839/codebuddy-proxy/internal/provider"
)

func TestFinishReasonPreservesUpstreamTermination(t *testing.T) {
	for _, test := range []struct {
		name, stop, want string
		tools            bool
	}{
		{"normal", "stop", "stop", false},
		{"legacy", "", "stop", false},
		{"length", "length", "length", false},
		{"reasoning_only", "max_tokens", "length", false},
		{"filter", "content_filter", "content_filter", false},
		{"tool", "tool_use", "tool_calls", true},
		{"limited_tool", "length", "length", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			turn := provider.Turn{StopReason: test.stop}
			if test.tools {
				turn.ToolUses = []provider.ToolUse{{ID: "c1", Name: "tool", Input: map[string]any{}}}
			}
			got := FromTurn(turn, "test", "auto")
			if got.Choices[0].FinishReason == nil || *got.Choices[0].FinishReason != test.want {
				t.Fatalf("finish=%v，期望 %s", got.Choices[0].FinishReason, test.want)
			}
		})
	}
}

func TestResponsesIncompleteTerminalStatus(t *testing.T) {
	for _, test := range []struct {
		stop, status, event, reason string
	}{
		{"stop", "completed", "response.completed", ""},
		{"length", "incomplete", "response.incomplete", "max_output_tokens"},
		{"max_tokens", "incomplete", "response.incomplete", "max_output_tokens"},
		{"content_filter", "incomplete", "response.incomplete", "content_filter"},
	} {
		t.Run(test.stop, func(t *testing.T) {
			turn := provider.Turn{
				Text: "partial", StopReason: test.stop,
				Usage: provider.Usage{PromptTokens: 10, CompletionTokens: 8, TotalTokens: 18},
			}
			req := &ResponsesRequest{Model: "auto", MaxOutputTokens: new(8)}
			builder := NewResponseStreamBuilder("r1", "auto", req)
			builder.MessageStart()
			builder.MessageDelta(turn.Text)
			events := builder.Completed(turn)
			last := events[len(events)-1]
			if last.Type != test.event || last.Response.Status != test.status {
				t.Fatalf("终止事件不一致: %s / %s", last.Type, last.Response.Status)
			}
			if last.Response.Usage.OutputTokens != 8 || last.Response.MaxOutputTokens != 8 {
				t.Fatal("截断后丢失用量或请求上限")
			}
			for _, event := range events {
				if event.Item != nil && event.Item.Type == "message" && event.Item.Status != test.status {
					t.Fatalf("流式消息状态与终止状态不一致: %s", event.Item.Status)
				}
			}
			if test.reason != "" {
				details, ok := last.Response.IncompleteDetails.(map[string]any)
				if !ok || details["reason"] != test.reason {
					t.Fatalf("错误的截断原因: %#v", last.Response.IncompleteDetails)
				}
				if last.Response.Output[0].Status != "incomplete" {
					t.Fatal("最终消息被错误标记为 completed")
				}
			} else if last.Response.IncompleteDetails != nil {
				t.Fatal("正常完成不应携带截断原因")
			}
		})
	}
}
