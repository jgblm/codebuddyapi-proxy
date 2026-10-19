package update

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func discoveryResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status, Body: io.NopCloser(strings.NewReader(body)),
		Header: make(http.Header), Request: req, ContentLength: int64(len(body)),
	}
}

func TestCheckUsesOfficialWebWithoutRateLimitedAPI(t *testing.T) {
	m, _ := fixture(t, "good")
	transport := m.client.Transport
	var apiCalls, webCalls int
	m.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "api.github.com" {
			apiCalls++
			resp := discoveryResponse(req, 403, `{"message":"API rate limit exceeded"}`)
			resp.Header.Set("X-RateLimit-Remaining", "0")
			return resp, nil
		}
		if req.URL.String() == latestWebURL {
			webCalls++
			if req.Method != http.MethodHead {
				t.Errorf("version discovery unexpectedly reads HTML: %s", req.Method)
			}
		}
		return transport.RoundTrip(req)
	})
	state, err := m.Check(t.Context())
	if err != nil || state.Latest != "v0.6" || !state.Available {
		t.Fatalf("web discovery failed: %+v %v", state, err)
	}
	if apiCalls != 0 || webCalls != 1 {
		t.Fatalf("unexpected discovery calls: API=%d web=%d", apiCalls, webCalls)
	}
	// 继续走真实暂存和校验，确保网页发现不只是能显示一个版本号。
	path, err := m.stage(t.Context(), m.release)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
}

func TestCheckFallsBackToAPIWhenWebUnavailable(t *testing.T) {
	m, _ := fixture(t, "good")
	transport := m.client.Transport
	var apiCalls int
	m.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() == latestWebURL {
			return discoveryResponse(req, 503, ""), nil
		}
		if req.URL.String() == latestURL {
			apiCalls++
		}
		return transport.RoundTrip(req)
	})
	state, err := m.Check(t.Context())
	if err != nil || !state.Available || apiCalls != 1 {
		t.Fatalf("API fallback failed: %+v calls=%d err=%v", state, apiCalls, err)
	}
}

func TestWebDiscoveryRejectsInvalidReleasesAndAssets(t *testing.T) {
	for _, test := range []struct {
		name, location, sums string
		size                 int64
	}{
		{"foreign_repo", "https://github.com/evil/repo/releases/tag/v0.7", "", 1},
		{"foreign_host", "https://evil.invalid/releases/tag/v0.7", "", 1},
		{"http", "http://github.com/wnddd839/buddy-proxy/releases/tag/v0.7", "", 1},
		{"prerelease", releaseTagBase + "v0.7-beta", "", 1},
		{"path", releaseTagBase + "../v0.7", "", 1},
		{"query", releaseTagBase + "v0.7?file=other", "", 1},
		{"missing_sum", releaseTagBase + "v0.7", "missing", 1},
		{"duplicate_sum", releaseTagBase + "v0.7", "duplicate", 1},
		{"zero_size", releaseTagBase + "v0.7", "good", 0},
		{"unknown_size", releaseTagBase + "v0.7", "good", -1},
		{"oversize", releaseTagBase + "v0.7", "good", maxBinary + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, _ := fixture(t, "good")
			m.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				switch {
				case req.URL.String() == latestWebURL:
					resp := discoveryResponse(req, 302, "")
					resp.Header.Set("Location", test.location)
					return resp, nil
				case strings.Contains(req.URL.Path, "/releases/tag/"):
					return discoveryResponse(req, 200, ""), nil
				case strings.HasSuffix(req.URL.Path, "SHA256SUMS.txt"):
					sum := fmt.Sprintf("%x  %s\n", sha256.Sum256([]byte("test")), m.asset)
					if test.sums == "missing" {
						sum = ""
					} else if test.sums == "duplicate" {
						sum += sum
					}
					return discoveryResponse(req, 200, sum), nil
				default:
					resp := discoveryResponse(req, 200, "")
					resp.ContentLength = test.size
					return resp, nil
				}
			})
			if _, err := m.latestWeb(t.Context()); err == nil {
				t.Fatal("accepted invalid release metadata")
			}
		})
	}
}

func TestDiscoveryExplainsSharedProxyRateLimit(t *testing.T) {
	m, _ := fixture(t, "good")
	m.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		resp := discoveryResponse(req, 403, "")
		if req.URL.String() == latestURL {
			resp.Header.Set("X-RateLimit-Remaining", "0")
			resp.Header.Set("X-RateLimit-Reset", "1792381407")
		}
		return resp, nil
	})
	_, err := m.Check(t.Context())
	if err == nil || !strings.Contains(err.Error(), "共享代理") || !strings.Contains(err.Error(), "恢复时间") {
		t.Fatalf("rate limit not explained: %v", err)
	}
	if m.Status().Available {
		t.Fatal("failed discovery allows install")
	}
}

func TestCanceledDiscoveryDoesNotCallAPI(t *testing.T) {
	m, _ := fixture(t, "good")
	m.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Errorf("canceled discovery contacted %s", req.URL)
		return nil, context.Canceled
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := m.Check(ctx)
	if err == nil {
		t.Fatal("canceled check succeeded")
	}
}
