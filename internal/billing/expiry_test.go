package billing

import (
	"testing"
	"time"
)

func TestPackageExpiryUsesBeijingTime(t *testing.T) {
	credits := summarizeResourceAccounts([]any{map[string]any{
		"PackageName": "测试套餐", "CapacityType": 1,
		"CapacityRemain": 123.5, "CapacitySize": 500, "CycleEndTime": "2026-10-16 00:33:58",
	}})
	want := time.Date(2026, 10, 15, 16, 33, 58, 0, time.UTC).UnixMilli()
	if len(credits.Packages) != 1 || credits.Packages[0].ExpiresAt != want {
		t.Fatalf("无时区上游时间未按北京时间解析: %#v", credits.Packages)
	}
	if ParseBillingTimeMillis("not-a-time") != 0 {
		t.Fatal("无效时间不应参与到期计算")
	}
}
