package server

import (
	"io"
	"net/http"

	"github.com/wnddd839/codebuddy-proxy/internal/httputil"
)

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request, path string) {
	// 所有更新路由复用既有管理鉴权及同源检查，不额外要求独立管理员密码。
	if r.Method == http.MethodPost {
		b, err := io.ReadAll(io.LimitReader(r.Body, 1))
		if err != nil || len(b) != 0 || r.URL.RawQuery != "" {
			httputil.WriteJSON(w, 400, map[string]any{"error": "更新接口不接受客户端参数或请求体"})
			return
		}
	}
	var err error
	switch {
	case path == "/direct-admin/api/update" && r.Method == http.MethodGet:
	case path == "/direct-admin/api/update/check" && r.Method == http.MethodPost:
		_, err = s.Updater.Check(r.Context())
	case path == "/direct-admin/api/update/install" && r.Method == http.MethodPost:
		err = s.Updater.Start()
	case path == "/direct-admin/api/update/cancel" && r.Method == http.MethodPost:
		err = s.Updater.Cancel()
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		httputil.WriteJSON(w, 409, map[string]any{"error": err.Error(), "state": s.Updater.Status()})
		return
	}
	httputil.WriteJSON(w, 200, s.Updater.Status())
}
