package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	root "github.com/zouyi/eco-guardian"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
	"github.com/zouyi/eco-guardian/internal/risk/orchestration"
	"github.com/zouyi/eco-guardian/internal/risk/threshold"
	_ "modernc.org/sqlite"
)

type sqliteRiskSource struct {
	value orchestration.MaterializedInput
}

func (s sqliteRiskSource) MaterializeRiskInput(context.Context) (orchestration.MaterializedInput, error) {
	return s.value, nil
}

type sqliteRiskClock struct{ value time.Time }

func (c sqliteRiskClock) Now() time.Time { return c.value }

func riskHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func riskFixture(t *testing.T, store *Store, key string) orchestration.Report {
	t.Helper()
	ctx := context.Background()
	_, revision, err := store.Create(ctx, domain.KindTag, tagDraft("risk_"+riskHash(key)[:12]))
	if err != nil {
		t.Fatal(err)
	}
	var configHash, manifestHash string
	if err = store.db.QueryRow(`SELECT r.config_hash,m.version_manifest_hash FROM config_revisions r JOIN revision_metadata m ON m.revision_id=r.id WHERE r.id=?`, revision.ID).Scan(&configHash, &manifestHash); err != nil {
		t.Fatal(err)
	}

	var policyID domain.ID
	var policyVersion int64
	var policyHash string
	if err = store.db.QueryRow(`SELECT id,display_version,canonical_hash FROM release_policies ORDER BY display_version LIMIT 1`).Scan(&policyID, &policyVersion, &policyHash); err != nil {
		t.Fatal(err)
	}
	starter := threshold.StarterFixtureV1()
	thresholdID := mustID(t)
	thresholdVersion, replayed, err := store.CreateEnabled(ctx, threshold.CreateRequest{ProjectID: store.projectID, ProposedID: thresholdID, Origin: threshold.OriginStarter, Body: starter.Body, BodyHash: starter.BodyHash, CreatedBy: "tester", CreatedAt: time.Now().UTC(), IdempotencyKey: "risk-threshold-" + key, RequestHash: riskHash("threshold-request-" + key)})
	if err != nil || replayed {
		t.Fatalf("threshold replayed=%v err=%v", replayed, err)
	}

	validationID := mustID(t)
	validationHash := riskHash("validation-result-" + key)
	if _, err = store.db.Exec(`INSERT INTO validation_runs(id,source_kind,source_revision_id,source_input_hash,scope,version_manifest_hash,version_manifest,status,error_count,block_count,warning_count,info_count,result_hash,created_at) VALUES(?,'revision',?,?,'FULL',?,'{}','completed',0,0,0,0,?,?)`, validationID, revision.ID, configHash, riskHash("validation-manifest-"+key), validationHash, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	var scenarioID domain.ID
	if err = store.db.QueryRow(`SELECT id FROM scenario_definitions ORDER BY id LIMIT 1`).Scan(&scenarioID); err != nil {
		t.Fatal(err)
	}
	runID, simulationJobID := mustID(t), mustID(t)
	inputHash := riskHash("simulation-input-" + key)
	fingerprintHash := riskHash("simulation-fingerprint-" + key)
	resultHash := riskHash("simulation-result-" + key)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = store.db.Exec(`INSERT INTO jobs(id,project_uuid,kind,revision_id,input_hash,idempotency_key,request_hash,status,result_type,result_id,result_url,created_at,updated_at) VALUES(?,?,?,?,?,?,?,'succeeded','simulation_run',?,?,?,?)`, simulationJobID, store.projectID, "simulation", revision.ID, inputHash, "simulation-"+key, riskHash("simulation-request-"+key), runID, "/api/v1/simulation-runs/"+string(runID), now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`INSERT INTO simulation_runs(id,job_id,project_uuid,revision_id,scenario_definition_id,input_hash,fingerprint_hash,result_hash,canonical_result,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, runID, simulationJobID, store.projectID, revision.ID, scenarioID, inputHash, fingerprintHash, resultHash, `{}`, now); err != nil {
		t.Fatal(err)
	}

	riskInputHash := riskHash("risk-input-" + key)
	job, replayed, err := store.CreateOrGet(ctx, sharedjob.Request{ProjectID: store.projectID, Kind: orchestration.RiskReviewJobKind, RevisionID: revision.ID, InputHash: riskInputHash, IdempotencyKey: "risk-job-" + key, RequestHash: riskHash("risk-request-" + key)})
	if err != nil || replayed {
		t.Fatalf("risk job replayed=%v err=%v", replayed, err)
	}
	if _, changed, transitionErr := store.Transition(ctx, job.ID, sharedjob.Queued, sharedjob.Running, nil, 0); transitionErr != nil || !changed {
		t.Fatalf("start risk job changed=%v err=%v", changed, transitionErr)
	}
	evidenceHash, err := riskcontract.ExplanationEvidenceManifestHash(nil)
	if err != nil {
		t.Fatal(err)
	}
	severity := riskcontract.Block
	metricValue, metricLow, metricHigh := "1.3", "1.2", "1.4"
	metricSubject := mustID(t)
	metricEvidence := &riskcontract.MetricComparisonEvidence{SceneID: "scene", SceneVersion: "v1", MetricID: "metric-resource", MetricVersion: "v1", Unit: "ratio", Direction: riskcontract.TargetRange, TargetRange: &riskcontract.TargetRangeValue{Lower: "0.8", Upper: "1.2", Bounds: "inclusive"}, Subject: riskcontract.Subject{Kind: riskcontract.SingletonSubject, EntityKind: "character", StableID: metricSubject, Members: []domain.ID{metricSubject}}, Candidate: riskcontract.ReportMetricValue{Status: riskcontract.MetricAvailable, Value: &metricValue, ConfidenceLow: &metricLow, ConfidenceHigh: &metricHigh, RunID: runID, ResultHash: resultHash}, Assumptions: []string{"fixed fixture"}}
	item := riskcontract.RiskItemV1{ID: "metric/scene/metric-resource", Kind: riskcontract.MetricRiskItem, Role: riskcontract.Required, Status: riskcontract.Comparable, Severity: &severity, Rule: riskcontract.Identity{ID: "risk-comparison-target_range", Version: "v1", Hash: riskHash("rule-v1")}, Override: riskcontract.NumericOverrideEligible, EvidenceHash: riskHash("evidence-" + key), Metric: metricEvidence}
	report := orchestration.Report{
		ID:              mustID(t),
		JobID:           job.ID,
		ProjectID:       store.projectID,
		InputHash:       riskInputHash,
		EvidenceHash:    evidenceHash,
		Candidate:       orchestration.RevisionRef{RevisionID: revision.ID, ConfigHash: configHash, ManifestHash: manifestHash},
		Baseline:        orchestration.BaselineRef{Kind: riskcontract.NoBaseline},
		Policy:          riskcontract.Identity{ID: string(policyID), Version: fmt.Sprint(policyVersion), Hash: policyHash},
		Threshold:       riskcontract.Identity{ID: string(thresholdVersion.ID), Version: fmt.Sprint(thresholdVersion.DisplayVersion), Hash: thresholdVersion.BodyHash},
		Validation:      riskcontract.Identity{ID: string(validationID), Version: "v1", Hash: validationHash},
		Implementations: []riskcontract.Identity{{ID: "risk-comparison", Version: "v1", Hash: riskHash("implementation-v1")}},
		SimulationRuns:  []orchestration.SimulationRef{{RunID: runID, InputHash: inputHash, FingerprintHash: fingerprintHash, ResultHash: resultHash}},
		CreatedAt:       time.Now().UTC(),
	}
	report, err = orchestration.NewCalculationReport(report, []riskcontract.RiskItemV1{item})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func riskInputForStoredFixture(t *testing.T, store *Store, report orchestration.Report) riskcontract.RiskInputV1 {
	t.Helper()
	record, err := store.GetRevisionRecord(context.Background(), report.Candidate.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	revision := riskcontract.RevisionIdentity{RevisionID: report.Candidate.RevisionID, ConfigHash: report.Candidate.ConfigHash, Manifest: record.Metadata.Manifest, ManifestHash: report.Candidate.ManifestHash}
	participant := mustID(t)
	value, low, high := "1", "0.9", "1.1"
	runRef := report.SimulationRuns[0]
	run := riskcontract.RunEvidence{RunID: runRef.RunID, Revision: revision, SceneID: "scene", SceneVersion: "v1", Participants: []domain.ID{participant}, SampleCount: 1000, Seed: 11, InputHash: runRef.InputHash, FingerprintHash: runRef.FingerprintHash, ResultHash: runRef.ResultHash, Status: "succeeded", Reproducible: true, Implementations: []riskcontract.Identity{{ID: "engine", Version: "v1", Hash: riskHash("engine")}}, Metrics: []riskcontract.MetricEvidence{{MetricID: "metric-resource", MetricVersion: "v1", Status: riskcontract.MetricAvailable, Unit: "ratio", Direction: riskcontract.TargetRange, TargetRange: &riskcontract.TargetRangeValue{Lower: "0.8", Upper: "1.2", Bounds: "inclusive"}, Value: &value, ConfidenceLow: &low, ConfidenceHigh: &high, SampleCount: 1000, CanonicalHash: riskHash("metric")}}}
	return riskcontract.RiskInputV1{SchemaVersion: "v1", ProjectID: store.projectID, Candidate: revision, Baseline: riskcontract.Baseline{Kind: riskcontract.NoBaseline}, Policy: report.Policy, Threshold: report.Threshold, Validation: report.Validation, PolicyRequirements: []riskcontract.PolicyRequirement{{SceneID: "scene", SceneVersion: "v1", MetricID: "metric-resource", MetricVersion: "v1", Role: riskcontract.Required}}, Implementations: []riskcontract.Identity{{ID: "comparison", Version: "v1", Hash: riskHash("comparison")}, {ID: "cohort", Version: "v1", Hash: riskHash("cohort")}, {ID: "structural", Version: "v1", Hash: riskHash("structural")}, {ID: "report", Version: "v1", Hash: riskHash("report")}}, Subjects: []riskcontract.Subject{{Kind: riskcontract.SingletonSubject, EntityKind: "character", StableID: participant, Members: []domain.ID{participant}}}, CandidateRuns: []riskcontract.RunEvidence{run}, ComparisonVersion: "v1", CohortVersion: "v1", StructuralVersion: "v1", ReportSchemaVersion: "v1"}
}

func TestRiskMigrationSeedsOneInactiveStarterAndReferenceOnlySchema(t *testing.T) {
	store := newStore(t)
	starter := threshold.StarterFixtureV1()
	var count, enabled int
	var bodyHash string
	if err := store.db.QueryRow(`SELECT count(*),COALESCE(sum(enabled),0),max(body_hash) FROM threshold_versions WHERE origin='starter_template'`).Scan(&count, &enabled, &bodyHash); err != nil || count != 1 || enabled != 0 || bodyHash != starter.BodyHash {
		t.Fatalf("starter count=%d enabled=%d hash=%s err=%v", count, enabled, bodyHash, err)
	}
	for _, table := range []string{"threshold_versions", "risk_reviews", "risk_items"} {
		var name string
		if err := store.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil {
			t.Fatalf("missing table %s: %v", table, err)
		}
	}
	rows, err := store.db.Query(`PRAGMA table_info(risk_reviews)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err = rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"version_manifest", "sample", "event", "graph_path_body", "release_configuration"} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("risk_reviews copies forbidden fact in column %s", name)
			}
		}
	}
}

