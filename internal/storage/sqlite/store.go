package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	root "github.com/zouyi/eco-guardian"
	"github.com/zouyi/eco-guardian/internal/domain"
	riskthreshold "github.com/zouyi/eco-guardian/internal/risk/threshold"
	"github.com/zouyi/eco-guardian/internal/validation"
	versioning "github.com/zouyi/eco-guardian/internal/versioning"
	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
	_ "modernc.org/sqlite"
)

const databaseName = "project.db"
const currentSchemaVersion = 18

func DBSchemaVersion() int { return currentSchemaVersion }

var (
	ErrProjectInvalid          = errors.New("invalid or unsupported project database")
	ErrDuplicateKey            = errors.New("duplicate active kind/key")
	ErrNotFound                = errors.New("entity not found")
	ErrRevisionConflict        = errors.New("entity revision conflict")
	ErrMigrationBackupRequired = errors.New("migration requires successful online backup")
	ErrSchemaTooNew            = errors.New("project schema is newer than this application")
	errLocalPreflightChanged   = errors.New("local validation preflight changed")
)

// BackupEvidence proves an adapter completed all mandatory pre-migration
// checks. The SQLite package owns only this port; backup implementation belongs
// to the later backup capability.
type BackupEvidence struct {
	Online           bool
	IntegrityChecked bool
	Checksum         string
}

