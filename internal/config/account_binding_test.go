package config_test

import (
	"testing"

	"github.com/wnddd839/codebuddy-proxy/internal/config"
)

func TestAccountKeyBindingRoundTrip(t *testing.T) {
	raw := "region:domestic,fixed:account=account-123,plain"
	bindings := config.ParseAPIKeys(raw)
	if len(bindings) != 3 || bindings[1].AccountID != "account-123" || bindings[1].Site != "" {
		t.Fatalf("账号后缀被误解释为区域: %#v", bindings)
	}
	if got := config.FormatAPIKeys(bindings); got != raw {
		t.Fatalf("绑定回写丢失: %s", got)
	}
	cfg := config.Config{APIKey: "fixed", APIKeys: bindings}
	ok, binding := cfg.LookupAPIKeyBinding("fixed")
	if !ok || binding.AccountID != "account-123" {
		t.Fatal("主 Key 的账号绑定失效")
	}
	if got := config.ParseAPIKeys("broken:account="); len(got) != 0 {
		t.Fatal("空账号约束不应成为普通区域 Key")
	}
}