func TestThresholdSQLiteRepositoryReplayHistoryAndImmutability(t *testing.T) {
	store := newStore(t)
	starter := threshold.StarterFixtureV1()
	request := threshold.CreateRequest{ProjectID: store.projectID, ProposedID: mustID(t), Origin: threshold.OriginStarter, Body: starter.Body, BodyHash: starter.BodyHash, CreatedBy: "tester", CreatedAt: time.Now().UTC(), IdempotencyKey: "enable-starter", RequestHash: riskHash("enable-starter")}
	first, replayed, err := store.CreateEnabled(context.Background(), request)
	if err != nil || replayed || first.DisplayVersion != 1 || !first.Enabled {
		t.Fatalf("first=%#v replayed=%v err=%v", first, replayed, err)
	}
	second, replayed, err := store.CreateEnabled(context.Background(), request)
	if err != nil || !replayed || second.ID != first.ID {
		t.Fatalf("second=%#v replayed=%v err=%v", second, replayed, err)
	}
	request.RequestHash = riskHash("changed")
	if _, _, err = store.CreateEnabled(context.Background(), request); !errors.Is(err, threshold.ErrIdempotencyConflict) {
		t.Fatalf("changed replay err=%v", err)
	}
	identity := riskcontract.Identity{ID: string(first.ID), Version: "1", Hash: first.BodyHash}
	if exact, found, getErr := store.GetExactEnabled(context.Background(), store.projectID, identity); getErr != nil || !found || exact.ID != first.ID || exact.Body.Entries[0].Relative.Warning != "0.10" || exact.Body.Entries[0].Relative.Block != "0.25" {
		t.Fatalf("exact=%#v found=%v err=%v", exact, found, getErr)
	}
	if _, err = store.db.Exec(`UPDATE threshold_versions SET enabled=0 WHERE id=?`, first.ID); err == nil {
		t.Fatal("threshold update unexpectedly succeeded")
	}
	if _, err = store.db.Exec(`DELETE FROM threshold_versions WHERE id=?`, first.ID); err == nil {
		t.Fatal("threshold delete unexpectedly succeeded")
	}
}

