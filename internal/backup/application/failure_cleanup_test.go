package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
)

type cleanupSource struct {
	identity ports.SnapshotIdentity
	err      error
}

func (source cleanupSource) Identity(context.Context) (ports.SnapshotIdentity, error) {
	return source.identity, nil
}
func (source cleanupSource) OnlineBackup(context.Context, string, func(ports.BackupProgress) error) error {
	return source.err
}

type cleanupVerifier struct {
	calls      int
	failAt     int
	mismatchAt int
	projectID  domain.ID
	schema     int
}

func (verifier *cleanupVerifier) Verify(context.Context, string, domain.ID, int) (ports.VerifiedSnapshot, error) {
	verifier.calls++
	if verifier.calls == verifier.failAt {
		return ports.VerifiedSnapshot{}, errors.New("injected integrity/hash failure")
	}
	hash := strings.Repeat("a", 64)
	if verifier.calls == verifier.mismatchAt {
		hash = strings.Repeat("b", 64)
	}
	return ports.VerifiedSnapshot{ProjectID: verifier.projectID, SchemaVersion: verifier.schema, Bytes: 4096, SHA256: hash}, nil
}

type cleanupStaging struct {
	id          domain.ID
	manifestErr error
	flushErr    error
	closed      bool
}

func (artifact *cleanupStaging) ID() domain.ID { return artifact.id }
func (artifact *cleanupStaging) DatabasePath() string {
	return "/managed/.staging/" + string(artifact.id) + "/project.db"
}
func (artifact *cleanupStaging) WriteManifest(context.Context, []byte) error {
	return artifact.manifestErr
}
func (artifact *cleanupStaging) Flush(context.Context) error { return artifact.flushErr }
func (artifact *cleanupStaging) Close() error                { artifact.closed = true; return nil }

type cleanupArtifacts struct {
	artifact           *cleanupStaging
	createErr          error
	publishErr         error
	discarded          int
	listed             bool
	closeBeforeDiscard bool
}

func (store *cleanupArtifacts) CreateStaging(context.Context, domain.ID, domain.ID) (ports.StagingArtifact, error) {
	if store.createErr != nil {
		return nil, store.createErr
	}
	return store.artifact, nil
}
func (store *cleanupArtifacts) Publish(context.Context, ports.StagingArtifact, backupdomain.Manifest) (backupdomain.Result, error) {
	if store.publishErr != nil {
		return backupdomain.Result{}, store.publishErr
	}
	store.listed = true
	return backupdomain.Result{}, errors.New("injected invalid publication result")
}
func (store *cleanupArtifacts) Discard(_ context.Context, artifact ports.StagingArtifact) error {
	store.discarded++
	store.closeBeforeDiscard = artifact.(*cleanupStaging).closed
	return nil
}
func (store *cleanupArtifacts) List(context.Context, ports.InventoryQuery) (ports.InventoryPage, error) {
	if store.listed {
		return ports.InventoryPage{Items: []backupdomain.InventoryRecord{{BackupID: store.artifact.id}}}, nil
	}
	return ports.InventoryPage{Items: []backupdomain.InventoryRecord{}}, nil
}
func (*cleanupArtifacts) Acquire(context.Context, domain.ID, domain.ID) (backupdomain.InventoryRecord, ports.ArtifactLease, error) {
	return backupdomain.InventoryRecord{}, nil, errors.New("not found")
}
func (*cleanupArtifacts) TrashAndDelete(context.Context, domain.ID, domain.ID, string) error {
	return nil
}

func TestPublicationFailuresCloseThenDiscardOwnedStagingWithoutListing(t *testing.T) {
	projectID, _ := domain.NewID()
	backupID, _ := domain.NewID()
	command := backupdomain.Command{
		CommandVersion: backupdomain.CommandVersion,
		ProjectID:      projectID,
		Purpose:        backupdomain.Migration,
		CallerJobID:    backupID,
		CallerHash:     strings.Repeat("c", 64),
		Source:         backupdomain.SourceIdentity{CallerJobID: backupID, RequestHash: strings.Repeat("c", 64)},
	}
	tests := []struct {
		name        string
		createErr   error
		copyErr     error
		manifestErr error
		flushErr    error
		verifyFail  int
		mismatch    int
		publishErr  error
	}{
		{name: "permission_create", createErr: errors.New("permission denied")},
		{name: "disk_full_copy", copyErr: errors.New("disk full")},
		{name: "source_close", copyErr: errors.New("source close failed")},
		{name: "short_write_manifest", manifestErr: errors.New("short write")},
		{name: "flush", flushErr: errors.New("flush failed")},
		{name: "integrity", verifyFail: 1},
		{name: "post_copy_hash", verifyFail: 2},
		{name: "post_copy_mutation", mismatch: 2},
		{name: "sharing_violation", publishErr: errors.New("sharing violation")},
		{name: "atomic_rename", publishErr: errors.New("rename failed")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			artifact := &cleanupStaging{id: backupID, manifestErr: test.manifestErr, flushErr: test.flushErr}
			artifacts := &cleanupArtifacts{artifact: artifact, createErr: test.createErr, publishErr: test.publishErr}
			verifier := &cleanupVerifier{failAt: test.verifyFail, mismatchAt: test.mismatch, projectID: projectID, schema: 22}
			service := NewService(cleanupSource{identity: ports.SnapshotIdentity{ProjectID: projectID, ProjectPath: "/active", AppVersion: "test", SchemaVersion: 22}, err: test.copyErr}, artifacts, verifier, nil, nil, nil)
			service.IDs = IDFunc(func() (domain.ID, error) { return backupID, nil })
			if _, err := service.ExecuteDirect(context.Background(), command, nil); err == nil {
				t.Fatal("failure injection unexpectedly published")
			}
			page, err := artifacts.List(context.Background(), ports.InventoryQuery{ProjectID: projectID})
			if err != nil || len(page.Items) != 0 {
				t.Fatalf("partial artifact became discoverable: %#v err=%v", page.Items, err)
			}
			if test.createErr == nil && (artifacts.discarded != 1 || !artifacts.closeBeforeDiscard) {
				t.Fatalf("cleanup order discarded=%d closed_first=%v", artifacts.discarded, artifacts.closeBeforeDiscard)
			}
		})
	}
}
