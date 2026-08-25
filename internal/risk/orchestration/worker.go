package orchestration

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/risk/contract"
)

const maxRiskUnits = 512

var (
	ErrRiskCanceled            = errors.New("risk job canceled")
	ErrRiskRecoveryMismatch    = errors.New("risk recovery identity mismatch")
	ErrRiskRecoveryUnavailable = errors.New("risk recovery implementation unavailable")
)

type CalculationPlan struct {
	ComparisonKeys []string
	StructuralKeys []string
}

func (p CalculationPlan) Normalize() (CalculationPlan, error) {
	copy := CalculationPlan{ComparisonKeys: append([]string(nil), p.ComparisonKeys...), StructuralKeys: append([]string(nil), p.StructuralKeys...)}
	if len(copy.ComparisonKeys)+len(copy.StructuralKeys) == 0 || len(copy.ComparisonKeys) > maxRiskUnits || len(copy.StructuralKeys) > maxRiskUnits {
		return CalculationPlan{}, errors.New("risk calculation plan is invalid")
	}
	for _, keys := range [][]string{copy.ComparisonKeys, copy.StructuralKeys} {
		for _, key := range keys {
			if strings.TrimSpace(key) == "" {
				return CalculationPlan{}, errors.New("risk calculation unit key is invalid")
			}
		}
	}
	sort.Strings(copy.ComparisonKeys)
	sort.Strings(copy.StructuralKeys)
	if duplicateString(copy.ComparisonKeys) || duplicateString(copy.StructuralKeys) {
		return CalculationPlan{}, errors.New("risk calculation unit is duplicated")
	}
	return copy, nil
}

type CalculationEngine interface {
	Plan(context.Context, contract.RiskInputV1) (CalculationPlan, error)
	Compare(context.Context, contract.RiskInputV1, string) ([]contract.RiskItemV1, error)
	CheckStructure(context.Context, contract.RiskInputV1, string) ([]contract.RiskItemV1, error)
}

type FailureStore interface {
	FailRiskJob(context.Context, domain.ID, int64, string, string) (sharedjob.Record, bool, error)
}

type Worker struct {
	Jobs             JobStore
	Events           EventStore
	Materializations MaterializationStore
	Reports          ReportRepository
	Failures         FailureStore
	Engine           CalculationEngine
	Clock            Clock
	IDs              IDGenerator
}

