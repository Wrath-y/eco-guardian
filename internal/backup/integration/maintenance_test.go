package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/project"
)

type targetHandle struct{ id domain.ID }

func (handle targetHandle) Close() error  { return nil }
func (handle targetHandle) ID() domain.ID { return handle.id }

type targetFactory struct{ handle targetHandle }

func (factory targetFactory) Create(context.Context, string) (project.ProjectHandle, error) {
	return factory.handle, nil
}
func (factory targetFactory) Open(context.Context, string) (project.ProjectHandle, error) {
	return factory.handle, nil
}

func TestEmptyRestoreTargetConsumesFreshTokenReservesAtomicallyAndDetectsRaces(t *testing.T) {
	ctx := context.Background()
	projectID, _ := domain.NewID()
	tokens := project.NewTokenStore(time.Minute, nil)
	manager := project.NewManager(tokens, project.FileLocker{}, nil, project.NoJobs{}, nil)
	targets := NewProjectTargets(manager, tokens)
	defer func() {
		if err := targets.Close(); err != nil {
			t.Error(err)
		}
	}()
	directory := filepath.Clean(t.TempDir())
	token, _, _ := tokens.Issue(directory)
	state, err := targets.ResolveEmpty(ctx, projectID, token)
	if err != nil || state.Mode != backupdomain.RestoreEmptySelection || state.ProjectID != projectID || state.CanonicalPath == "" || state.Identity == "" || state.Generation == "" {
		t.Fatalf("state=%#v err=%v", state, err)
	}
	if _, err = targets.ResolveEmpty(ctx, projectID, token); !errors.Is(err, project.ErrInvalidSelection) {
		t.Fatalf("selection token was reusable: %v", err)
	}
	if actual, err := targets.Revalidate(ctx, state); err != nil || actual != state {
		t.Fatalf("revalidate=%#v err=%v", actual, err)
	}
	if err = os.WriteFile(filepath.Join(directory, "raced.txt"), []byte("raced"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = targets.Revalidate(ctx, state); !errors.Is(err, project.ErrMaintenance) {
		t.Fatalf("target race was not rejected: %v", err)
	}
}

func TestEmptyRestoreTargetRejectsNonemptySpecialSymlinkAndCurrentProjectOverlap(t *testing.T) {
	ctx := context.Background()
	projectID, _ := domain.NewID()
	tokens := project.NewTokenStore(time.Minute, nil)
	manager := project.NewManager(tokens, project.FileLocker{}, nil, project.NoJobs{}, nil)
	targets := NewProjectTargets(manager, tokens)

	nonempty := filepath.Clean(t.TempDir())
	if err := os.WriteFile(filepath.Join(nonempty, "existing"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	token, _, _ := tokens.Issue(nonempty)
	if _, err := targets.ResolveEmpty(ctx, projectID, token); !errors.Is(err, ErrRestoreTargetNotEmpty) {
		t.Fatalf("nonempty target err=%v", err)
	}
	if contents, _ := os.ReadFile(filepath.Join(nonempty, "existing")); string(contents) != "keep" {
		t.Fatal("nonempty target was mutated")
	}

	regular := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(regular, []byte("special"), 0o600); err != nil {
		t.Fatal(err)
	}
	token, _, _ = tokens.Issue(regular)
	if _, err := targets.ResolveEmpty(ctx, projectID, token); !errors.Is(err, ErrRestoreTargetUnsafe) {
		t.Fatalf("regular-file target err=%v", err)
	}

	realDirectory := filepath.Clean(t.TempDir())
	symlink := filepath.Join(t.TempDir(), "selected-link")
	if err := os.Symlink(realDirectory, symlink); err == nil {
		token, _, _ = tokens.Issue(symlink)
		if _, err = targets.ResolveEmpty(ctx, projectID, token); !errors.Is(err, ErrRestoreTargetUnsafe) {
			t.Fatalf("symlink target err=%v", err)
		}
	}

	activeTokens := project.NewTokenStore(time.Minute, nil)
	activeRoot := filepath.Clean(t.TempDir())
	nested := filepath.Join(activeRoot, "empty-target")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	activeManager := project.NewManager(activeTokens, project.FileLocker{}, targetFactory{handle: targetHandle{id: projectID}}, project.NoJobs{}, nil)
	activeToken, _, _ := activeTokens.Issue(activeRoot)
	if _, err := activeManager.Create(ctx, activeToken); err != nil {
		t.Fatal(err)
	}
	targets = NewProjectTargets(activeManager, activeTokens)
	token, _, _ = activeTokens.Issue(nested)
	if _, err := targets.ResolveEmpty(ctx, projectID, token); !errors.Is(err, ErrRestoreTargetUnsafe) {
		t.Fatalf("current-project overlap err=%v", err)
	}
	if err := activeManager.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