func (e BackupEvidence) Valid() bool {
	if !e.Online || !e.IntegrityChecked || len(e.Checksum) != 64 {
		return false
	}
	for _, r := range e.Checksum {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

type MigrationBackup interface {
	Backup(context.Context, string, domain.ID) (BackupEvidence, error)
}

type SchemaTooNewError struct{ DatabaseVersion, SupportedVersion int }

func (e *SchemaTooNewError) Unwrap() error { return ErrSchemaTooNew }

func (e *SchemaTooNewError) Error() string {
	return fmt.Sprintf("%s: database=%d supported=%d", ErrSchemaTooNew, e.DatabaseVersion, e.SupportedVersion)
}

type ReferencedError struct{ References []domain.Reference }

func (e *ReferencedError) Error() string { return "entity is referenced" }

type ValidationError struct{ Issues []domain.FieldIssue }

func (e ValidationError) Error() string { return "entity validation failed" }

type Store struct {
	db                *sql.DB
	path              string
	registry          *domain.Registry
	writes            sync.Mutex
	now               func() time.Time
	projectID         domain.ID
	graphVersion      versioningrevision.VersionEntry
	simulationVersion versioningrevision.VersionEntry
	riskVersion       versioningrevision.VersionEntry
	afterRevision     func(context.Context, domain.RevisionSummary)
	failStage         func(string) error // test-only transaction fault injector
}

// RegisterGraphVersionContributor is startup composition glue. Its entry is
// copied under the write lock and becomes part of every subsequently saved
// immutable revision; existing records are never rewritten.
func (s *Store) RegisterGraphVersionContributor(contributor versioningrevision.VersionContributor) error {
	if contributor == nil || contributor.CapabilityID() != "graph-projector" {
		return errors.New("invalid graph version contributor")
	}
	entry := versioningrevision.VersionEntry{CapabilityID: contributor.CapabilityID(), ContractVersion: contributor.ContractVersion(), ImplementationVersion: contributor.ImplementationVersion(), State: contributor.RegistrationState()}
	if !entry.Valid() {
		return errors.New("invalid graph version contributor")
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	s.graphVersion = entry
	return nil
}

// RegisterSimulationVersionContributor records the exact implementation for
// future revision metadata only. Existing historical manifests remain read
// only, including explicitly unavailable simulation entries.
func (s *Store) RegisterSimulationVersionContributor(contributor versioningrevision.VersionContributor) error {
	if contributor == nil || contributor.CapabilityID() != "simulation-engine" {
		return errors.New("invalid simulation version contributor")
	}
	entry := versioningrevision.VersionEntry{CapabilityID: contributor.CapabilityID(), ContractVersion: contributor.ContractVersion(), ImplementationVersion: contributor.ImplementationVersion(), State: contributor.RegistrationState()}
	if !entry.Valid() {
		return errors.New("invalid simulation version contributor")
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	s.simulationVersion = entry
	return nil
}

// RegisterRiskVersionContributor pins the installed risk contract into future
// revisions. The unregistered default is preserved explicitly when risk is
// absent, so release capability checks can disable rather than guess.
func (s *Store) RegisterRiskVersionContributor(contributor versioningrevision.VersionContributor) error {
	if contributor == nil || contributor.CapabilityID() != "risk" {
		return errors.New("invalid risk version contributor")
	}
	entry := versioningrevision.VersionEntry{CapabilityID: contributor.CapabilityID(), ContractVersion: contributor.ContractVersion(), ImplementationVersion: contributor.ImplementationVersion(), State: contributor.RegistrationState()}
	if !entry.Valid() {
		return errors.New("invalid risk version contributor")
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	s.riskVersion = entry
	return nil
}

// RegisterRevisionObserver installs a post-commit lifecycle seam. Observers
// run only after the immutable revision transaction commits, so derived Graph
// work cannot roll back user configuration facts.
func (s *Store) RegisterRevisionObserver(observer func(context.Context, domain.RevisionSummary)) {
	s.writes.Lock()
	defer s.writes.Unlock()
	s.afterRevision = observer
}

func (s *Store) notifyRevisionCommitted(ctx context.Context, revision domain.RevisionSummary) {
	if s.afterRevision != nil && revision.ID.Valid() {
		s.afterRevision(ctx, revision)
	}
}

func Create(ctx context.Context, dir string, registry *domain.Registry) (*Store, domain.ID, error) {
	path := filepath.Join(dir, databaseName)
	if _, err := os.Stat(path); err == nil {
		return nil, "", fmt.Errorf("%w: database already exists", ErrProjectInvalid)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", err
	}
	s, err := open(path, registry)
	if err != nil {
		return nil, "", err
	}
	migration, err := root.Assets.ReadFile("migrations/0001_initial.sql")
	if err != nil {
		s.Close()
		return nil, "", err
	}
	id, err := domain.NewID()
	if err != nil {
		s.Close()
		return nil, "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.Close()
		return nil, "", err
	}
	_, err = tx.ExecContext(ctx, string(migration))
	if err == nil {
		_, err = tx.ExecContext(ctx, "INSERT INTO project_meta(id,db_schema_version,created_at) VALUES(?,?,?)", id, 1, s.now().UTC().Format(time.RFC3339Nano))
	}
	if err == nil {
		err = applyMigrationSteps(ctx, tx, 1, nil)
	}
	if err != nil {
		tx.Rollback()
		s.Close()
		return nil, "", err
	}
	if err = tx.Commit(); err != nil {
		s.Close()
		return nil, "", err
	}
	s.projectID = id
	return s, id, nil
}

func Open(dir string, registry *domain.Registry) (*Store, domain.ID, error) {
	return OpenWithMigrationBackup(context.Background(), dir, registry, nil)
}

// OpenWithMigrationBackup upgrades only after schema support is read and, for
// a populated older project, a registered backup port proves online backup,
// integrity, and checksum success before any migration transaction starts.
func OpenWithMigrationBackup(ctx context.Context, dir string, registry *domain.Registry, backup MigrationBackup) (*Store, domain.ID, error) {
	s, err := open(filepath.Join(dir, databaseName), registry)
	if err != nil {
		return nil, "", err
	}
	var id domain.ID
	var version int
	if err = s.db.QueryRow("SELECT id,db_schema_version FROM project_meta").Scan(&id, &version); err != nil || !id.Valid() {
		s.Close()
		return nil, "", fmt.Errorf("%w: %v", ErrProjectInvalid, err)
	}
	if version > currentSchemaVersion {
		s.Close()
		return nil, "", &SchemaTooNewError{DatabaseVersion: version, SupportedVersion: currentSchemaVersion}
	}
	if version < 1 {
		s.Close()
		return nil, "", fmt.Errorf("%w: unsupported schema version %d", ErrProjectInvalid, version)
	}
	if version < currentSchemaVersion {
		populated, dataErr := hasProjectData(ctx, s.db)
		if dataErr != nil {
			s.Close()
			return nil, "", fmt.Errorf("%w: %v", ErrProjectInvalid, dataErr)
		}
		if populated {
			if backup == nil {
				s.Close()
				return nil, "", ErrMigrationBackupRequired
			}
			evidence, backupErr := backup.Backup(ctx, dir, id)
			if backupErr != nil || !evidence.Valid() {
				s.Close()
				return nil, "", fmt.Errorf("%w: %v", ErrMigrationBackupRequired, backupErr)
			}
		}
		if err = upgradeToCurrent(ctx, s.db, version); err != nil {
			s.Close()
			return nil, "", fmt.Errorf("%w: %v", ErrProjectInvalid, err)
		}
	}
	s.projectID = id
	if err = verifyMigrationSteps(ctx, s.db); err != nil {
		s.Close()
		return nil, "", fmt.Errorf("%w: %v", ErrProjectInvalid, err)
	}
	return s, id, nil
}

func upgradeV2(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = applyMigrationV2(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}
func upgradeToCurrent(ctx context.Context, db *sql.DB, version int) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = applyMigrationSteps(ctx, tx, version, nil); err != nil {
		return err
	}
	version = currentSchemaVersion
	if version != currentSchemaVersion {
		return fmt.Errorf("unsupported schema version %d", version)
	}
	return tx.Commit()
}
func applyMigrationSteps(ctx context.Context, tx *sql.Tx, version int, hook func(string) error) error {
	if version == 1 {
		if err := applyMigrationV2(ctx, tx); err != nil {
			return err
		}
		if hook != nil {
			if err := hook("validation-v2"); err != nil {
				return err
			}
		}
		version = 2
	}
	if version == 2 {
		if err := applyMigrationV3(ctx, tx); err != nil {
			return err
		}
		if hook != nil {
			if err := hook("versioning-v3"); err != nil {
				return err
			}
		}
		version = 3
	}
	if version == 3 {
		if err := applyMigrationV4(ctx, tx); err != nil {
			return err
		}
		version = 4
	}
	if version == 4 {
		if err := applyMigrationV5(ctx, tx); err != nil {
			return err
		}
		if hook != nil {
			if err := hook("graph-sync-v5"); err != nil {
				return err
			}
		}
		version = 5
	}
	if version == 5 {
		if err := applyMigrationV6(ctx, tx); err != nil {
			return err
		}
		version = 6
	}
	if version == 6 {
		if err := applyMigrationV7(ctx, tx); err != nil {
			return err
		}
		version = 7
	}
	if version == 7 {
		if err := applyMigrationV8(ctx, tx); err != nil {
			return err
		}
		version = 8
	}
	if version == 8 {
		if err := applyMigrationV9(ctx, tx); err != nil {
			return err
		}
		version = 9
	}
	if version == 9 {
		if err := applyMigrationV10(ctx, tx); err != nil {
			return err
		}
		version = 10
	}
	if version == 10 {
		if err := applyMigrationV11(ctx, tx); err != nil {
			return err
		}
		version = 11
	}
	if version == 11 {
		if err := applyMigrationV12(ctx, tx); err != nil {
			return err
		}
		version = 12
	}
	if version == 12 {
		if err := applyMigrationV13(ctx, tx); err != nil {
			return err
		}
		version = 13
	}
	if version == 13 {
		if err := applyMigrationV14(ctx, tx); err != nil {
			return err
		}
		version = 14
	}
	if version == 14 {
		if err := applyMigrationV15(ctx, tx); err != nil {
			return err
		}
		version = 15
	}
	if version == 15 {
		if err := applyMigrationV16(ctx, tx); err != nil {
			return err
		}
		version = 16
	}
	if version == 16 {
		if err := applyMigrationV17(ctx, tx); err != nil {
			return err
		}
		if hook != nil {
			if err := hook("ai-design-persistence-v17"); err != nil {
				return err
			}
		}
		version = 17
	}
	if version == 17 {
		if err := applyMigrationV18(ctx, tx); err != nil {
			return err
		}
		if hook != nil {
			if err := hook("ai-generation-immutability-v18"); err != nil {
				return err
			}
		}
		version = 18
	}
	if version != currentSchemaVersion {
		return fmt.Errorf("unsupported schema version %d", version)
	}
	return nil
}
func verifyMigrationSteps(ctx context.Context, db *sql.DB) error {
	checksums, err := migrationStepChecksums()
	if err != nil {
		return err
	}
	for stepID, want := range checksums {
		var got sql.NullString
		if err = db.QueryRowContext(ctx, `SELECT checksum FROM schema_migration_steps WHERE step_id=?`, stepID).Scan(&got); err != nil {
			return errors.New("missing committed migration step")
		}
		if !got.Valid || got.String != want {
			return errors.New("migration checksum mismatch")
		}
	}
	return nil
}
func applyMigrationV4(ctx context.Context, tx *sql.Tx) error {
	body, err := root.Assets.ReadFile("migrations/0004_release_audit.sql")
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, string(body))
	return err
}
func applyMigrationV5(ctx context.Context, tx *sql.Tx) error {
	body, err := root.Assets.ReadFile("migrations/0005_graph_sync.sql")
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, string(body))
	return err
}
func applyMigrationV6(ctx context.Context, tx *sql.Tx) error {
	body, err := root.Assets.ReadFile("migrations/0006_graph_checkpoints.sql")
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, string(body))
	return err
}
func applyMigrationV7(ctx context.Context, tx *sql.Tx) error {
	body, err := root.Assets.ReadFile("migrations/0007_graph_sync_indexes.sql")
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, string(body))
	return err
}
func applyMigrationV8(ctx context.Context, tx *sql.Tx) error {
	body, err := root.Assets.ReadFile("migrations/0008_graph_migration_checksums.sql")
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, string(body)); err != nil {
		return err
	}
	return recordMigrationChecksums(ctx, tx)
}

