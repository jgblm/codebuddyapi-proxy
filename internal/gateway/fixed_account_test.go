package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/wnddd839/codebuddy-proxy/internal/accounts"
	"github.com/wnddd839/codebuddy-proxy/internal/config"
)

func TestFixedAccountNeverRotates(t *testing.T) {
	svc := New(config.Config{Site: "domestic", AccountsPath: filepath.Join(t.TempDir(), "accounts.json")},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = svc.Close() })
	a, _, err := svc.Pool.Upsert(accounts.CreateAccount(accounts.Account{Site: "domestic", BearerToken: "a", Enabled: true}))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = svc.Pool.Upsert(accounts.CreateAccount(accounts.Account{Site: "domestic", BearerToken: "b", Enabled: true}))
	if err != nil {
		t.Fatal(err)
	}
	transport := &scriptedChatTransport{byAuth: map[string]int{"a": http.StatusTooManyRequests}}
	svc.Provider.HTTP = &http.Client{Transport: transport}
	_, err = svc.CompleteFromPool(t.Context(), CompleteOptions{
		AccountID: a.ID, FixedAccount: true, Model: "auto", Messages: []map[string]any{{"role": "user", "content": "hi"}},
	})
	if err == nil {
		t.Fatal("429 不应切换到其他账号后成功")
	}
	calls, _ := transport.snapshot()
	if len(calls) != 1 || calls[0] != "a" {
		t.Fatalf("固定账号发生换号: %v", calls)
	}
	_, err = svc.ImageFromPool(t.Context(), ImageOptions{
		AccountID: a.ID, FixedAccount: true, Model: "image", Prompt: "test",
	})
	calls, _ = transport.snapshot()
	if err == nil || len(calls) != 2 || calls[1] != "a" {
		t.Fatalf("出图请求未遵守固定账号约束: %v", calls)
	}
	if _, err := svc.Pool.Delete(a.ID); err != nil {
		t.Fatal(err)
	}
	_, err = svc.CompleteFromPool(t.Context(), CompleteOptions{AccountID: a.ID, FixedAccount: true})
	if err == nil {
		t.Fatal("删除的固定账号不应清钉重选")
	}
}
