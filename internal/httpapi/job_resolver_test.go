package httpapi

import (
	"context"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
)

func TestDurableResolverFuncWithoutCallbacksDoesNotClaimJob(t *testing.T) {
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	resolver := DurableResolverFunc{}
	if value, found, err := resolver.GetJob(context.Background(), id); err != nil || found || value != nil {
		t.Fatalf("value=%v found=%v err=%v", value, found, err)
	}
}