func applyMigrationV9(ctx context.Context, tx *sql.Tx) error {
	body, err := root.Assets.ReadFile("migrations/0009_graph_job_evidence.sql")
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, string(body)); err != nil {
		return err
	}
	return recordMigrationChecksums(ctx, tx)
}

func applyMigrationV10(ctx context.Context, tx *sql.Tx) error {
	body, err := root.Assets.ReadFile("migrations/0010_shared_job_cancellation.sql")
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, string(body)); err != nil {
		return err
	}
	return recordMigrationChecksums(ctx, tx)
}

func applyMigrationV11(ctx context.Context, tx *sql.Tx) error {
	body, err := root.Assets.ReadFile("migrations/0011_simulation_scenarios.sql")
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, string(body)); err != nil {
		return err
	}
	if err = seedBuiltinScenarioDefinitions(ctx, tx); err != nil {
		return err
	}
	return recordMigrationChecksums(ctx, tx)
}

func applyMigrationV12(ctx context.Context, tx *sql.Tx) error {
	body, err := root.Assets.ReadFile("migrations/0012_simulation_runs.sql")
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, string(body)); err != nil {
		return err
	}
	return recordMigrationChecksums(ctx, tx)
}

func applyMigrationV13(ctx context.Context, tx *sql.Tx) error {
	body, err := root.Assets.ReadFile("migrations/0013_simulation_job_materializations.sql")
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, string(body)); err != nil {
		return err
	}
	return recordMigrationChecksums(ctx, tx)
}

func applyMigrationV14(ctx context.Context, tx *sql.Tx) error {
	body, err := root.Assets.ReadFile("migrations/0014_simulation_run_implementations.sql")
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, string(body)); err != nil {
		return err
	}
	return recordMigrationChecksums(ctx, tx)
}

func applyMigrationV15(ctx context.Context, tx *sql.Tx) error {
	body, err := root.Assets.ReadFile("migrations/0015_simulation_verification_intents.sql")
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, string(body)); err != nil {
		return err
	}
	return recordMigrationChecksums(ctx, tx)
}

func applyMigrationV16(ctx context.Context, tx *sql.Tx) error {
	body, err := root.Assets.ReadFile("migrations/0016_balance_risk_assessment.sql")
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, string(body)); err != nil {
		return err
	}
	if err = seedRiskStarterThreshold(ctx, tx); err != nil {
		return err
	}
	return recordMigrationChecksums(ctx, tx)
}

