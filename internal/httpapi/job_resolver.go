package httpapi

import (
	"context"

	"github.com/zouyi/eco-guardian/internal/domain"
)

// DurableResolver is the capability-neutral HTTP boundary for the shared Job
// resource. Each capability supplies a projection instead of extending the
// handler with another domain-specific route branch.
type DurableResolver interface {
	GetJob(context.Context, domain.ID) (map[string]any, bool, error)
	CancelJob(context.Context, domain.ID) (map[string]any, bool, bool, error)
}

// DurableResolverFunc keeps composition explicit and makes resolver registration
// easy to fake in HTTP contract tests.
type DurableResolverFunc struct {
	Get    func(context.Context, domain.ID) (map[string]any, bool, error)
	Cancel func(context.Context, domain.ID) (map[string]any, bool, bool, error)
}

func (r DurableResolverFunc) GetJob(ctx context.Context, id domain.ID) (map[string]any, bool, error) {
	if r.Get == nil {
		return nil, false, nil
	}
	return r.Get(ctx, id)
}

func (r DurableResolverFunc) CancelJob(ctx context.Context, id domain.ID) (map[string]any, bool, bool, error) {
	if r.Cancel == nil {
		return nil, false, false, nil
	}
	return r.Cancel(ctx, id)
}