func TestSealRiskReportIsAtomicImmutableAndBoundedReadable(t *testing.T) {
	store := newStore(t)
	report := riskFixture(t, store, "seal")
	if err := store.SealRiskReport(context.Background(), report); err != nil {
		t.Fatal(err)
	}
	job, err := store.GetJob(context.Background(), report.JobID)
	if err != nil || job.Status != sharedjob.Succeeded || job.Result == nil || job.Result.ID != report.ID || job.Result.Type != "risk_review" {
		t.Fatalf("job=%#v err=%v", job, err)
	}
	stored, err := store.GetRiskReport(context.Background(), report.ID)
	if err != nil || stored.ReportHash != report.ReportHash || stored.CalculationHash != report.CalculationHash || len(stored.Items) != 1 {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}
	history, err := store.ListRiskReports(context.Background(), orchestration.HistoryQuery{CandidateRevisionID: report.Candidate.RevisionID, Limit: 1})
	if err != nil || len(history) != 1 || history[0].ID != report.ID {
		t.Fatalf("history=%#v err=%v", history, err)
	}
	if err = store.SealRiskReport(context.Background(), report); !errors.Is(err, ErrRiskReportSeal) {
		t.Fatalf("duplicate seal err=%v", err)
	}
	if _, err = store.db.Exec(`UPDATE risk_items SET severity='INFO' WHERE report_id=?`, report.ID); err == nil {
		t.Fatal("risk item update unexpectedly succeeded")
	}
	if _, err = store.db.Exec(`DELETE FROM risk_reviews WHERE id=?`, report.ID); err == nil {
		t.Fatal("risk report delete unexpectedly succeeded")
	}
	var reviewCount, itemCount int
	_ = store.db.QueryRow(`SELECT count(*) FROM risk_reviews WHERE job_id=?`, report.JobID).Scan(&reviewCount)
	_ = store.db.QueryRow(`SELECT count(*) FROM risk_items WHERE report_id=?`, report.ID).Scan(&itemCount)
	if reviewCount != 1 || itemCount != 1 {
		t.Fatalf("reviewCount=%d itemCount=%d", reviewCount, itemCount)
	}
}