func applyMigrationV17(ctx context.Context, tx *sql.Tx) error {
	body, err := root.Assets.ReadFile("migrations/0017_ai_design_persistence.sql")
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, string(body)); err != nil {
		return err
	}
	return recordMigrationChecksums(ctx, tx)
}

func applyMigrationV18(ctx context.Context, tx *sql.Tx) error {
	body, err := root.Assets.ReadFile("migrations/0018_ai_generation_immutability.sql")
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, string(body)); err != nil {
		return err
	}
	return recordMigrationChecksums(ctx, tx)
}

func seedRiskStarterThreshold(ctx context.Context, tx *sql.Tx) error {
	starter := riskthreshold.StarterFixtureV1()
	body, err := starter.Body.CanonicalJSON()
	if err != nil {
		return err
	}
	var projectID, createdAt string
	if err = tx.QueryRowContext(ctx, `SELECT id,created_at FROM project_meta`).Scan(&projectID, &createdAt); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO threshold_versions(id,project_uuid,display_version,origin,enabled,schema_version,canonical_body,body_hash,created_by,created_at) VALUES(?,?,0,'starter_template',0,?,?,?,?,?)`, starter.ID, projectID, riskthreshold.SchemaVersionV1, string(body), starter.BodyHash, "system", createdAt)
	return err
}

func recordMigrationChecksums(ctx context.Context, tx *sql.Tx) error {
	checksums, err := migrationStepChecksums()
	if err != nil {
		return err
	}
	for stepID, checksum := range checksums {
		write, updateErr := tx.ExecContext(ctx, `UPDATE schema_migration_steps SET checksum=? WHERE step_id=?`, checksum, stepID)
		if updateErr != nil {
			return updateErr
		}
		if rows, rowsErr := write.RowsAffected(); rowsErr != nil {
			return errors.New("missing migration step while recording checksum")
		} else if rows == 0 {
			continue
		}
	}
	return nil
}

func migrationStepChecksums() (map[string]string, error) {
	files := map[string]string{
		"validation-v2":                       "migrations/0002_validation.sql",
		"versioning-v3":                       "migrations/0003_versioning.sql",
		"release-audit-v4":                    "migrations/0004_release_audit.sql",
		"graph-sync-v5":                       "migrations/0005_graph_sync.sql",
		"graph-checkpoints-v6":                "migrations/0006_graph_checkpoints.sql",
		"graph-sync-indexes-v7":               "migrations/0007_graph_sync_indexes.sql",
		"graph-migration-checksums-v8":        "migrations/0008_graph_migration_checksums.sql",
		"graph-job-evidence-v9":               "migrations/0009_graph_job_evidence.sql",
		"shared-job-cancellation-v10":         "migrations/0010_shared_job_cancellation.sql",
		"simulation-scenarios-v11":            "migrations/0011_simulation_scenarios.sql",
		"simulation-runs-v12":                 "migrations/0012_simulation_runs.sql",
		"simulation-job-materializations-v13": "migrations/0013_simulation_job_materializations.sql",
		"simulation-run-implementations-v14":  "migrations/0014_simulation_run_implementations.sql",
		"simulation-verification-intents-v15": "migrations/0015_simulation_verification_intents.sql",
		"balance-risk-assessment-v16":         "migrations/0016_balance_risk_assessment.sql",
		"ai-design-persistence-v17":           "migrations/0017_ai_design_persistence.sql",
		"ai-generation-immutability-v18":      "migrations/0018_ai_generation_immutability.sql",
	}
	checksums := make(map[string]string, len(files))
	for stepID, path := range files {
		body, err := root.Assets.ReadFile(path)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(body)
		checksums[stepID] = fmt.Sprintf("%x", digest)
	}
	return checksums, nil
}
func hasProjectData(ctx context.Context, db *sql.DB) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM config_revisions`).Scan(&count)
	return count > 0, err
}
func applyMigrationV2(ctx context.Context, tx *sql.Tx) error {
	migration, err := root.Assets.ReadFile("migrations/0002_validation.sql")
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, string(migration))
	return err
}

// applyMigrationV3 is intentionally invoked by the ordered migration runner
// added with the versioning upgrade preflight. Keeping the schema step separate
// lets a new runner enforce mandatory backup before mutating an existing DB.
func applyMigrationV3(ctx context.Context, tx *sql.Tx) error {
	migration, err := root.Assets.ReadFile("migrations/0003_versioning.sql")
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, string(migration)); err != nil {
		return err
	}
	if err = backfillRevisionMetadata(ctx, tx); err != nil {
		return err
	}
	return seedStarterReleasePolicy(ctx, tx)
}

