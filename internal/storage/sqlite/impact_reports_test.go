package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/impact"
	impactreport "github.com/zouyi/eco-guardian/internal/graph/impact/report"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	versioningdiff "github.com/zouyi/eco-guardian/internal/versioning/diff"
)

func impactFixture(t *testing.T, store *Store, key string) (sharedjob.Record, domain.ID, impact.Report) {
	t.Helper()
	entity, base, err := store.Create(context.Background(), domain.KindTag, tagDraft("impact_base"))
	if err != nil {
		t.Fatal(err)
	}
	name, _ := json.Marshal("impact-target-" + key)
	_, target, err := store.Patch(context.Background(), domain.KindTag, entity.ID, entity.EntityVersion, domain.EntityPatch{"name": name})
	if err != nil {
		t.Fatal(err)
	}
	projectID := store.ProjectID()
	hashA, hashB, hashC := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	input := impact.Input{ProjectID: projectID, Base: impact.RevisionIdentity{RevisionID: base.ID, ConfigHash: base.ConfigHash, VersionManifestHash: hashA, GraphManifestHash: hashB}, Target: impact.RevisionIdentity{RevisionID: target.ID, ConfigHash: target.ConfigHash, VersionManifestHash: hashA, GraphManifestHash: hashC}, AnalysisContractVersion: impact.AnalysisContractVersion, Filters: impact.Filters{RelationshipKinds: []string{"explicit"}, Direction: impact.DirectionIncoming}, Limits: impact.Limits{MaxDepth: 3, MaxNodes: 500, DefaultPathsPerTarget: 1, ExpandedMaxPaths: 20}, Suspected: impact.SuspectedOptions{MaxSeeds: 20, MaxResults: 20, GraphMaxDepth: 2}}
	inputHash := strings.Repeat("d", 64)
	job, _, err := store.CreateOrGet(context.Background(), sharedjob.Request{ProjectID: projectID, Kind: "impact_analysis", RevisionID: target.ID, InputHash: inputHash, IdempotencyKey: "impact-" + key, RequestHash: strings.Repeat("e", 64)})
	if err != nil {
		t.Fatal(err)
	}
	stagingID, _, err := store.CreateOrGetStaging(context.Background(), job.ID, input, inputHash)
	if err != nil {
		t.Fatal(err)
	}
	reportID, _ := domain.NewID()
	record := impact.Report{ID: reportID, Input: input, InputHash: inputHash, Mode: impact.ReverseDependencyImpact, Changed: []impact.ChangedEntity{{EntityID: entity.ID, Kind: entity.Kind, ChangeKind: versioningdiff.Modify, FieldPaths: []string{"/name"}, QueryEligible: false, Ineligibility: "not_in_target_graph"}}, SuspectedState: impact.SuspectedDisabled, CreatedAt: time.Now().UTC()}
	record.ResultHash, err = impactreport.ResultHash(record)
	if err != nil {
		t.Fatal(err)
	}
	return job, stagingID, record
}

