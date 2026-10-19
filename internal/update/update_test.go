package update

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestVersionsAndTargets(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v0.6", "v0.5.5.1", true}, {"v0.5.5.2", "0.5.5.1", true},
		{"v0.6.0", "v0.6", false}, {"v0.6", "v0.6.0.1", false},
		{"dev", "v0.5", false}, {"v1.2-beta", "v1.1", false},
	} {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("%s > %s = %v", c.a, c.b, got)
		}
	}
	for _, c := range []struct{ os, arch, arm, want string }{
		{"windows", "amd64", "", "codebuddy-proxy-windows-x64.exe"},
		{"linux", "amd64", "", "codebuddy-proxy-linux-amd64"},
		{"linux", "arm64", "", "codebuddy-proxy-linux-arm64"},
		{"linux", "arm", "6", "codebuddy-proxy-linux-armv6"},
		{"linux", "arm", "7,hardfloat", "codebuddy-proxy-linux-armv7"},
		{"darwin", "amd64", "", "codebuddy-proxy-darwin-amd64"},
		{"darwin", "arm64", "", "codebuddy-proxy-darwin-arm64"},
	} {
		got, err := assetName(c.os, c.arch, c.arm)
		if err != nil || got != c.want {
			t.Errorf("%+v: %s %v", c, got, err)
		}
	}
	for _, c := range [][3]string{{"linux", "arm", ""}, {"linux", "arm", "5"}, {"windows", "arm64", ""}} {
		if _, err := assetName(c[0], c[1], c[2]); err == nil {
			t.Errorf("accepted unsupported %v", c)
		}
	}
}

func fixture(t *testing.T, checksum string) (*Manager, *atomic.Int32) {
	t.Helper()
	data := []byte("synthetic executable")
	name := "codebuddy-proxy-windows-x64.exe"
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch {
		case r.Method == http.MethodHead && strings.HasSuffix(r.URL.Path, "/latest"):
			http.Redirect(w, r, releaseTagBase+"v0.6", http.StatusFound)
		case r.Method == http.MethodHead && strings.Contains(r.URL.Path, "/releases/tag/"):
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/latest"):
			json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.6", "assets": []map[string]any{{"name": name, "size": len(data)}, {"name": "SHA256SUMS.txt", "size": 200}}})
		case strings.HasSuffix(r.URL.Path, "SHA256SUMS.txt"):
			if checksum == "good" {
				fmt.Fprintf(w, "%x  %s\n", sha256.Sum256(data), name)
			} else {
				fmt.Fprint(w, checksum)
			}
		case strings.HasSuffix(r.URL.Path, name):
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			w.Write(data)
		default:
			t.Errorf("unexpected request %s", r.URL)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(ts.Close)
	m := New("v0.5.5.1")
	m.exe = filepath.Join(t.TempDir(), "proxy.exe")
	if err := os.WriteFile(m.exe, []byte("original"), 0700); err != nil {
		t.Fatal(err)
	}
	m.asset = name
	m.state.Message = ""
	m.supported = true
	// 测试只替换传输，生产 URL 始终固定，不增加可配置下载源。
	m.client = &http.Client{Timeout: time.Second, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.github.com" && r.URL.Host != "github.com" {
			t.Fatalf("unofficial host %s", r.URL)
		}
		c := r.Clone(r.Context())
		c.URL.Scheme = "http"
		c.URL.Host = strings.TrimPrefix(ts.URL, "http://")
		resp, err := http.DefaultTransport.RoundTrip(c)
		if resp != nil {
			resp.Request = r
		}
		return resp, err
	})}
	return m, &calls
}

