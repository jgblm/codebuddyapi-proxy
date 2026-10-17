package provider

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAgenticSearchDeltaEOFAndFinalOverride(t *testing.T) {
	for _, test := range []struct {
		name, tail, want string
	}{
		{"delta EOF", "", "hello world"},
		{"final replaces deltas", "event: done\ndata: {\"synthesis\":{\"content\":\"final\"}}\n\n", "final"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := "event: synthesis_delta\ndata: {\"delta\":\"hello \"}\n\n" +
				"event: synthesis_delta\ndata: {\"delta\":\"world\"}\n\n" + test.tail
			client := &Client{HTTP: &http.Client{Transport: &searchTransport{
				reply: func(_, _ string) *http.Response {
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
				},
			}}}
			result, err := client.AgenticSearch(t.Context(), ChatOptions{Site: "domestic"}, "research", nil)
			if err != nil || result.Content != test.want {
				t.Fatalf("content=%q err=%v", result.Content, err)
			}
			for _, event := range result.Events {
				raw, err := json.Marshal(event)
				if err != nil || strings.Contains(string(raw), "<nil>") || strings.Contains(string(raw), `\u003cnil\u003e`) {
					t.Fatalf("missing field became a literal value: %s %v", raw, err)
				}
			}
		})
	}
}
