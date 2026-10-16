package admin

import (
	"os/exec"
	"strings"
	"testing"
)

func TestAdminScriptSyntax(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("可选的管理台脚本测试需要 Node.js")
	}
	_, script, ok := strings.Cut(PageHTML(), "<script>")
	if !ok {
		t.Fatal("管理台脚本缺失")
	}
	script, _, ok = strings.Cut(script, "</script>")
	if !ok {
		t.Fatal("管理台脚本未闭合")
	}
	cmd := exec.CommandContext(t.Context(), node, "--check")
	cmd.Stdin = strings.NewReader(script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("管理台脚本语法错误: %v\n%s", err, output)
	}
}

func TestAccountSelectorBelongsToKeyPanel(t *testing.T) {
	_, keyPanel, ok := strings.Cut(PageHTML(), `<select id="boundKeySite"`)
	if !ok {
		t.Fatal("Key 创建区域缺失")
	}
	panelEnd := strings.Index(keyPanel, `id="btnNewBoundKey"`)
	selector := strings.Index(keyPanel, `id="boundKeyAccount"`)
	if selector < 0 || panelEnd < 0 || selector > panelEnd {
		t.Fatal("固定账号选择器必须位于 Key 创建区域内")
	}
}

func TestExpiryViewBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("可选的管理台脚本测试需要 Node.js")
	}
	page := PageHTML()
	start := strings.Index(page, "function packageExpiryView(")
	if start < 0 {
		t.Fatal("套餐明细脚本缺失")
	}
	end := strings.Index(page[start:], "\nfunction renderAccounts(")
	if end < 0 {
		t.Fatal("套餐明细脚本缺失")
	}
	// 直接执行页面内的实际函数，防止测试副本与界面实现分叉。
	script := `const assert = require('node:assert/strict');
const escapeHtml = s => String(s).replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('>','&gt;').replaceAll('"','&quot;');
` + page[start:start+end] + `
const now = Date.UTC(2026,9,16), day = 86400000;
const packages = [
 {packageName:'later', remaining:8, total:10, expiresAt:now+8*day},
 {packageName:'<img src=x>', remaining:3, total:5, expiresAt:now+day},
 {packageName:'expired', remaining:90, total:100, expiresAt:now-1},
 {packageName:'unknown', remaining:null, total:null},
 {packageName:'soon', remaining:5, total:5, expiresAt:now+7*day},
 {packageName:'unlimited', remaining:-1, total:-1, expiresAt:now+day}
];
const before = JSON.stringify(packages);
const html = packageExpiryView({credits:{packages},quota:{checkedAt:now}},now);
assert.match(html,/2 包 \/ 8.00 分/);
assert.match(html,/已过期/);
assert.match(html,/到期未知/);
assert.match(html,/不限量/);
assert.ok(!html.includes('<img src=x>'));
assert.ok(html.includes('&lt;img src=x&gt;'));
assert.ok(html.indexOf('expired') < html.indexOf('later'));
assert.equal(JSON.stringify(packages),before);
assert.match(packageExpiryView({credits:{packages:[]}},now),/暂无套餐明细/);
`
	cmd := exec.CommandContext(t.Context(), node, "-e", script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("套餐明细脚本测试失败: %v\n%s", err, output)
	}
}
