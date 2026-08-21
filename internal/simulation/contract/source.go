package contract

import (
	"context"
	"errors"
)

var (
	ErrSourceInvalid   = errors.New("simulation source is invalid")
	ErrSourceOwnership = errors.New("simulation source belongs to another project")
)

// SourceSelection permits exactly one immutable source. A release is resolved
// once to its pinned revision and the returned value is thereafter used as the
// simulation input; callers never receive a mutable release pointer.
type SourceSelection struct {
	ProjectID  ID
	RevisionID ID
	ReleaseID  ID
}

func ResolveSource(ctx context.Context, revisions RevisionSource, releases ReleaseSource, selection SourceSelection) (Revision, error) {
	if selection.ProjectID == "" || (selection.RevisionID == "" && selection.ReleaseID == "") || (selection.RevisionID != "" && selection.ReleaseID != "") {
		return Revision{}, ErrSourceInvalid
	}
	var (
		revision Revision
		err      error
	)
	if selection.RevisionID != "" {
		if revisions == nil {
			return Revision{}, ErrSourceInvalid
		}
		revision, err = revisions.ResolveRevision(ctx, selection.RevisionID)
	} else {
		if releases == nil {
			return Revision{}, ErrSourceInvalid
		}
		revision, err = releases.ResolveReleaseRevision(ctx, selection.ReleaseID)
	}
	if err != nil {
		return Revision{}, err
	}
	if revision.ID == "" || revision.ProjectID == "" || revision.ConfigHash == "" || revision.ManifestHash == "" {
		return Revision{}, ErrSourceInvalid
	}
	if revision.ProjectID != selection.ProjectID {
		return Revision{}, ErrSourceOwnership
	}
	if selection.RevisionID != "" && revision.ID != selection.RevisionID {
		return Revision{}, ErrSourceInvalid
	}
	return revision, nil
}
