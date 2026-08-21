package orchestration

import (
	"context"
	"errors"
	"testing"

	"github.com/zouyi/eco-guardian/internal/simulation/contract"
)

type admissionRevisionSource struct {
	revision contract.Revision
	calls    int
}

func (f *admissionRevisionSource) ResolveRevision(context.Context, contract.ID) (contract.Revision, error) {
	f.calls++
	return f.revision, nil
}

type admissionGate struct {
	err   error
	calls int
}

func (f *admissionGate) RequireFull(context.Context, contract.ID) error { f.calls++; return f.err }

func TestAdmitRejectsBeforeJobCreationWhenFullValidationFails(t *testing.T) {
	revisions := &admissionRevisionSource{revision: contract.Revision{ID: "revision", ProjectID: "project", ConfigHash: "config", ManifestHash: "manifest"}}
	gate := &admissionGate{err: errors.New("blocked")}
	if _, err := Admit(context.Background(), revisions, nil, gate, contract.SourceSelection{ProjectID: "project", RevisionID: "revision"}); !errors.Is(err, ErrFullValidationRequired) {
		t.Fatalf("err=%v", err)
	}
	if revisions.calls != 1 || gate.calls != 1 {
		t.Fatalf("revision calls=%d gate calls=%d", revisions.calls, gate.calls)
	}
}

func TestAdmitReturnsPinnedRevisionOnlyAfterGatePasses(t *testing.T) {
	revision := contract.Revision{ID: "revision", ProjectID: "project", ConfigHash: "config", ManifestHash: "manifest"}
	result, err := Admit(context.Background(), &admissionRevisionSource{revision: revision}, nil, &admissionGate{}, contract.SourceSelection{ProjectID: "project", RevisionID: "revision"})
	if err != nil || result != revision {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}