func seedStarterReleasePolicy(ctx context.Context, tx *sql.Tx) error {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM release_policies`).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return nil
	}
	definition := versioningpolicy.Definition{Samples: 1000, ThresholdID: "starter-threshold-v1", ThresholdOn: true,
		Capabilities: []versioningpolicy.CapabilityRequirement{{CapabilityID: "graph", GateID: "projection", ContractVersion: "1"}, {CapabilityID: "simulation", GateID: "scenario", ContractVersion: "1"}, {CapabilityID: "risk", GateID: "threshold", ContractVersion: "1"}, {CapabilityID: "backup", GateID: "online-backup", ContractVersion: "1"}},
		Scenes:       []versioningpolicy.Scene{{ID: "single-target-30s", Version: "v1", Seed: uint64ptr(11), Required: true, Metrics: []versioningpolicy.Metric{{ID: "metric-dps", Required: true}}}, {ID: "extreme-stacking-60s", Version: "v1", Seed: uint64ptr(14), Required: true, Metrics: []versioningpolicy.Metric{{ID: "metric-dps", Required: true}}}, {ID: "single-target-180s", Version: "v1", Seed: uint64ptr(12), Required: false, Metrics: []versioningpolicy.Metric{{ID: "metric-dps", Required: false}}}, {ID: "three-target-60s", Version: "v1", Seed: uint64ptr(13), Required: false, Metrics: []versioningpolicy.Metric{{ID: "metric-dps", Required: false}}}}}
	canonical, err := definition.CanonicalJSON()
	if err != nil {
		return err
	}
	id, err := domain.NewID()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO release_policies(id,display_version,canonical_body,canonical_hash,created_at) VALUES(?,?,?,?,?)`, id, 1, string(canonical), versioning.SHA256(canonical), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func uint64ptr(value uint64) *uint64 { return &value }

// backfillRevisionMetadata only reads historical immutable facts. It takes the
// #6 version manifest from a revision-source validation run when present and
// records absent later capabilities as unregistered; it never substitutes a
// currently installed implementation for history.
func backfillRevisionMetadata(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,created_at FROM config_revisions ORDER BY display_revision,id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var revisionID domain.ID
		var createdAt string
		if err = rows.Scan(&revisionID, &createdAt); err != nil {
			return err
		}
		manifest, err := historicalVersionManifest(ctx, tx, revisionID)
		if err != nil {
			return err
		}
		body, err := manifest.CanonicalJSON()
		if err != nil {
			return err
		}
		hash, err := manifest.Hash()
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO revision_metadata(revision_id,version_manifest,version_manifest_hash,created_at) VALUES(?,?,?,?)`, revisionID, string(body), hash, createdAt); err != nil {
			return err
		}
	}
	return rows.Err()
}

func historicalVersionManifest(ctx context.Context, tx *sql.Tx, revisionID domain.ID) (versioningrevision.VersionManifest, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT version_manifest FROM validation_runs WHERE source_kind='revision' AND source_revision_id=? ORDER BY created_at DESC,id DESC LIMIT 1`, revisionID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return unresolvedHistoricalManifest(), nil
	}
	if err != nil {
		return versioningrevision.VersionManifest{}, err
	}
	var versions validation.VersionManifest
	if err := json.Unmarshal([]byte(raw), &versions); err != nil {
		return versioningrevision.VersionManifest{}, err
	}
	if !versions.Valid() {
		return unresolvedHistoricalManifest(), nil
	}
	return versionManifestFromValidation(versions)
}

func versionManifestFromValidation(versions validation.VersionManifest) (versioningrevision.VersionManifest, error) {
	return versionManifestFromValidationWithCapabilities(versions, defaultGraphVersionEntry(), defaultSimulationVersionEntry(), defaultRiskVersionEntry())
}
func defaultGraphVersionEntry() versioningrevision.VersionEntry {
	return versioningrevision.VersionEntry{CapabilityID: "graph-projector", ContractVersion: "unavailable", State: versioningrevision.Unregistered}
}
func defaultSimulationVersionEntry() versioningrevision.VersionEntry {
	return versioningrevision.VersionEntry{CapabilityID: "simulation-engine", ContractVersion: "unavailable", State: versioningrevision.Unregistered}
}
func defaultRiskVersionEntry() versioningrevision.VersionEntry {
	return versioningrevision.VersionEntry{CapabilityID: "risk", ContractVersion: "unavailable", State: versioningrevision.Unregistered}
}
func versionManifestFromValidationWithGraph(versions validation.VersionManifest, graph versioningrevision.VersionEntry) (versioningrevision.VersionManifest, error) {
	return versionManifestFromValidationWithCapabilities(versions, graph, defaultSimulationVersionEntry(), defaultRiskVersionEntry())
}
func versionManifestFromValidationWithCapabilities(versions validation.VersionManifest, graph, simulation, risk versioningrevision.VersionEntry) (versioningrevision.VersionManifest, error) {
	if !versions.Valid() {
		return versioningrevision.VersionManifest{}, errors.New("incomplete validation version manifest")
	}
	manifest := versioningrevision.VersionManifest{Entries: []versioningrevision.VersionEntry{
		{CapabilityID: "schema", ContractVersion: "validation-v1", ImplementationVersion: versions.Schema, State: versioningrevision.Registered},
		{CapabilityID: "dsl", ContractVersion: "validation-v1", ImplementationVersion: versions.DSL, State: versioningrevision.Registered},
		{CapabilityID: "validator-registry", ContractVersion: "validation-v1", ImplementationVersion: versions.Registry, State: versioningrevision.Registered},
		{CapabilityID: "numeric-policy", ContractVersion: "validation-v1", ImplementationVersion: versions.NumericPolicy, State: versioningrevision.Registered},
		graph,
		simulation,
		risk,
	}}
	if !manifest.Valid() {
		return versioningrevision.VersionManifest{}, errors.New("invalid revision version manifest")
	}
	return manifest, nil
}

type revisionMetadataFields struct {
	name, description                  string
	parentRevisionID, sourceRevisionID domain.ID
	sourceReleaseID                    domain.ID
}

func (s *Store) writeRevisionMetadata(ctx context.Context, tx *sql.Tx, revision domain.RevisionSummary, versions validation.VersionManifest, fields revisionMetadataFields) error {
	manifest, err := versionManifestFromValidationWithCapabilities(versions, s.graphVersion, s.simulationVersion, s.riskVersion)
	if err != nil {
		return err
	}
	body, err := manifest.CanonicalJSON()
	if err != nil {
		return err
	}
	hash, err := manifest.Hash()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO revision_metadata(revision_id,name,description,parent_revision_id,source_revision_id,source_release_id,version_manifest,version_manifest_hash,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, revision.ID, nullString(fields.name), nullString(fields.description), nullID(fields.parentRevisionID), nullID(fields.sourceRevisionID), nullID(fields.sourceReleaseID), string(body), hash, revision.CreatedAt.Format(time.RFC3339Nano))
	return err
}

