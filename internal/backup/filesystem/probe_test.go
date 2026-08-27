package filesystem

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestProbeUsesBoundedSafeDiagnostics(t *testing.T) {
	root := filepath.Clean(t.TempDir())
	probe := Probe{}
	if err := probe.RequireWritable(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if err := probe.RequireAvailable(context.Background(), root, 1); err != nil {
		t.Fatal(err)
	}
	if err := probe.RequireAvailable(context.Background(), root, int64(^uint64(0)>>1)); !errors.Is(err, ErrSpaceInsufficient) {
		t.Fatalf("large probe err=%v", err)
	}
}