func TestRiskSealFaultsRollbackEveryFact(t *testing.T) {
	for _, stage := range []string{"risk-seal-before-review", "risk-seal-after-review", "risk-seal-before-item", "risk-seal-after-item", "risk-seal-before-job-success", "risk-seal-after-job-success"} {
		t.Run(stage, func(t *testing.T) {
			store := newStore(t)
			report := riskFixture(t, store, stage)
			store.failStage = func(at string) error {
				if at == stage {
					return errors.New("injected")
				}
				return nil
			}
			if err := store.SealRiskReport(context.Background(), report); err == nil {
				t.Fatal("faulted seal unexpectedly succeeded")
			}
			var reviews, items int
			_ = store.db.QueryRow(`SELECT count(*) FROM risk_reviews WHERE job_id=?`, report.JobID).Scan(&reviews)
			_ = store.db.QueryRow(`SELECT count(*) FROM risk_items WHERE report_id=?`, report.ID).Scan(&items)
			job, getErr := store.GetJob(context.Background(), report.JobID)
			if reviews != 0 || items != 0 || getErr != nil || job.Status != sharedjob.Running || job.Result != nil {
				t.Fatalf("reviews=%d items=%d job=%#v err=%v", reviews, items, job, getErr)
			}
		})
	}
}

func TestDecisionReportReferencesEligibleSourceWithoutCopyingItems(t *testing.T) {
	store := newStore(t)
	source := riskFixture(t, store, "decision")
	if err := store.SealRiskReport(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	inputHash := riskHash("decision-input")
	job, replayed, err := store.CreateOrGet(context.Background(), sharedjob.Request{ProjectID: store.projectID, Kind: orchestration.RiskReviewJobKind, RevisionID: source.Candidate.RevisionID, InputHash: inputHash, IdempotencyKey: "decision-job", RequestHash: riskHash("decision-request")})
	if err != nil || replayed {
		t.Fatalf("job replayed=%v err=%v", replayed, err)
	}
	if _, changed, transitionErr := store.Transition(context.Background(), job.ID, sharedjob.Queued, sharedjob.Running, nil, 0); transitionErr != nil || !changed {
		t.Fatalf("start changed=%v err=%v", changed, transitionErr)
	}
	decision, err := orchestration.NewDecisionReport(orchestration.Report{ID: mustID(t), JobID: job.ID, ProjectID: store.projectID, InputHash: inputHash, CreatedAt: time.Now().UTC()}, source, []string{source.Items[0].Item.ID}, "accepted for this release after review")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SealRiskReport(context.Background(), decision); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetRiskReport(context.Background(), decision.ID)
	if err != nil || stored.SourceReportID != source.ID || len(stored.Items) != 0 || stored.Envelope.DecisionReason == "" || stored.CalculationHash != source.CalculationHash {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}
	var copiedItems int
	if err = store.db.QueryRow(`SELECT count(*) FROM risk_items WHERE report_id=?`, decision.ID).Scan(&copiedItems); err != nil || copiedItems != 0 {
		t.Fatalf("copied items=%d err=%v", copiedItems, err)
	}
}

func TestRiskSealRejectsMismatchedReferencesAndHistoryLimit(t *testing.T) {
	store := newStore(t)
	report := riskFixture(t, store, "foreign")
	foreignRunID := mustID(t)
	report.SimulationRuns[0].RunID = foreignRunID
	item := report.Items[0].Item
	item.Metric.Candidate.RunID = foreignRunID
	report, err := orchestration.NewCalculationReport(report, []riskcontract.RiskItemV1{item})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Valid() {
		t.Fatal("fixture should remain structurally valid")
	}
	if err := store.SealRiskReport(context.Background(), report); !errors.Is(err, ErrRiskReportSeal) {
		t.Fatalf("foreign simulation err=%v", err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT count(*) FROM risk_reviews WHERE job_id=?`, report.JobID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("reports=%d err=%v", count, err)
	}
	if _, err := store.ListRiskReports(context.Background(), orchestration.HistoryQuery{Limit: 101}); !errors.Is(err, ErrRiskReportInvalid) {
		t.Fatalf("unbounded history err=%v", err)
	}
}

func TestSQLiteRiskAdmissionPersistsExactMaterializationAndConflictsOnEvidenceChange(t *testing.T) {
	store := newStore(t)
	fixture := riskFixture(t, store, "materialization")
	input := riskInputForStoredFixture(t, store, fixture)
	clock := sqliteRiskClock{time.Now().UTC()}
	command := orchestration.AdmissionCommand{ProjectID: store.projectID, IdempotencyKey: "sqlite-risk-admission"}
	first, err := orchestration.AdmitRiskReview(context.Background(), sqliteRiskSource{orchestration.MaterializedInput{Input: input}}, store, store, clock, command)
	if err != nil || first.Replayed || !first.Materialization.Valid() {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	stored, err := store.GetRiskJobMaterialization(context.Background(), first.Job.ID)
	if err != nil || stored.InputHash != first.Job.InputHash || stored.RequestHash != first.Job.RequestHash {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}
	second, err := orchestration.AdmitRiskReview(context.Background(), sqliteRiskSource{orchestration.MaterializedInput{Input: input}}, store, store, clock, command)
	if err != nil || !second.Replayed || second.Job.ID != first.Job.ID {
		t.Fatalf("second=%#v err=%v", second, err)
	}
	impact := riskcontract.ImpactEvidenceRef{ReportID: "impact-report", EvidenceID: "path-1", RevisionPairHash: riskHash("pair"), SnapshotHash: riskHash("snapshot"), Classification: "deterministic", Freshness: "fresh"}
	if _, err = orchestration.AdmitRiskReview(context.Background(), sqliteRiskSource{orchestration.MaterializedInput{Input: input, ImpactEvidence: []riskcontract.ImpactEvidenceRef{impact}}}, store, store, clock, command); !errors.Is(err, ErrJobIdempotencyConflict) {
		t.Fatalf("evidence conflict err=%v", err)
	}
	if _, err = store.db.Exec(`UPDATE risk_job_materializations SET evidence_hash=? WHERE job_id=?`, riskHash("changed"), first.Job.ID); err == nil {
		t.Fatal("materialization update unexpectedly succeeded")
	}
	recoverable, err := store.ListRecoverableRiskJobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, job := range recoverable {
		found = found || job.ID == first.Job.ID
	}
	if !found {
		t.Fatal("admitted risk job missing from recovery scan")
	}
}

func TestRiskFailureAndSealedReportReconciliationAreAtomic(t *testing.T) {
	t.Run("failure", func(t *testing.T) {
		store := newStore(t)
		report := riskFixture(t, store, "failure")
		failed, replayed, err := store.FailRiskJob(context.Background(), report.JobID, 0, "RISK_CALCULATION_FAILED", "metric comparison failed")
		if err != nil || replayed || failed.Status != sharedjob.Failed {
			t.Fatalf("failed=%#v replayed=%v err=%v", failed, replayed, err)
		}
		if _, replayed, err = store.FailRiskJob(context.Background(), report.JobID, 0, "RISK_CALCULATION_FAILED", "metric comparison failed"); err != nil || !replayed {
			t.Fatalf("failure replayed=%v err=%v", replayed, err)
		}
		events, err := store.ListEvents(context.Background(), report.JobID, 0)
		if err != nil || len(events) != 1 || events[0].SafeError != "RISK_CALCULATION_FAILED: metric comparison failed" {
			t.Fatalf("events=%#v err=%v", events, err)
		}
	})
	t.Run("reconcile", func(t *testing.T) {
		store := newStore(t)
		report := riskFixture(t, store, "reconcile")
		tx, err := store.db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = insertRiskReport(context.Background(), tx, report); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		item := report.Items[0]
		canonical, err := domain.CanonicalJSON(item.Item)
		if err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if _, err = tx.Exec(`INSERT INTO risk_items(report_id,ordinal,item_id,policy_role,comparison_status,severity,rule_id,rule_version,rule_hash,override_classification,evidence_hash,item_hash,canonical_item) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, report.ID, item.Item.Ordinal, item.Item.ID, item.Item.Role, item.Item.Status, string(*item.Item.Severity), item.Item.Rule.ID, item.Item.Rule.Version, item.Item.Rule.Hash, item.Item.Override, item.Item.EvidenceHash, item.ItemHash, string(canonical)); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		job, replayed, err := store.ReconcileSealedRiskReport(context.Background(), report.JobID, report.InputHash, report.CalculationHash)
		if err != nil || replayed || job.Status != sharedjob.Succeeded || job.Result == nil || job.Result.ID != report.ID {
			t.Fatalf("job=%#v replayed=%v err=%v", job, replayed, err)
		}
		if _, replayed, err = store.ReconcileSealedRiskReport(context.Background(), report.JobID, report.InputHash, report.CalculationHash); err != nil || !replayed {
			t.Fatalf("reconcile replayed=%v err=%v", replayed, err)
		}
	})
}