func (w Worker) Run(ctx context.Context, jobID domain.ID) (sharedjob.Record, error) {
	if w.Jobs == nil || w.Events == nil || w.Materializations == nil || w.Reports == nil || w.Engine == nil || w.Clock == nil || w.IDs == nil || !jobID.Valid() {
		return sharedjob.Record{}, errors.New("risk worker is invalid")
	}
	job, err := w.Jobs.GetJob(ctx, jobID)
	if err != nil || string(job.Kind) != RiskReviewJobKind {
		return sharedjob.Record{}, errors.New("risk job is unavailable")
	}
	if job.Status == sharedjob.Succeeded {
		return job, w.ensureSucceededEvent(ctx, job)
	}
	if job.Status == sharedjob.Failed || job.Status == sharedjob.Canceled {
		return job, errors.New("risk job is terminal")
	}
	if canceled, cancelErr := w.cancelIfRequested(ctx, job); canceled {
		return cancelErr, ErrRiskCanceled
	}
	if job.Status == sharedjob.Queued {
		job, _, err = w.Jobs.Transition(ctx, job.ID, sharedjob.Queued, sharedjob.Running, nil, job.CancelGeneration)
		if err != nil {
			return sharedjob.Record{}, err
		}
	}
	materialization, err := w.Materializations.GetRiskJobMaterialization(ctx, job.ID)
	if err != nil || !materialization.Valid() || materialization.InputHash != job.InputHash || materialization.RequestHash != job.RequestHash || materialization.CancelGeneration != job.CancelGeneration {
		return w.fail(ctx, job, "RECOVERY_MISMATCH", "captured risk input is missing or invalid")
	}
	events, err := newRiskEventCursor(ctx, w.Events, job.ID)
	if err != nil {
		return sharedjob.Record{}, err
	}
	if err = events.append(ctx, w.Clock, "MATERIALIZED", 10, "", nil); err != nil {
		return w.fail(ctx, job, "STORAGE_FAILURE", "risk progress could not be recorded")
	}
	plan, err := w.Engine.Plan(ctx, materialization.Input)
	if err != nil {
		return w.fail(ctx, job, "RISK_RULE_CONTRACT_INVALID", "risk calculation plan is incompatible")
	}
	plan, err = plan.Normalize()
	if err != nil {
		return w.fail(ctx, job, "RISK_RULE_CONTRACT_INVALID", "risk calculation plan is invalid")
	}
	items := []contract.RiskItemV1{}
	for index, key := range plan.ComparisonKeys {
		if job, err = w.requireUncanceled(ctx, job); err != nil {
			return job, err
		}
		unitItems, unitErr := w.Engine.Compare(ctx, materialization.Input, key)
		if unitErr != nil {
			return w.fail(ctx, job, "RISK_RULE_CONTRACT_INVALID", "metric comparison failed")
		}
		items = append(items, unitItems...)
		progress := unitProgress(10, 60, index+1, len(plan.ComparisonKeys))
		if err = events.append(ctx, w.Clock, "COMPARING", progress, "", nil); err != nil {
			return w.fail(ctx, job, "STORAGE_FAILURE", "risk progress could not be recorded")
		}
	}
	if len(plan.ComparisonKeys) == 0 {
		if err = events.append(ctx, w.Clock, "COMPARING", 60, "", nil); err != nil {
			return w.fail(ctx, job, "STORAGE_FAILURE", "risk progress could not be recorded")
		}
	}
	for index, key := range plan.StructuralKeys {
		if job, err = w.requireUncanceled(ctx, job); err != nil {
			return job, err
		}
		unitItems, unitErr := w.Engine.CheckStructure(ctx, materialization.Input, key)
		if unitErr != nil {
			return w.fail(ctx, job, "RISK_RULE_CONTRACT_INVALID", "structural risk check failed")
		}
		items = append(items, unitItems...)
		progress := unitProgress(60, 85, index+1, len(plan.StructuralKeys))
		if err = events.append(ctx, w.Clock, "STRUCTURE_CHECKING", progress, "", nil); err != nil {
			return w.fail(ctx, job, "STORAGE_FAILURE", "risk progress could not be recorded")
		}
	}
	if len(plan.StructuralKeys) == 0 {
		if err = events.append(ctx, w.Clock, "STRUCTURE_CHECKING", 85, "", nil); err != nil {
			return w.fail(ctx, job, "STORAGE_FAILURE", "risk progress could not be recorded")
		}
	}
	if job, err = w.requireUncanceled(ctx, job); err != nil {
		return job, err
	}
	if err = events.append(ctx, w.Clock, "SEALING", 95, "", nil); err != nil {
		return w.fail(ctx, job, "STORAGE_FAILURE", "risk progress could not be recorded")
	}
	report, err := w.buildCalculationReport(materialization, job, items)
	if err != nil {
		return w.fail(ctx, job, "RISK_RULE_CONTRACT_INVALID", "risk report could not be canonicalized")
	}
	if job, err = w.requireUncanceled(ctx, job); err != nil {
		return job, err
	}
	if err = w.Reports.SealRiskReport(ctx, report); err != nil {
		return w.fail(ctx, job, "STORAGE_FAILURE", "risk report could not be sealed")
	}
	job, err = w.Jobs.GetJob(ctx, job.ID)
	if err != nil || job.Status != sharedjob.Succeeded || job.Result == nil || job.Result.ID != report.ID {
		return sharedjob.Record{}, errors.New("sealed risk Job result is invalid")
	}
	if err = events.append(ctx, w.Clock, "SUCCEEDED", 100, "", job.Result); err != nil {
		return job, err
	}
	return job, nil
}

