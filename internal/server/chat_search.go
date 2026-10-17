package server

import (
	"context"

	"github.com/wnddd839/codebuddy-proxy/internal/gateway"
)

// completeWithRequestedSearch never enables search by default. A chat or
// Responses handler must pass an explicit client capability declaration.
func (s *Server) completeWithRequestedSearch(
	ctx context.Context,
	opts gateway.CompleteOptions,
	requested bool,
) (gateway.CompleteResult, error) {
	if !requested {
		return s.Svc.CompleteFromPool(ctx, opts)
	}
	searchCtx, cancel := context.WithTimeout(ctx, responsesSearchOverallTimeout)
	defer cancel()
	_, result, err := s.prepareBuiltinSearch(searchCtx, opts, opts.Site)
	if err != nil {
		return gateway.CompleteResult{}, err
	}
	return *result, nil
}
