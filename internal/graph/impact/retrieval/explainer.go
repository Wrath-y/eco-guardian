package retrieval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/impact"
)

var ErrInvalidExplanation = errors.New("invalid evidence explanation")

const MaxExplanationRefs = 50

func Explain(ctx context.Context, explainer impact.EvidenceExplainer, ids impact.IDGenerator, clock impact.Clock, reportID domain.ID, refs []impact.EvidenceRef) (impact.ExplanationAttempt, error) {
	if explainer == nil || ids == nil || clock == nil || !reportID.Valid() || len(refs) == 0 || len(refs) > MaxExplanationRefs {
		return impact.ExplanationAttempt{}, ErrInvalidExplanation
	}
	refs, inputHash, err := ExplanationInputHash(reportID, refs)
	if err != nil {
		return impact.ExplanationAttempt{}, err
	}
	attemptID, err := ids.New()
	if err != nil {
		return impact.ExplanationAttempt{}, err
	}
	attempt := impact.ExplanationAttempt{ID: attemptID, ReportID: reportID, InputHash: inputHash, Status: "failed", CreatedAt: clock.Now().UTC()}
	seen := map[string]impact.EvidenceRef{}
	for _, ref := range refs {
		seen[ref.ID] = ref
	}
	output, err := explainer.Explain(ctx, impact.ExplanationInput{ReportID: reportID, Refs: refs})
	if err != nil {
		attempt.Diagnostics = safeDiagnostic(err)
		return attempt, nil
	}
	if strings.TrimSpace(output.Text) == "" || output.Provider == "" || output.Model == "" || len(output.Refs) == 0 {
		attempt.Diagnostics = "INVALID_EXPLANATION_OUTPUT"
		return attempt, nil
	}
	for _, refID := range output.Refs {
		if _, exists := seen[refID]; !exists {
			attempt.Diagnostics = "UNSUPPORTED_EVIDENCE_REF"
			return attempt, nil
		}
	}
	attempt.Status, attempt.Text, attempt.Provider, attempt.Model = "succeeded", output.Text, output.Provider, output.Model
	attempt.Refs = append([]string(nil), output.Refs...)
	return attempt, nil
}

func ExplanationInputHash(reportID domain.ID, refs []impact.EvidenceRef) ([]impact.EvidenceRef, string, error) {
	if !reportID.Valid() || len(refs) == 0 || len(refs) > MaxExplanationRefs {
		return nil, "", ErrInvalidExplanation
	}
	refs = append([]impact.EvidenceRef(nil), refs...)
	sort.Slice(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })
	seen := map[string]struct{}{}
	for _, ref := range refs {
		if ref.ID == "" || !impact.ValidHash(ref.Hash) || ref.Kind == "" {
			return nil, "", ErrInvalidExplanation
		}
		if _, duplicate := seen[ref.ID]; duplicate {
			return nil, "", ErrInvalidExplanation
		}
		seen[ref.ID] = struct{}{}
	}
	encoded, _ := json.Marshal(struct {
		Version  string               `json:"version"`
		ReportID domain.ID            `json:"report_id"`
		Refs     []impact.EvidenceRef `json:"refs"`
	}{impact.AnalysisContractVersion, reportID, refs})
	digest := sha256.Sum256(encoded)
	return refs, hex.EncodeToString(digest[:]), nil
}

func safeDiagnostic(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "AI_TIMEOUT"
	}
	return "AI_UNAVAILABLE"
}