func (w Worker) ensureSucceededEvent(ctx context.Context, job sharedjob.Record) error {
	if job.Result == nil {
		return errors.New("succeeded risk job has no result")
	}
	events, err := w.Events.ListEvents(ctx, job.ID, 0)
	if err != nil {
		return err
	}
	if len(events) > 0 {
		last := events[len(events)-1]
		if last.Phase == "SUCCEEDED" && last.Result != nil && last.Result.ID == job.Result.ID && last.Result.Type == job.Result.Type && last.Result.URL == job.Result.URL {
			return nil
		}
	}
	cursor, err := newRiskEventCursor(ctx, w.Events, job.ID)
	if err != nil {
		return err
	}
	return cursor.append(ctx, w.Clock, "SUCCEEDED", 100, "", job.Result)
}

func (w Worker) buildCalculationReport(materialization Materialization, job sharedjob.Record, items []contract.RiskItemV1) (Report, error) {
	input := materialization.Input
	reportID, err := w.IDs.New()
	if err != nil {
		return Report{}, err
	}
	baseline := BaselineRef{Kind: input.Baseline.Kind}
	if input.Baseline.Revision != nil {
		baseline.ReleaseID = input.Baseline.ReleaseID
		baseline.Revision = &RevisionRef{RevisionID: input.Baseline.Revision.RevisionID, ConfigHash: input.Baseline.Revision.ConfigHash, ManifestHash: input.Baseline.Revision.ManifestHash}
	}
	implementations, err := reportImplementations(input)
	if err != nil {
		return Report{}, err
	}
	runs := make([]SimulationRef, 0, len(input.CandidateRuns)+len(input.BaselineRuns))
	for _, run := range append(append([]contract.RunEvidence(nil), input.CandidateRuns...), input.BaselineRuns...) {
		runs = append(runs, SimulationRef{RunID: run.RunID, InputHash: run.InputHash, FingerprintHash: run.FingerprintHash, ResultHash: run.ResultHash})
	}
	base := Report{ID: reportID, JobID: job.ID, ProjectID: job.ProjectID, InputHash: materialization.InputHash, EvidenceHash: materialization.EvidenceHash, Candidate: RevisionRef{RevisionID: input.Candidate.RevisionID, ConfigHash: input.Candidate.ConfigHash, ManifestHash: input.Candidate.ManifestHash}, Baseline: baseline, Policy: input.Policy, Threshold: input.Threshold, Validation: input.Validation, Implementations: implementations, SimulationRuns: runs, CreatedAt: w.Clock.Now().UTC(), CancelGeneration: job.CancelGeneration}
	sort.Slice(items, func(i, j int) bool {
		return items[i].ID+"\x00"+items[i].Rule.ID+"\x00"+items[i].EvidenceHash < items[j].ID+"\x00"+items[j].Rule.ID+"\x00"+items[j].EvidenceHash
	})
	return NewCalculationReport(base, items)
}