func TestRiskReportHashCorruptionIsDetectedAndHistoricalProjectionSurvivesMissingImplementation(t *testing.T) {
	store := newStore(t)
	report := riskFixture(t, store, "corrupt")
	if err := store.SealRiskReport(context.Background(), report); err != nil {
		t.Fatal(err)
	}
	projection := orchestration.ProjectReport(report, orchestration.CurrentContext{Policy: report.Policy, Threshold: report.Threshold, SimulationResults: map[domain.ID]string{report.SimulationRuns[0].RunID: report.SimulationRuns[0].ResultHash}})
	if projection.Freshness != orchestration.Fresh || projection.Compatibility != orchestration.MissingImplement || len(projection.Reasons) != 1 {
		t.Fatalf("projection=%#v", projection)
	}
	if _, err := store.GetRiskReport(context.Background(), report.ID); err != nil {
		t.Fatalf("missing implementation made history unreadable: %v", err)
	}
	if _, err := store.db.Exec(`DROP TRIGGER risk_items_immutable_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE risk_items SET item_hash=? WHERE report_id=?`, riskHash("corrupt"), report.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetRiskReport(context.Background(), report.ID); !errors.Is(err, ErrRiskReportInvalid) {
		t.Fatalf("corrupt report err=%v", err)
	}
}

func TestConcurrentRiskReportReadsAndProjectReopen(t *testing.T) {
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	store, _, err := Create(context.Background(), dir, registry)
	if err != nil {
		t.Fatal(err)
	}
	report := riskFixture(t, store, "reopen")
	if err = store.SealRiskReport(context.Background(), report); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	errs := make(chan error, 24)
	for range 24 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			stored, readErr := store.GetRiskReport(context.Background(), report.ID)
			if readErr != nil || stored.ReportHash != report.ReportHash {
				errs <- fmt.Errorf("hash=%s err=%v", stored.ReportHash, readErr)
			}
		}()
	}
	wait.Wait()
	close(errs)
	for readErr := range errs {
		t.Fatal(readErr)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, _, err := Open(dir, registry)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	stored, err := reopened.GetRiskReport(context.Background(), report.ID)
	if err != nil || stored.ReportHash != report.ReportHash {
		t.Fatalf("reopened hash=%s err=%v", stored.ReportHash, err)
	}
	var starters int
	if err = reopened.db.QueryRow(`SELECT count(*) FROM threshold_versions WHERE origin='starter_template'`).Scan(&starters); err != nil || starters != 1 {
		t.Fatalf("starters=%d err=%v", starters, err)
	}
}

