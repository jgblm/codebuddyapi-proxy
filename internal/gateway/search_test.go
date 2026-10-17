package gateway

import (
	"errors"
	"net/http"
	"testing"

	"github.com/wnddd839/codebuddy-proxy/internal/accounts"
	"github.com/wnddd839/codebuddy-proxy/internal/provider"
)

func TestSearchRetryExhaustionPreservesUpstreamError(t *testing.T) {
	svc := newImageService(t, &scriptedImageTransport{byAuth: map[string]int{"token-a": http.StatusTooManyRequests}})
	account, _, err := svc.Pool.Upsert(accounts.CreateAccount(accounts.Account{
		Site: "domestic", BearerToken: "token-a", Enabled: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = svc.WebSearchFromPool(t.Context(), SearchOptions{AccountID: account.ID},
		provider.WebSearchRequest{Query: "Go"})
	upstream, ok := errors.AsType[*provider.ChatError](err)
	if !ok || upstream.Status != http.StatusTooManyRequests {
		t.Fatalf("retry exhausted masked upstream 429: %v", err)
	}
}
