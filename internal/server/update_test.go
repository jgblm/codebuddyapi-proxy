package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUpdateManagementAuthorization(t *testing.T) {
	for _, path := range []string{"update", "update/check", "update/install", "update/cancel"} {
		t.Run(path, func(t *testing.T) {
			s := testServer(t, false, "synthetic-admin", "")
			method := "POST"
			if path == "update" {
				method = "GET"
			}
			run := func(auth, foreign bool) int {
				r := httptest.NewRequest(method, "http://localhost/direct-admin/api/"+path, nil)
				if auth {
					r.SetBasicAuth("admin", "synthetic-admin")
				}
				if foreign {
					r.Header.Set("Origin", "https://evil.invalid")
				}
				w := httptest.NewRecorder()
				s.HTTP.Handler.ServeHTTP(w, r)
				return w.Code
			}
			if code := run(false, false); code != 401 {
				t.Fatalf("anonymous %d", code)
			}
			if method == "POST" && run(true, true) != 403 {
				t.Fatal("cross origin mutation accepted")
			}
			if code := run(true, false); code == 404 || code == 401 {
				t.Fatalf("missing authorized route %d", code)
			}
		})
	}
}

func TestUpdateOpenAdminAndNoClientInput(t *testing.T) {
	s := testServer(t, false, "", "")
	r := httptest.NewRequest("GET", "/direct-admin/api/update", nil)
	w := httptest.NewRecorder()
	s.HTTP.Handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("open admin status %d", w.Code)
	}
	r = httptest.NewRequest("POST", "/direct-admin/api/update/install", strings.NewReader(`{"url":"http://evil.invalid","path":"other","command":"evil"}`))
	w = httptest.NewRecorder()
	s.HTTP.Handler.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatalf("client input must be rejected: %d", w.Code)
	}
}
