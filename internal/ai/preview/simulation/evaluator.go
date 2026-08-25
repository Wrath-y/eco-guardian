package simulation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aipreview "github.com/zouyi/eco-guardian/internal/ai/preview"
	"github.com/zouyi/eco-guardian/internal/domain"
	simulationcontract "github.com/zouyi/eco-guardian/internal/simulation/contract"
	simulationpreview "github.com/zouyi/eco-guardian/internal/simulation/preview"
)

const VersionV1 = "ai-proposal-simulation-v1"

var (
	ErrInvalid     = errors.New("AI proposal simulation preview is invalid")
	ErrUnavailable = errors.New("AI proposal simulation evaluator is unavailable")
)

type SceneRequest struct {
	SceneID      string                              `json:"scene_id"`
	SceneVersion string                              `json:"scene_version"`
	SampleCount  int                                 `json:"sample_count"`
	Seed         *uint64                             `json:"seed,omitempty"`
	Budget       *simulationcontract.BudgetOverride  `json:"budget,omitempty"`
	Metrics      []simulationcontract.MetricIdentity `json:"metrics"`
}

type Request struct {
	Proposal                aipreview.ProposalMaterializationV1         `json:"-"`
	Scenes                  []SceneRequest                              `json:"scenes"`
	RevisionImplementations []simulationcontract.RevisionImplementation `json:"revision_implementations"`
}

type SceneResult struct {
	SceneID      string                     `json:"scene_id"`
	SceneVersion string                     `json:"scene_version"`
	Preview      simulationpreview.ResultV1 `json:"preview"`
}

type ResultV1 struct {
	Version            string          `json:"version"`
	Advisory           bool            `json:"advisory"`
	ProposalHash       aicontract.Hash `json:"proposal_materialization_hash"`
	SimulationSealHash string          `json:"simulation_materialization_hash"`
	Scenes             []SceneResult   `json:"scenes"`
	Canonical          []byte          `json:"-"`
	Hash               string          `json:"hash"`
}

type Service struct{ Evaluator simulationpreview.Evaluator }

func (service Service) Evaluate(ctx context.Context, request Request) (ResultV1, error) {
	if ctx == nil || service.Evaluator == nil {
		return ResultV1{}, ErrUnavailable
	}
	if !request.Proposal.Valid() || len(request.Scenes) == 0 {
		return ResultV1{}, ErrInvalid
	}
	base := simulationcontract.Revision{
		ID: simulationcontract.ID(request.Proposal.Base.ConfigRevisionID), ProjectID: simulationcontract.ID(request.Proposal.Base.ProjectID),
		ConfigHash: string(request.Proposal.Base.ConfigHash), ManifestHash: string(request.Proposal.Base.VersionManifestHash),
	}
	materialization, err := simulationpreview.SealMaterialization(base, request.Proposal.Canonical)
	if err != nil {
		return ResultV1{}, errors.Join(ErrInvalid, err)
	}
	scenes := append([]SceneRequest(nil), request.Scenes...)
	for index := range scenes {
		scenes[index].Metrics = append([]simulationcontract.MetricIdentity(nil), scenes[index].Metrics...)
		scenes[index].Seed = cloneSeed(scenes[index].Seed)
		scenes[index].Budget = cloneBudget(scenes[index].Budget)
	}
	sort.Slice(scenes, func(i, j int) bool {
		if scenes[i].SceneID != scenes[j].SceneID {
			return scenes[i].SceneID < scenes[j].SceneID
		}
		return scenes[i].SceneVersion < scenes[j].SceneVersion
	})
	results := make([]SceneResult, 0, len(scenes))
	for index, scene := range scenes {
		if index > 0 && scene.SceneID == scenes[index-1].SceneID && scene.SceneVersion == scenes[index-1].SceneVersion {
			return ResultV1{}, ErrInvalid
		}
		if err = ctx.Err(); err != nil {
			return ResultV1{}, err
		}
		preview, evaluateErr := service.Evaluator.Evaluate(ctx, simulationpreview.Request{
			Materialization: materialization, SceneID: scene.SceneID, SceneVersion: scene.SceneVersion, SampleCount: scene.SampleCount,
			Seed: cloneSeed(scene.Seed), Budget: cloneBudget(scene.Budget), Metrics: append([]simulationcontract.MetricIdentity(nil), scene.Metrics...),
			RevisionImplementations: append([]simulationcontract.RevisionImplementation(nil), request.RevisionImplementations...),
		})
		if evaluateErr != nil {
			return ResultV1{}, evaluateErr
		}
		if !validPreview(preview, materialization.Hash, scene) {
			return ResultV1{}, ErrInvalid
		}
		results = append(results, SceneResult{SceneID: scene.SceneID, SceneVersion: scene.SceneVersion, Preview: preview})
	}
	result := ResultV1{Version: VersionV1, Advisory: true, ProposalHash: request.Proposal.Hash, SimulationSealHash: materialization.Hash, Scenes: results}
	result.Canonical, result.Hash, err = canonicalResult(result)
	if err != nil || !result.Valid() {
		return ResultV1{}, ErrInvalid
	}
	return result, nil
}