func TestImpactReportSealIsAtomicImmutableAndReplaySafe(t *testing.T) {
	store := newStore(t)
	_, stagingID, report := impactFixture(t, store, "seal")
	if err := store.SaveChanged(context.Background(), stagingID, report.Changed); err != nil {
		t.Fatal(err)
	}
	sealed, replay, err := store.Seal(context.Background(), report)
	if err != nil || replay || sealed.ID != report.ID {
		t.Fatalf("sealed=%#v replay=%v err=%v", sealed, replay, err)
	}
	loaded, err := store.GetImpactReport(context.Background(), report.ID)
	if err != nil || loaded.ResultHash != report.ResultHash || len(loaded.Changed) != 1 {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	if _, err = store.db.Exec(`UPDATE impact_reports SET result_hash=? WHERE id=?`, strings.Repeat("f", 64), report.ID); err == nil {
		t.Fatal("immutable report update succeeded")
	}
	if _, replay, err = store.Seal(context.Background(), report); err != nil || !replay {
		t.Fatalf("replay=%v err=%v", replay, err)
	}
}

func TestImpactExpansionIsInsertOnlyAndDoesNotChangeReportHash(t *testing.T) {
	store := newStore(t)
	_, _, report := impactFixture(t, store, "expand")
	if _, _, err := store.Seal(context.Background(), report); err != nil {
		t.Fatal(err)
	}
	expansionHash, err := impactreport.ExpansionHash(string(report.ID), "target", 20, report.Input)
	if err != nil {
		t.Fatal(err)
	}
	path := impact.Path{SourceNodeID: "source", TargetNodeID: "target", NodeIDs: []string{"source", "target"}, EdgeIDs: []string{"edge"}, HopCount: 1}
	replay, err := store.ExpandPaths(context.Background(), report.ID, expansionHash, path.TargetNodeID, []impact.Path{path})
	if err != nil || replay {
		t.Fatalf("replay=%v err=%v", replay, err)
	}
	replay, err = store.ExpandPaths(context.Background(), report.ID, expansionHash, path.TargetNodeID, []impact.Path{path})
	if err != nil || !replay {
		t.Fatalf("second replay=%v err=%v", replay, err)
	}
	emptyHash := strings.Repeat("d", 64)
	if replay, err = store.ExpandPaths(context.Background(), report.ID, emptyHash, "no-path-target", nil); err != nil || replay {
		t.Fatalf("empty expansion replay=%v err=%v", replay, err)
	}
	if replay, err = store.ExpandPaths(context.Background(), report.ID, emptyHash, "no-path-target", nil); err != nil || !replay {
		t.Fatalf("empty expansion second replay=%v err=%v", replay, err)
	}
	loaded, _ := store.GetImpactReport(context.Background(), report.ID)
	if loaded.ResultHash != report.ResultHash {
		t.Fatalf("result hash changed %s -> %s", report.ResultHash, loaded.ResultHash)
	}
}

func TestImpactSealDeduplicatesConcurrentWriters(t *testing.T) {
	store := newStore(t)
	_, _, report := impactFixture(t, store, "concurrent-seal")
	const writers = 8
	var wait sync.WaitGroup
	results := make(chan bool, writers)
	errorsFound := make(chan error, writers)
	for index := 0; index < writers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, replay, err := store.Seal(context.Background(), report)
			if err != nil {
				errorsFound <- err
				return
			}
			results <- replay
		}()
	}
	wait.Wait()
	close(results)
	close(errorsFound)
	if len(errorsFound) != 0 {
		t.Fatal(<-errorsFound)
	}
	created := 0
	for replay := range results {
		if !replay {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("created=%d", created)
	}
}

func TestImpactSealFaultsNeverExposePartialReport(t *testing.T) {
	for _, stage := range []string{"impact-before-seal", "impact-after-seal"} {
		t.Run(stage, func(t *testing.T) {
			store := newStore(t)
			_, _, report := impactFixture(t, store, stage)
			store.failStage = func(candidate string) error {
				if candidate == stage {
					return errors.New("injected")
				}
				return nil
			}
			if _, _, err := store.Seal(context.Background(), report); err == nil {
				t.Fatal("faulted seal succeeded")
			}
			if _, err := store.GetImpactReport(context.Background(), report.ID); !errors.Is(err, ErrImpactNotFound) {
				t.Fatalf("partial report visible: %v", err)
			}
		})
	}
}

func TestImpactSchemaHasStableIndexesAndMigrationLedger(t *testing.T) {
	store := newStore(t)
	for _, name := range []string{"impact_reports_history_lookup", "impact_reports_pair_lookup", "impact_changed_page_lookup", "impact_affected_page_lookup", "impact_paths_target_lookup"} {
		var found string
		if err := store.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&found); err != nil {
			t.Fatalf("missing index %s: %v", name, err)
		}
	}
	var count int
	if err := store.db.QueryRow(`SELECT count(*) FROM schema_migration_steps WHERE step_id='dependency-impact-v21' AND checksum IS NOT NULL`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("ledger count=%d err=%v", count, err)
	}
}
