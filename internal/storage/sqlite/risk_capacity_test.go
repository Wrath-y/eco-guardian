package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"testing"
	"time"

	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
	"github.com/zouyi/eco-guardian/internal/risk/orchestration"
)

// TestRiskCapacityAtPolicyLimit records the bounded cost of the public
// 500-scene/Metric contract. The report is sealed without dropping an item;
// the elapsed seal time is also the upper bound for this fixture's SQLite
// write-lock duration because SealRiskReport owns one short transaction.
func TestRiskCapacityAtPolicyLimit(t *testing.T) {
	store := newStore(t)
	report := riskFixture(t, store, "capacity-500")
	base := report.Items[0].Item
	items := make([]riskcontract.RiskItemV1, 500)
	for index := range items {
		item := base
		item.ID = fmt.Sprintf("metric-resource-capacity-%03d", index)
		item.Metric = cloneMetricComparison(base.Metric)
		item.Metric.MetricID = fmt.Sprintf("metric-resource-%03d", index)
		item.EvidenceHash = riskHash(fmt.Sprintf("capacity-evidence-%03d", index))
		items[index] = item
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	var err error
	report, err = orchestration.NewCalculationReport(report, items)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err = store.SealRiskReport(context.Background(), report); err != nil {
		t.Fatal(err)
	}
	sealDuration := time.Since(started)
	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - before.TotalAlloc
	if len(report.Items) != 500 {
		t.Fatalf("sealed items=%d, want 500", len(report.Items))
	}
	stored, err := store.GetRiskReport(context.Background(), report.ID)
	if err != nil || len(stored.Items) != 500 || stored.ReportHash != report.ReportHash || stored.CalculationHash != report.CalculationHash {
		t.Fatalf("stored items=%d hash=%s err=%v", len(stored.Items), stored.ReportHash, err)
	}
	if len(payload) > 8<<20 {
		t.Fatalf("risk payload=%d bytes, exceeds HTTP result bound", len(payload))
	}
	if allocated > 128<<20 {
		t.Fatalf("risk capacity allocated=%d bytes, want <=128MiB", allocated)
	}
	if sealDuration > 2*time.Second {
		t.Fatalf("risk SQLite seal/write-lock=%s, want <=2s", sealDuration)
	}
	cancelJob, replayed, err := store.CreateOrGet(context.Background(), sharedjob.Request{ProjectID: store.projectID, Kind: orchestration.RiskReviewJobKind, RevisionID: report.Candidate.RevisionID, InputHash: riskHash("capacity-cancel-input"), IdempotencyKey: "capacity-cancel", RequestHash: riskHash("capacity-cancel-request")})
	if err != nil || replayed {
		t.Fatalf("create cancellation fixture replayed=%v err=%v", replayed, err)
	}
	cancelStarted := time.Now()
	canceled, replayedCancel, err := store.RequestCancellation(context.Background(), cancelJob.ID)
	cancelDuration := time.Since(cancelStarted)
	if err != nil || replayedCancel || canceled.CancelGeneration != 1 || cancelDuration > 250*time.Millisecond {
		t.Fatalf("cancel replayed=%v generation=%d latency=%s err=%v", replayedCancel, canceled.CancelGeneration, cancelDuration, err)
	}
	t.Logf("risk-capacity items=500 payload_bytes=%d allocated_bytes=%d sqlite_seal_lock=%s cancellation_intent=%s", len(payload), allocated, sealDuration, cancelDuration)
}

func cloneMetricComparison(source *riskcontract.MetricComparisonEvidence) *riskcontract.MetricComparisonEvidence {
	if source == nil {
		return nil
	}
	copy := *source
	copy.Assumptions = append([]string(nil), source.Assumptions...)
	return &copy
}
