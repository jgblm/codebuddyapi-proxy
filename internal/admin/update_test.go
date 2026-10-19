package admin

import (
	"os"
	"strings"
	"testing"
)

func TestManualUpdateUI(t *testing.T) {
	b, err := os.ReadFile("page.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, text := range []string{"检查更新", "更新并重启", "updateCheck", "updateInstall", "/direct-admin/api/update/check", "/direct-admin/api/update/install", "等待服务重新连接"} {
		if !strings.Contains(s, text) {
			t.Errorf("missing UI behavior %q", text)
		}
	}
	if strings.Contains(s, "setInterval(checkUpdate") || strings.Contains(s, "await checkUpdate();") {
		t.Fatal("automatic release check")
	}
}

// TestUpdateBarOutsideHeader 确保更新按钮位于 header 之后独立一行，不挤占 pillrow。
func TestUpdateBarOutsideHeader(t *testing.T) {
	b, err := os.ReadFile("page.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `class="updatebar"`) {
		t.Fatal("missing updatebar container")
	}
	headerEnd := strings.Index(s, "</header>")
	if headerEnd < 0 {
		t.Fatal("missing header")
	}
	before := s[:headerEnd]
	for _, id := range []string{`id="updateCheck"`, `id="updateInstall"`, `id="updateCancel"`, `id="updateMessage"`} {
		if strings.Contains(before, id) {
			t.Errorf("%s still inside header", id)
		}
	}
	bar := strings.Index(s, `class="updatebar"`)
	nav := strings.Index(s, `<nav class="tabs-nav"`)
	if bar < headerEnd || nav < bar {
		t.Fatalf("updatebar not between header and nav: header=%d bar=%d nav=%d", headerEnd, bar, nav)
	}
}

// TestUpdatePollingTimeout 防止重启期间的状态轮询无限挂起。
func TestUpdatePollingTimeout(t *testing.T) {
	b, err := os.ReadFile("page.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "AbortController") {
		t.Fatal("update polling has no timeout")
	}
	if !strings.Contains(s, "updateFetch('/direct-admin/api/update/install'") {
		t.Fatal("install does not use the timeout-aware fetch")
	}
	if !strings.Contains(s, "updateFetch('/direct-admin/api/update')") {
		t.Fatal("status polling does not use the timeout-aware fetch")
	}
}