func (result ResultV1) Valid() bool {
	if result.Version != VersionV1 || !result.Advisory || !result.ProposalHash.Valid() || !validHash(result.SimulationSealHash) || !validHash(result.Hash) || len(result.Scenes) == 0 {
		return false
	}
	for index, scene := range result.Scenes {
		if index > 0 && (result.Scenes[index-1].SceneID > scene.SceneID || result.Scenes[index-1].SceneID == scene.SceneID && result.Scenes[index-1].SceneVersion >= scene.SceneVersion) || !validPreview(scene.Preview, result.SimulationSealHash, SceneRequest{SceneID: scene.SceneID, SceneVersion: scene.SceneVersion}) {
			return false
		}
	}
	canonical, hash, err := canonicalResult(result)
	return err == nil && hash == result.Hash && bytes.Equal(canonical, result.Canonical)
}

func validPreview(value simulationpreview.ResultV1, materializationHash string, scene SceneRequest) bool {
	if !value.Advisory || value.SchemaVersion != simulationpreview.SchemaVersionV1 || value.MaterializationHash != materializationHash || value.Input.RuleMaterializationHash != materializationHash || value.Input.SceneID != scene.SceneID || value.Input.SceneVersion != scene.SceneVersion || !validHash(value.InputHash) || !validHash(value.FingerprintHash) || !validHash(value.ResultHash) || value.Result.InputHash != value.InputHash || value.Result.FingerprintHash != value.FingerprintHash || len(value.Fingerprint.Revision) == 0 || len(value.Fingerprint.Simulation) == 0 || len(value.Result.Metrics) == 0 {
		return false
	}
	inputHash, err := value.Input.Hash()
	if err != nil || inputHash != value.InputHash {
		return false
	}
	resultHash, err := value.Result.Hash()
	return err == nil && resultHash == value.ResultHash
}

func canonicalResult(result ResultV1) ([]byte, string, error) {
	payload := struct {
		Version            string          `json:"version"`
		Advisory           bool            `json:"advisory"`
		ProposalHash       aicontract.Hash `json:"proposal_materialization_hash"`
		SimulationSealHash string          `json:"simulation_materialization_hash"`
		Scenes             []SceneResult   `json:"scenes"`
	}{result.Version, result.Advisory, result.ProposalHash, result.SimulationSealHash, result.Scenes}
	canonical, err := domain.CanonicalJSON(payload)
	if err != nil {
		return nil, "", err
	}
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("eco-guardian.ai-proposal-simulation/v1"))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(canonical)
	return canonical, hex.EncodeToString(hasher.Sum(nil)), nil
}

func cloneSeed(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneBudget(value *simulationcontract.BudgetOverride) *simulationcontract.BudgetOverride {
	if value == nil {
		return nil
	}
	copy := *value
	copy.MaxEvents = cloneInt(value.MaxEvents)
	copy.MaxSteps = cloneInt(value.MaxSteps)
	copy.MaxRuntimeMS = cloneInt(value.MaxRuntimeMS)
	return &copy
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func validHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
