package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wnddd839/codebuddy-proxy/internal/accounts"
	"github.com/wnddd839/codebuddy-proxy/internal/config"
)

func TestFixedKeyAcrossChatAndResponses(t *testing.T) {
	srv := testServerCfg(t, config.Config{RequireAPIKey: true, APIKey: "main", Site: "global"})
	account, _, err := srv.Svc.Pool.Upsert(accounts.CreateAccount(accounts.Account{
		Site: "domestic", Label: "fixed", BearerToken: "token-fixed", Enabled: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	srv.Svc.SetAPIKeys([]config.APIKeyBinding{{Key: "fixed", AccountID: account.ID}})
	transport := &recordingUpstreamTransport{}
	srv.Svc.Provider.HTTP = &http.Client{Transport: transport}
	for _, test := range []struct {
		path, body string
		want       int
	}{
		{"/v1/chat/completions", `{"model":"auto","messages":[{"role":"user","content":"hi"}]}`, 200},
		{"/v1/responses", `{"model":"auto","input":"hi"}`, 200},
		{"/v1/responses", `{"model":"global:auto","input":"hi"}`, 400},
		{"/v1/chat/completions", `{"model":"global:auto","messages":[{"role":"user","content":"hi"}]}`, 400},
		{"/v1/images/generations", `{"model":"global:auto","prompt":"hi"}`, 400},
	} {
		req := httptest.NewRequest("POST", test.path, strings.NewReader(test.body))
		req.Header.Set("Authorization", "Bearer fixed")
		rec := httptest.NewRecorder()
		srv.HTTP.Handler.ServeHTTP(rec, req)
		if rec.Code != test.want {
			t.Fatalf("%s status=%d: %s", test.path, rec.Code, rec.Body.String())
		}
	}
	for _, token := range transport.chatTokens() {
		if token != "token-fixed" {
			t.Fatal("请求使用了其他账号")
		}
	}
	if _, _, err := srv.Svc.Pool.SetEnabled(account.ID, false); err != nil {
		t.Fatal(err)
	}
	disabled := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"auto","input":"hi"}`))
	disabled.Header.Set("Authorization", "Bearer fixed")
	disabledResponse := httptest.NewRecorder()
	srv.HTTP.Handler.ServeHTTP(disabledResponse, disabled)
	if disabledResponse.Code != http.StatusServiceUnavailable {
		t.Fatal("禁用账号未明确失败")
	}
	if _, err := srv.Svc.Pool.Delete(account.ID); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer fixed")
	rec := httptest.NewRecorder()
	srv.HTTP.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("账号删除后未明确失败: %d", rec.Code)
	}
}

func TestCreateAccountBindingPersistsAndReloads(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), "proxy.env")
	t.Setenv("CODEBUDDY_PROXY_ENV_FILE", envPath)
	srv := testServer(t, true, "", "main")
	account, _, err := srv.Svc.Pool.Upsert(accounts.CreateAccount(accounts.Account{
		Site: "domestic", BearerToken: "fixture", Enabled: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"accountId": account.ID})
	req := httptest.NewRequest("POST", "/direct-admin/api/client-config/bound-keys", strings.NewReader(string(raw)))
	rec := httptest.NewRecorder()
	srv.HTTP.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("创建失败: %d", rec.Code)
	}
	keys := srv.Svc.Config().APIKeys
	if len(keys) != 1 || keys[0].AccountID != account.ID {
		t.Fatal("运行时绑定丢失")
	}
	env, err := os.ReadFile(envPath)
	if err != nil || !strings.Contains(string(env), ":account="+account.ID) {
		t.Fatal("绑定未落盘")
	}
	reloaded := config.ParseAPIKeys(config.FormatAPIKeys(keys))
	if len(reloaded) != 1 || reloaded[0].AccountID != account.ID {
		t.Fatal("重启后的绑定约束丢失")
	}
	// 相同账号显式指定冲突区域时，不应创建第二把 Key。
	raw, _ = json.Marshal(map[string]any{"accountId": account.ID, "site": "global"})
	req = httptest.NewRequest("POST", "/direct-admin/api/client-config/bound-keys", strings.NewReader(string(raw)))
	rec = httptest.NewRecorder()
	srv.HTTP.Handler.ServeHTTP(rec, req)
	if rec.Code != 400 || len(srv.Svc.Config().APIKeys) != 1 {
		t.Fatal("区域冲突未拒绝或产生了配置副作用")
	}
}
