package sync

import "errors"

var ErrReconciliationInvalid = errors.New("graph task reconciliation is invalid")

type ReconciliationDecision struct {
	AcceptExisting bool
	Resubmit       bool
	Verification   SnapshotVerification
}

// ReconcileMissingTask never guesses from a task error alone. A verifiable
// target Snapshot wins; only an absent target permits replaying the same PUT.
func ReconcileMissingTask(expected SnapshotExpectation, snapshot *Snapshot) (ReconciliationDecision, error) {
	if snapshot == nil {
		return ReconciliationDecision{Resubmit: true}, nil
	}
	verification := VerifySnapshot(expected, *snapshot)
	if verification.Ready {
		return ReconciliationDecision{AcceptExisting: true, Verification: verification}, nil
	}
	return ReconciliationDecision{Verification: verification}, ErrReconciliationInvalid
}