func reportImplementations(input contract.RiskInputV1) ([]contract.Identity, error) {
	byID := make(map[string]contract.Identity)
	for _, implementation := range input.Implementations {
		byID[implementation.ID] = implementation
	}
	for _, run := range append(append([]contract.RunEvidence(nil), input.CandidateRuns...), input.BaselineRuns...) {
		for _, implementation := range run.Implementations {
			if existing, found := byID[implementation.ID]; found && existing != implementation {
				return nil, errors.New("implementation identity conflict")
			}
			byID[implementation.ID] = implementation
		}
	}
	values := make([]contract.Identity, 0, len(byID))
	for _, implementation := range byID {
		values = append(values, implementation)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	return values, nil
}

func (w Worker) requireUncanceled(ctx context.Context, previous sharedjob.Record) (sharedjob.Record, error) {
	current, err := w.Jobs.GetJob(ctx, previous.ID)
	if err != nil {
		return sharedjob.Record{}, err
	}
	if current.CancelGeneration != 0 {
		canceled, cancelErr := w.cancelIfRequested(ctx, current)
		if canceled {
			return cancelErr, ErrRiskCanceled
		}
	}
	if current.Status != sharedjob.Running && current.Status != sharedjob.Interrupted {
		return current, errors.New("risk job state changed")
	}
	return current, nil
}

func (w Worker) cancelIfRequested(ctx context.Context, job sharedjob.Record) (bool, sharedjob.Record) {
	if job.CancelGeneration == 0 {
		return false, job
	}
	if job.Status == sharedjob.Canceled {
		return true, job
	}
	if job.Status != sharedjob.Queued && job.Status != sharedjob.Running && job.Status != sharedjob.Interrupted {
		return false, job
	}
	updated, _, err := w.Jobs.Transition(ctx, job.ID, job.Status, sharedjob.Canceled, nil, job.CancelGeneration)
	if err != nil {
		return false, job
	}
	events, eventErr := newRiskEventCursor(ctx, w.Events, job.ID)
	if eventErr == nil {
		_ = events.append(ctx, w.Clock, "CANCELED", events.progress, "", nil)
	}
	return true, updated
}

func (w Worker) fail(ctx context.Context, job sharedjob.Record, code, detail string) (sharedjob.Record, error) {
	current, err := w.Jobs.GetJob(ctx, job.ID)
	if err != nil {
		return sharedjob.Record{}, err
	}
	if current.Status == sharedjob.Succeeded || current.Status == sharedjob.Failed || current.Status == sharedjob.Canceled {
		return current, errors.New(code)
	}
	if current.CancelGeneration > 0 {
		if canceled, canceledJob := w.cancelIfRequested(ctx, current); canceled {
			return canceledJob, ErrRiskCanceled
		}
	}
	if w.Failures != nil {
		failed, _, failErr := w.Failures.FailRiskJob(ctx, current.ID, current.CancelGeneration, code, detail)
		if failErr != nil {
			return sharedjob.Record{}, failErr
		}
		return failed, errors.New(code)
	}
	updated, _, err := w.Jobs.Transition(ctx, current.ID, current.Status, sharedjob.Failed, nil, current.CancelGeneration)
	if err != nil {
		return sharedjob.Record{}, err
	}
	events, eventErr := newRiskEventCursor(ctx, w.Events, job.ID)
	if eventErr == nil {
		_ = events.append(ctx, w.Clock, "FAILED", events.progress, code+": "+detail, nil)
	}
	return updated, errors.New(code)
}

type riskEventCursor struct {
	store    EventStore
	jobID    domain.ID
	ordinal  int64
	progress int
}

func newRiskEventCursor(ctx context.Context, store EventStore, jobID domain.ID) (*riskEventCursor, error) {
	events, err := store.ListEvents(ctx, jobID, 0)
	if err != nil {
		return nil, err
	}
	cursor := &riskEventCursor{store: store, jobID: jobID}
	if len(events) > 0 {
		cursor.ordinal = events[len(events)-1].Ordinal
		cursor.progress = events[len(events)-1].Progress
	}
	return cursor, nil
}

func (c *riskEventCursor) append(ctx context.Context, clock Clock, phase string, progress int, safeError string, result *sharedjob.Result) error {
	if progress < c.progress {
		progress = c.progress
	}
	event := sharedjob.Event{JobID: c.jobID, Ordinal: c.ordinal + 1, Phase: phase, Progress: progress, SafeError: safeError, Result: result, CreatedAt: clock.Now().UTC()}
	_, _, err := c.store.Append(ctx, event)
	if err == nil {
		c.ordinal = event.Ordinal
		c.progress = event.Progress
	}
	return err
}

func unitProgress(start, end, complete, total int) int {
	if total <= 0 {
		return end
	}
	return start + ((end - start) * complete / total)
}

func duplicateString(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index] == values[index-1] {
			return true
		}
	}
	return false
}

func recoveryError(code string) error { return fmt.Errorf("risk recovery: %s", code) }