func TestManualOnlyAndChecksum(t *testing.T) {
	m, calls := fixture(t, "good")
	for range 3 {
		m.Status()
	}
	if calls.Load() != 0 {
		t.Fatal("unsolicited network")
	}
	s, err := m.Check(t.Context())
	if err != nil || !s.Available || s.Latest != "v0.6" {
		t.Fatalf("%+v %v", s, err)
	}
	path, err := m.stage(t.Context(), m.release)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	if filepath.Dir(path) != filepath.Dir(m.exe) {
		t.Fatal("stage outside install directory")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "synthetic executable" {
		t.Fatal("download incorrect")
	}
	old, _ := os.ReadFile(m.exe)
	if string(old) != "original" {
		t.Fatal("download changed current binary")
	}
}

func TestBadChecksumAndCancellationCleanStage(t *testing.T) {
	for _, sum := range []string{"", strings.Repeat("0", 64) + "  codebuddy-proxy-windows-x64.exe\n"} {
		t.Run(fmt.Sprintf("sum%d", len(sum)), func(t *testing.T) {
			m, _ := fixture(t, sum)
			if _, err := m.Check(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := m.stage(t.Context(), m.release); err == nil {
				t.Fatal("accepted missing/wrong checksum")
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := m.stage(ctx, m.release); err == nil {
				t.Fatal("accepted cancellation")
			}
			files, _ := os.ReadDir(filepath.Dir(m.exe))
			if len(files) != 1 {
				t.Fatalf("left artifacts: %v", files)
			}
		})
	}
}

func TestBusyAndUnsupported(t *testing.T) {
	m, calls := fixture(t, "good")
	m.busy = true
	if _, err := m.Check(t.Context()); err == nil {
		t.Fatal("parallel check accepted")
	}
	if err := m.Start(); err == nil {
		t.Fatal("parallel install accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("busy request made network call")
	}
	dev := New("dev")
	if _, err := dev.Check(t.Context()); err == nil {
		t.Fatal("dev source build accepted")
	}
}

func TestReplacementFailurePreservesOriginal(t *testing.T) {
	d := t.TempDir()
	exe := filepath.Join(d, "proxy")
	os.WriteFile(exe, []byte("original"), 0700)
	backup, err := reserve(d, "backup")
	if err != nil {
		t.Fatal(err)
	}
	if err := replace(filepath.Join(d, "missing"), exe, backup); err == nil {
		t.Fatal("missing staged file accepted")
	}
	b, err := os.ReadFile(exe)
	if err != nil || string(b) != "original" {
		t.Fatalf("lost original: %s %v", b, err)
	}
}

func TestReleaseAndResponseBoundaries(t *testing.T) {
	for _, body := range []string{
		`{"tag_name":"v0.7","draft":true}`,
		`{"tag_name":"v0.7","prerelease":true}`,
		`{"tag_name":"v0.7-beta"}`,
		`{"tag_name":"../../evil"}`,
		`{"tag_name":"v0.7","assets":[{"name":"other.exe","size":1}]}`,
	} {
		m, _ := fixture(t, "good")
		m.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() == latestWebURL {
				return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(""))}, nil
			}
			if r.URL.String() != latestURL {
				t.Errorf("unexpected feed %s", r.URL)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		if _, err := m.Check(t.Context()); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
	m := New("v0.6")
	for _, u := range []string{"http://github.com/file", "https://evil.invalid/file", "https://github.com:8443/file", "https://u:p@github.com/file"} {
		req, _ := http.NewRequest("GET", u, nil)
		if err := m.client.CheckRedirect(req, nil); err == nil {
			t.Errorf("accepted redirect %s", u)
		}
	}
	m.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("oversize"))}, nil
	})}
	if _, err := m.get(t.Context(), latestURL, 3); err == nil {
		t.Fatal("accepted oversized body")
	}
}

func TestRealConcurrentInstallCancellation(t *testing.T) {
	m, _ := fixture(t, "good")
	if _, err := m.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	m.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(entered)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := m.Start(); err == nil {
		t.Fatal("second active install")
	}
	if _, err := m.Check(t.Context()); err == nil {
		t.Fatal("check during install")
	}
	if err := m.Cancel(); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		if m.Status().Phase == "error" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if m.Status().Phase != "error" {
		t.Fatal("cancellation did not settle")
	}
	select {
	case <-m.Requests():
		t.Fatal("cancelled install requested restart")
	default:
	}
	files, _ := os.ReadDir(filepath.Dir(m.exe))
	if len(files) != 1 {
		t.Fatalf("cancel left artifacts %v", files)
	}
}

func TestDirectoryNotWritable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows modes do not enforce directory ACLs; Unix permission test is native-only")
	}
	d := t.TempDir()
	if err := os.Chmod(d, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(d, 0700)
	if f, err := os.CreateTemp(d, "probe"); err == nil {
		f.Close()
		os.Remove(f.Name())
		t.Skip("privileged user bypasses directory mode")
	}
	m, _ := fixture(t, "good")
	m.exe = filepath.Join(d, "proxy")
	if _, err := m.stage(t.Context(), release{}); err == nil || !strings.Contains(err.Error(), "不可写") {
		t.Fatalf("unwritable directory %v", err)
	}
}

func TestExistingBackupNeverOverwritten(t *testing.T) {
	d := t.TempDir()
	exe, stage, backup := filepath.Join(d, "exe"), filepath.Join(d, "stage"), filepath.Join(d, "valuable-backup")
	for p, b := range map[string]string{exe: "old", stage: "new", backup: "valuable"} {
		if err := os.WriteFile(p, []byte(b), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := replace(stage, exe, backup); err == nil {
		t.Fatal("overwrote existing backup")
	}
	b, _ := os.ReadFile(backup)
	old, _ := os.ReadFile(exe)
	if string(b) != "valuable" || string(old) != "old" {
		t.Fatal("changed valuable files")
	}
}
