package integration

import (
	"context"

	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	backupfs "github.com/zouyi/eco-guardian/internal/backup/filesystem"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
)

type InventoryRootResolver interface {
	ResolveInventoryRoot(context.Context) (string, error)
}

// ManagedInventory re-resolves the configured managed root for every lookup,
// so a settings change cannot leave a detached restore reading an old root.
type ManagedInventory struct {
	Roots     InventoryRootResolver
	MaxSchema int
}

var _ ports.ManagedBackupInventory = ManagedInventory{}

func (inventory ManagedInventory) Resolve(ctx context.Context, backupID domain.ID) (backupdomain.InventoryRecord, ports.ArtifactLease, error) {
	if inventory.Roots == nil || inventory.MaxSchema < 1 {
		return backupdomain.InventoryRecord{}, nil, backupfs.ErrPathSecurity
	}
	root, err := inventory.Roots.ResolveInventoryRoot(ctx)
	if err != nil {
		return backupdomain.InventoryRecord{}, nil, err
	}
	store, err := backupfs.NewStore(root, inventory.MaxSchema)
	if err != nil {
		return backupdomain.InventoryRecord{}, nil, err
	}
	return store.Resolve(ctx, backupID)
}

func (inventory ManagedInventory) ListAll(ctx context.Context, after string, limit int) (ports.InventoryPage, error) {
	if inventory.Roots == nil || inventory.MaxSchema < 1 {
		return ports.InventoryPage{}, backupfs.ErrPathSecurity
	}
	root, err := inventory.Roots.ResolveInventoryRoot(ctx)
	if err != nil {
		return ports.InventoryPage{}, err
	}
	store, err := backupfs.NewStore(root, inventory.MaxSchema)
	if err != nil {
		return ports.InventoryPage{}, err
	}
	return store.ListAll(ctx, after, limit)
}