func unresolvedHistoricalManifest() versioningrevision.VersionManifest {
	entries := []versioningrevision.VersionEntry{}
	for _, capability := range []string{"schema", "dsl", "validator-registry", "numeric-policy", "graph-projector", "simulation-engine", "risk"} {
		entries = append(entries, versioningrevision.VersionEntry{CapabilityID: capability, ContractVersion: "unavailable", State: versioningrevision.Unregistered})
	}
	return versioningrevision.VersionManifest{Entries: entries}
}
func open(path string, registry *domain.Registry) (*Store, error) {
	if registry == nil {
		return nil, errors.New("registry is required")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	for _, p := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000"} {
		if _, err = db.Exec(p); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{db: db, path: path, registry: registry, now: time.Now, graphVersion: defaultGraphVersionEntry(), simulationVersion: defaultSimulationVersionEntry(), riskVersion: defaultRiskVersionEntry()}, nil
}
func (s *Store) Close() error         { return s.db.Close() }
func (s *Store) ProjectID() domain.ID { return s.projectID }

func (s *Store) Get(ctx context.Context, kind domain.EntityKind, id domain.ID) (domain.Entity, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT b.json FROM working_entities w JOIN entity_blobs b ON b.hash=w.blob_hash WHERE w.id=? AND w.kind=?`, id, kind).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Entity{}, ErrNotFound
	}
	if err != nil {
		return domain.Entity{}, err
	}
	var e domain.Entity
	return e, json.Unmarshal(raw, &e)
}

type Page struct {
	Items      []domain.Entity
	NextCursor string
}
type cursor struct {
	Key string    `json:"k"`
	ID  domain.ID `json:"i"`
}

func encodeCursor(c cursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}
func decodeCursor(v string) (cursor, error) {
	var c cursor
	b, e := base64.RawURLEncoding.DecodeString(v)
	if e != nil {
		return c, e
	}
	return c, json.Unmarshal(b, &c)
}
func (s *Store) List(ctx context.Context, kind domain.EntityKind, query, after string, limit int) (Page, error) {
	if !kind.Valid() {
		return Page{}, errors.New("unsupported kind")
	}
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		return Page{}, errors.New("limit must be 1..200")
	}
	c := cursor{}
	var err error
	if after != "" {
		c, err = decodeCursor(after)
		if err != nil {
			return Page{}, errors.New("invalid cursor")
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT b.json FROM working_entities w JOIN entity_blobs b ON b.hash=w.blob_hash WHERE w.kind=? AND w.status='active' AND (?='' OR lower(w.entity_key) LIKE lower(?) OR lower(json_extract(b.json,'$.name')) LIKE lower(?)) AND (w.entity_key>? OR (w.entity_key=? AND w.id>?)) ORDER BY w.entity_key,w.id LIMIT ?`, kind, query, "%"+query+"%", "%"+query+"%", c.Key, c.Key, c.ID, limit+1)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	p := Page{Items: make([]domain.Entity, 0, limit)}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return Page{}, err
		}
		var e domain.Entity
		if err := json.Unmarshal(raw, &e); err != nil {
			return Page{}, err
		}
		p.Items = append(p.Items, e)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	if len(p.Items) > limit {
		last := p.Items[limit-1]
		p.Items = p.Items[:limit]
		p.NextCursor = encodeCursor(cursor{last.Key, last.ID})
	}
	return p, nil
}