func TestV15RiskMigrationRequiresBackupBeforeAnySchemaWrite(t *testing.T) {
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, databaseName))
	if err != nil {
		t.Fatal(err)
	}
	initial, err := root.Assets.ReadFile("migrations/0001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(initial)); err != nil {
		t.Fatal(err)
	}
	projectID := mustID(t)
	if _, err = db.Exec(`INSERT INTO project_meta(id,db_schema_version,created_at) VALUES(?,?,?)`, projectID, 1, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	steps := []func(context.Context, *sql.Tx) error{applyMigrationV2, applyMigrationV3, applyMigrationV4, applyMigrationV5, applyMigrationV6, applyMigrationV7, applyMigrationV8, applyMigrationV9, applyMigrationV10, applyMigrationV11, applyMigrationV12, applyMigrationV13, applyMigrationV14, applyMigrationV15}
	for _, step := range steps {
		if err = step(context.Background(), tx); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO config_revisions(id,display_revision,config_hash,created_at) VALUES(?,1,?,?)`, mustID(t), riskHash("existing-config"), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err = Open(dir, registry); !errors.Is(err, ErrMigrationBackupRequired) {
		t.Fatalf("open without backup err=%v", err)
	}
	raw, err := sql.Open("sqlite", filepath.Join(dir, databaseName))
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err = raw.QueryRow(`SELECT db_schema_version FROM project_meta`).Scan(&version); err != nil || version != 15 {
		t.Fatalf("version=%d err=%v", version, err)
	}
	var tableCount int
	if err = raw.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='risk_reviews'`).Scan(&tableCount); err != nil || tableCount != 0 {
		t.Fatalf("risk table count=%d err=%v", tableCount, err)
	}
	_ = raw.Close()
	backup := &fakeMigrationBackup{evidence: BackupEvidence{Online: true, IntegrityChecked: true, Checksum: strings.Repeat("a", 64)}}
	upgraded, _, err := OpenWithMigrationBackup(context.Background(), dir, registry, backup)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if backup.calls != 1 {
		t.Fatalf("backup calls=%d", backup.calls)
	}
}
