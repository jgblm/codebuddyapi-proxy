package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/wnddd839/codebuddy-proxy/internal/accounts"
	"github.com/wnddd839/codebuddy-proxy/internal/config"
	"github.com/wnddd839/codebuddy-proxy/internal/httputil"
	"github.com/wnddd839/codebuddy-proxy/internal/openai"
)

type accountBindingKey struct{}

func requestBinding(r *http.Request) config.APIKeyBinding {
	binding, _ := r.Context().Value(accountBindingKey{}).(config.APIKeyBinding)
	return binding
}

// bindAccount 校验账号状态并推导区域，不进行选号或上游请求。
func (s *Server) bindAccount(r *http.Request, binding config.APIKeyBinding) (config.APIKeyBinding, error) {
	store, err := s.Svc.Pool.Read()
	if err != nil {
		return binding, err
	}
	for _, account := range store.Accounts {
		if account.ID != binding.AccountID {
			continue
		}
		if !account.Enabled || !accounts.HasCredentials(account) {
			return binding, errors.New("绑定账号不可用，请联系管理员")
		}
		binding.Site = config.NormalizeSite(account.Site)
		*r = *r.WithContext(context.WithValue(r.Context(), accountBindingKey{}, binding))
		return binding, nil
	}
	return binding, errors.New("绑定账号不存在，请联系管理员")
}

// boundSiteAllowed 拒绝模型前缀或请求头试图覆盖固定账号区域的请求。
func boundSiteAllowed(w http.ResponseWriter, r *http.Request, site string) bool {
	binding := requestBinding(r)
	if binding.AccountID != "" && config.NormalizeSite(site) != binding.Site {
		httputil.WriteJSON(w, http.StatusBadRequest, openai.NewError("请求区域与绑定账号不一致", "invalid_request_error"))
		return false
	}
	return true
}