func (s *Store) Create(ctx context.Context, kind domain.EntityKind, d domain.EntityDraft) (domain.Entity, domain.RevisionSummary, error) {
	e, err := domain.NewEntity(kind, d, s.now())
	if err != nil {
		return domain.Entity{}, domain.RevisionSummary{}, err
	}
	e, err = s.validateProspective(e)
	if err != nil {
		return domain.Entity{}, domain.RevisionSummary{}, err
	}
	return s.save(ctx, e, true)
}
func (s *Store) Patch(ctx context.Context, kind domain.EntityKind, id domain.ID, version int64, p domain.EntityPatch) (domain.Entity, domain.RevisionSummary, error) {
	// Compose and validate outside the serialized write transaction. The same
	// entity version is rechecked inside it before any write takes place.
	current, e := s.Get(ctx, kind, id)
	if e != nil {
		return domain.Entity{}, domain.RevisionSummary{}, e
	}
	if current.EntityVersion != version {
		return domain.Entity{}, domain.RevisionSummary{}, ErrRevisionConflict
	}
	next, e := current.ApplyPatch(p, s.now())
	if e != nil {
		return domain.Entity{}, domain.RevisionSummary{}, e
	}
	next, e = s.validateProspective(next)
	if e != nil {
		return domain.Entity{}, domain.RevisionSummary{}, e
	}
	for attempt := 0; attempt < 2; attempt++ {
		preflight, preflightErr := s.preflightLocal(ctx, next)
		if preflightErr != nil {
			return domain.Entity{}, domain.RevisionSummary{}, preflightErr
		}
		s.writes.Lock()
		tx, txErr := s.db.BeginTx(ctx, nil)
		if txErr != nil {
			s.writes.Unlock()
			return domain.Entity{}, domain.RevisionSummary{}, txErr
		}
		current, txErr = s.getTx(ctx, tx, kind, id)
		if txErr == nil && current.EntityVersion != version {
			txErr = ErrRevisionConflict
		}
		if txErr == nil {
			var currentInput bool
			currentInput, txErr = s.recheckLocalPreflightTx(ctx, tx, preflight)
			if txErr == nil && !currentInput {
				txErr = errLocalPreflightChanged
			}
		}
		if txErr == nil {
			txErr = s.recheckActiveKeyTx(ctx, tx, next)
		}
		var revision domain.RevisionSummary
		if txErr == nil {
			revision, txErr = s.saveTx(ctx, tx, next, false)
		}
		if txErr == nil {
			txErr = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		s.writes.Unlock()
		if errors.Is(txErr, errLocalPreflightChanged) && attempt == 0 {
			continue
		}
		if txErr == nil {
			s.notifyRevisionCommitted(ctx, revision)
		}
		return next, revision, txErr
	}
	return domain.Entity{}, domain.RevisionSummary{}, ErrRevisionConflict
}

func (s *Store) validateProspective(entity domain.Entity) (domain.Entity, error) {
	normalized, err := domain.NormalizeNumericEntity(entity)
	if err != nil {
		return domain.Entity{}, ValidationError{[]domain.FieldIssue{{Path: "payload", Message: err.Error()}}}
	}
	if issues := s.registry.Validate(normalized); len(issues) > 0 {
		return domain.Entity{}, ValidationError{issues}
	}
	return normalized, nil
}
func (s *Store) Delete(ctx context.Context, kind domain.EntityKind, id domain.ID, version int64) (domain.Entity, domain.RevisionSummary, error) {
	s.writes.Lock()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		s.writes.Unlock()
		return domain.Entity{}, domain.RevisionSummary{}, e
	}
	var r domain.RevisionSummary
	defer tx.Rollback()
	defer func() {
		s.writes.Unlock()
		if e == nil {
			s.notifyRevisionCommitted(ctx, r)
		}
	}()
	entity, e := s.getTx(ctx, tx, kind, id)
	if e != nil {
		return domain.Entity{}, domain.RevisionSummary{}, e
	}
	if entity.EntityVersion != version {
		return domain.Entity{}, domain.RevisionSummary{}, ErrRevisionConflict
	}
	rows, e := tx.QueryContext(ctx, `SELECT r.source_entity_id,r.field_path,r.ordinal,r.expected_kind,r.target_entity_id FROM entity_references r JOIN working_entities w ON w.id=r.source_entity_id WHERE r.target_entity_id=? AND w.status='active'`, id)
	if e != nil {
		return domain.Entity{}, domain.RevisionSummary{}, e
	}
	refs := []domain.Reference{}
	for rows.Next() {
		var ref domain.Reference
		if e = rows.Scan(&ref.SourceID, &ref.FieldPath, &ref.Ordinal, &ref.ExpectedKind, &ref.TargetID); e != nil {
			rows.Close()
			return domain.Entity{}, domain.RevisionSummary{}, e
		}
		refs = append(refs, ref)
	}
	rows.Close()
	if len(refs) > 0 {
		return domain.Entity{}, domain.RevisionSummary{}, &ReferencedError{refs}
	}
	entity.Status = domain.StatusArchived
	entity.EntityVersion++
	entity.UpdatedAt = s.now().UTC()
	r, e = s.saveTx(ctx, tx, entity, false)
	if e == nil {
		e = tx.Commit()
	}
	return entity, r, e
}

func (s *Store) save(ctx context.Context, e domain.Entity, create bool) (domain.Entity, domain.RevisionSummary, error) {
	for attempt := 0; attempt < 2; attempt++ {
		preflight, err := s.preflightLocal(ctx, e)
		if err != nil {
			return domain.Entity{}, domain.RevisionSummary{}, err
		}
		s.writes.Lock()
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			s.writes.Unlock()
			return domain.Entity{}, domain.RevisionSummary{}, err
		}
		currentInput, err := s.recheckLocalPreflightTx(ctx, tx, preflight)
		if err == nil && !currentInput {
			err = errLocalPreflightChanged
		}
		if err == nil {
			err = s.recheckActiveKeyTx(ctx, tx, e)
		}
		var revision domain.RevisionSummary
		if err == nil {
			revision, err = s.saveTx(ctx, tx, e, create)
		}
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		s.writes.Unlock()
		if errors.Is(err, errLocalPreflightChanged) && attempt == 0 {
			continue
		}
		if err == nil {
			s.notifyRevisionCommitted(ctx, revision)
		}
		return e, revision, err
	}
	return domain.Entity{}, domain.RevisionSummary{}, ErrRevisionConflict
}

