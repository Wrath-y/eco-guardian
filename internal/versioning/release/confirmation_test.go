package release

import (
	"errors"
	"strings"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

func TestValidateConfirmationsRequiresExplicitBaselineAndWarningAcknowledgement(t *testing.T) {
	candidate := confirmationCandidate(t, "")
	warning := versioninggate.Assessment{State: versioninggate.Warning}
	if err := ValidateConfirmations(candidate, "", warning, nil, nil, nil); !errors.Is(err, ErrBaselineConfirmation) {
		t.Fatalf("first baseline=%v", err)
	}
	if err := ValidateConfirmations(candidate, "", warning, nil, []Confirmation{{Kind: ConfirmationEstablishBaseline, Confirmed: true}}, nil); !errors.Is(err, ErrWarningConfirmation) {
		t.Fatalf("warning=%v", err)
	}
	if err := ValidateConfirmations(candidate, "", warning, nil, []Confirmation{{Kind: ConfirmationEstablishBaseline, Confirmed: true}, {Kind: ConfirmationAcknowledgeWarning, Confirmed: true}}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestValidateConfirmationsBindsRiskBaselineAndRestrictsOverrides(t *testing.T) {
	baseline, resultID := confirmationID(t), confirmationID(t)
	candidate := confirmationCandidate(t, baseline)
	numeric := confirmationResult(resultID, candidate, true)
	blocked := versioninggate.Assessment{State: versioninggate.Block}
	if err := ValidateConfirmations(candidate, baseline, blocked, []versioninggate.Result{numeric}, nil, &OverrideAudit{GateResultID: resultID, Reason: "measured regression accepted", Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	wrongBaseline := numeric
	wrongBaseline.Context.Candidate.BaselineReleaseID = confirmationID(t)
	if err := ValidateConfirmations(candidate, baseline, blocked, []versioninggate.Result{wrongBaseline}, nil, &OverrideAudit{GateResultID: resultID, Reason: "reason", Confirmed: true}); !errors.Is(err, ErrBaselineConfirmation) {
		t.Fatalf("stale risk baseline=%v", err)
	}
	deterministic := confirmationResult(resultID, candidate, false)
	if err := ValidateConfirmations(candidate, baseline, blocked, []versioninggate.Result{deterministic}, nil, &OverrideAudit{GateResultID: resultID, Reason: "reason", Confirmed: true}); !errors.Is(err, ErrOverrideInvalid) {
		t.Fatalf("deterministic override=%v", err)
	}
}

func confirmationCandidate(t *testing.T, baseline domain.ID) versioningrevision.CandidateContext {
	t.Helper()
	return versioningrevision.CandidateContext{RevisionID: confirmationID(t), ConfigHash: strings.Repeat("a", 64), ManifestHash: strings.Repeat("b", 64), PolicyID: confirmationID(t), BaselineReleaseID: baseline}
}

func confirmationResult(id domain.ID, candidate versioningrevision.CandidateContext, overridable bool) versioninggate.Result {
	return versioninggate.Result{ID: id, Descriptor: versioninggate.Descriptor{CapabilityID: "risk", GateID: "threshold", ContractVersion: "1", ImplementationVersion: "risk-v1", RequiredInputs: []string{"revision_id"}, SupportedStates: []versioninggate.ResultState{versioninggate.Block}, OverridableNumericBlock: overridable}, State: versioninggate.Block, ResultHash: strings.Repeat("c", 64), Context: versioninggate.EvaluationContext{Candidate: candidate, PolicyHash: strings.Repeat("d", 64), ImplementationVersions: map[string]string{"risk": "risk-v1"}}}
}

func confirmationID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
