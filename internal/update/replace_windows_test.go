package update

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// shortWaits 把退避表缩短到毫秒级，保证测试快速且不依赖真实杀软时序。
func shortWaits(t *testing.T, waits ...time.Duration) {
	t.Helper()
	if len(waits) == 0 {
		waits = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	}
	old := replaceWaits
	replaceWaits = waits
	t.Cleanup(func() { replaceWaits = old })
}

// lockFile 以不允许删除/重命名的共享模式打开文件，模拟 Windows 上的占用。
func lockFile(t *testing.T, path string) syscall.Handle {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ, syscall.FILE_SHARE_READ, nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestWindowsReplacementSharingFailureRestoresOld(t *testing.T) {
	shortWaits(t)
	d := t.TempDir()
	exe, stage := filepath.Join(d, "exe"), filepath.Join(d, "stage")
	os.WriteFile(exe, []byte("original"), 0700)
	os.WriteFile(stage, []byte("new"), 0700)
	h := lockFile(t, stage)
	defer syscall.CloseHandle(h)
	backup, err := reserve(d, "backup")
	if err != nil {
		t.Fatal(err)
	}
	if err := replace(stage, exe, backup); err == nil {
		t.Fatal("rename of delete-locked stage should fail")
	}
	b, err := os.ReadFile(exe)
	if err != nil || string(b) != "original" {
		t.Fatalf("original was not restored: %s %v", b, err)
	}
}

// TestWindowsReplacementRetriesUntilReleased 验证占用在退避窗口内释放后 replace 仍能成功。
func TestWindowsReplacementRetriesUntilReleased(t *testing.T) {
	// 放大重试窗口，覆盖锁在若干次尝试之后才释放的情况。
	shortWaits(t, make([]time.Duration, 40)...)
	for i := range replaceWaits {
		replaceWaits[i] = 5 * time.Millisecond
	}
	d := t.TempDir()
	exe, stage := filepath.Join(d, "exe"), filepath.Join(d, "stage")
	os.WriteFile(exe, []byte("original"), 0700)
	os.WriteFile(stage, []byte("new"), 0700)
	h := lockFile(t, stage)
	released := make(chan struct{})
	// 稍后释放锁；replace 应在有限重试中等到释放并完成替换。
	go func() {
		time.Sleep(5 * time.Millisecond)
		syscall.CloseHandle(h)
		close(released)
	}()
	defer func() { <-released }()
	backup, err := reserve(d, "backup")
	if err != nil {
		t.Fatal(err)
	}
	if err := replace(stage, exe, backup); err != nil {
		t.Fatalf("replace after lock release: %v", err)
	}
	if b, err := os.ReadFile(exe); err != nil || string(b) != "new" {
		t.Fatalf("new binary not installed: %s %v", b, err)
	}
	if b, err := os.ReadFile(backup); err != nil || string(b) != "original" {
		t.Fatalf("backup missing original: %s %v", b, err)
	}
}