func (s *Store) recheckActiveKeyTx(ctx context.Context, tx *sql.Tx, entity domain.Entity) error {
	if entity.Status != domain.StatusActive {
		return nil
	}
	var existing domain.ID
	err := tx.QueryRowContext(ctx, `SELECT id FROM working_entities WHERE kind=? AND entity_key=? AND status='active' AND id<>? LIMIT 1`, entity.Kind, entity.Key, entity.ID).Scan(&existing)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrDuplicateKey
}
func (s *Store) getTx(ctx context.Context, tx *sql.Tx, kind domain.EntityKind, id domain.ID) (domain.Entity, error) {
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT b.json FROM working_entities w JOIN entity_blobs b ON b.hash=w.blob_hash WHERE w.id=? AND w.kind=?`, id, kind).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Entity{}, ErrNotFound
	}
	var e domain.Entity
	if err != nil {
		return e, err
	}
	return e, json.Unmarshal(raw, &e)
}
func (s *Store) saveTx(ctx context.Context, tx *sql.Tx, e domain.Entity, create bool) (domain.RevisionSummary, error) {
	versions, err := currentValidationVersionManifest()
	if err != nil {
		return domain.RevisionSummary{}, err
	}
	hash, blob, err := domain.BlobHash(e)
	if err != nil {
		return domain.RevisionSummary{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO entity_blobs(hash,json) VALUES(?,?)", hash, blob); err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = s.inject("blob"); err != nil {
		return domain.RevisionSummary{}, err
	}
	if create {
		_, err = tx.ExecContext(ctx, `INSERT INTO working_entities(id,kind,entity_key,schema_version,entity_version,blob_hash,status,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?)`, e.ID, e.Kind, e.Key, e.SchemaVersion, e.EntityVersion, hash, e.Status, e.CreatedAt.Format(time.RFC3339Nano), e.UpdatedAt.Format(time.RFC3339Nano))
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return domain.RevisionSummary{}, ErrDuplicateKey
			}
			return domain.RevisionSummary{}, err
		}
	} else {
		result, x := tx.ExecContext(ctx, `UPDATE working_entities SET entity_key=?,schema_version=?,entity_version=?,blob_hash=?,status=?,updated_at=? WHERE id=?`, e.Key, e.SchemaVersion, e.EntityVersion, hash, e.Status, e.UpdatedAt.Format(time.RFC3339Nano), e.ID)
		if x != nil {
			return domain.RevisionSummary{}, x
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return domain.RevisionSummary{}, ErrNotFound
		}
	}
	if err = s.inject("working"); err != nil {
		return domain.RevisionSummary{}, err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM entity_references WHERE source_entity_id=?", e.ID); err != nil {
		return domain.RevisionSummary{}, err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM entity_tags WHERE entity_id=?", e.ID); err != nil {
		return domain.RevisionSummary{}, err
	}
	refs, tags := domain.ExtractIndexes(e)
	for _, v := range refs {
		if _, err = tx.ExecContext(ctx, "INSERT INTO entity_references(source_entity_id,field_path,ordinal,expected_kind,target_entity_id) VALUES(?,?,?,?,?)", v.SourceID, v.FieldPath, v.Ordinal, v.ExpectedKind, v.TargetID); err != nil {
			return domain.RevisionSummary{}, err
		}
	}
	for _, v := range tags {
		if _, err = tx.ExecContext(ctx, "INSERT INTO entity_tags(entity_id,tag_id) VALUES(?,?)", v.EntityID, v.TagID); err != nil {
			return domain.RevisionSummary{}, err
		}
	}
	if err = s.inject("indexes"); err != nil {
		return domain.RevisionSummary{}, err
	}
	revision, err := s.writeRevision(ctx, tx)
	if err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = s.writeRevisionMetadata(ctx, tx, revision, versions, revisionMetadataFields{}); err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = s.inject("metadata"); err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = s.persistRevisionDerived(ctx, tx, revision.ID); err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = s.inject("derived"); err != nil {
		return domain.RevisionSummary{}, err
	}
	summary, err := s.persistLocalValidation(ctx, tx, e, revision, versions)
	if err != nil {
		return domain.RevisionSummary{}, err
	}
	revision.Validation = &summary
	return revision, nil
}

func (s *Store) inject(stage string) error {
	if s.failStage == nil {
		return nil
	}
	return s.failStage(stage)
}
func (s *Store) writeRevision(ctx context.Context, tx *sql.Tx) (domain.RevisionSummary, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id,entity_version,status,blob_hash FROM working_entities ORDER BY id")
	if err != nil {
		return domain.RevisionSummary{}, err
	}
	defer rows.Close()
	type m struct {
		id domain.ID
		v  int64
		st domain.EntityStatus
		h  string
	}
	all := []m{}
	in := strings.Builder{}
	for rows.Next() {
		var x m
		if err = rows.Scan(&x.id, &x.v, &x.st, &x.h); err != nil {
			return domain.RevisionSummary{}, err
		}
		all = append(all, x)
		fmt.Fprintf(&in, "%s:%d:%s:%s\n", x.id, x.v, x.st, x.h)
	}
	if err = rows.Err(); err != nil {
		return domain.RevisionSummary{}, err
	}
	sum := sha256.Sum256([]byte(in.String()))
	id, err := domain.NewID()
	if err != nil {
		return domain.RevisionSummary{}, err
	}
	var display int64
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(display_revision),0)+1 FROM config_revisions").Scan(&display); err != nil {
		return domain.RevisionSummary{}, err
	}
	now := s.now().UTC()
	h := fmt.Sprintf("%x", sum)
	if _, err = tx.ExecContext(ctx, "INSERT INTO config_revisions(id,display_revision,config_hash,created_at)VALUES(?,?,?,?)", id, display, h, now.Format(time.RFC3339Nano)); err != nil {
		return domain.RevisionSummary{}, err
	}
	for _, x := range all {
		if _, err = tx.ExecContext(ctx, "INSERT INTO revision_entities(revision_id,entity_id,entity_version,status,blob_hash)VALUES(?,?,?,?,?)", id, x.id, x.v, x.st, x.h); err != nil {
			return domain.RevisionSummary{}, err
		}
	}
	if err = s.inject("revision"); err != nil {
		return domain.RevisionSummary{}, err
	}
	return domain.RevisionSummary{ID: id, DisplayRevision: display, ConfigHash: h, CreatedAt: now}, nil
}
