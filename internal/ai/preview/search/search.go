// Package search implements the versioned deterministic grid search used by
// AI proposal preview. It generates values only; validation and simulation
// remain behind their registered pure evaluator port.
package search

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
)

const (
	VersionV1           = "v1"
	DescriptorIDV1      = "grid-constraint-search"
	OrderingV1          = "entity_path_then_numeric_ascending_cartesian"
	MaxDimensionsV1     = 8
	MaxDurationMillisV1 = int64(30_000)
)

var (
	ErrInvalid     = errors.New("AI parameter search is invalid")
	ErrUnavailable = errors.New("AI parameter search evaluator is unavailable")
)

type NumericKind string

const (
	Decimal  NumericKind = "decimal"
	Duration NumericKind = "duration_ms"
)

type Range struct {
	Min  string `json:"min"`
	Max  string `json:"max"`
	Step string `json:"step"`
}

type Dimension struct {
	EntityID aicontract.EntityID  `json:"entity_id"`
	Path     aicontract.FieldPath `json:"path"`
	Kind     NumericKind          `json:"kind"`
	Values   []string             `json:"values,omitempty"`
	Range    *Range               `json:"range,omitempty"`
}

type Assignment struct {
	EntityID aicontract.EntityID  `json:"entity_id"`
	Path     aicontract.FieldPath `json:"path"`
	Kind     NumericKind          `json:"kind"`
	Value    string               `json:"value"`
}

type Budget struct {
	MaxCandidates     int   `json:"max_candidates"`
	MaxDurationMillis int64 `json:"max_duration_millis"`
}

type Descriptor struct {
	Identity      aicontract.VersionIdentity `json:"identity"`
	Ordering      string                     `json:"ordering"`
	MaxDimensions int                        `json:"max_dimensions"`
}

func DescriptorV1() Descriptor {
	descriptor := Descriptor{Identity: aicontract.VersionIdentity{ID: DescriptorIDV1, Version: VersionV1}, Ordering: OrderingV1, MaxDimensions: MaxDimensionsV1}
	descriptor.Identity.Hash = aicontract.Hash(mustHash("eco-guardian.ai-search-descriptor/v1", struct {
		ID            string `json:"id"`
		Version       string `json:"version"`
		Ordering      string `json:"ordering"`
		MaxDimensions int    `json:"max_dimensions"`
	}{DescriptorIDV1, VersionV1, OrderingV1, MaxDimensionsV1}))
	return descriptor
}

type EvaluatorDescriptor struct {
	Identity   aicontract.VersionIdentity `json:"identity"`
	Validation aicontract.VersionIdentity `json:"validation"`
	Simulation aicontract.VersionIdentity `json:"simulation"`
}

func (descriptor EvaluatorDescriptor) Valid() bool {
	return descriptor.Identity.Valid() && descriptor.Validation.Valid() && descriptor.Simulation.Valid()
}

type Candidate struct {
	Ordinal     int          `json:"ordinal"`
	InputHash   string       `json:"input_hash"`
	Assignments []Assignment `json:"assignments"`
}

type EvaluationStatus string

const (
	EvaluationPassed            EvaluationStatus = "passed"
	EvaluationValidationBlocked EvaluationStatus = "validation_blocked"
)

type Objective struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

type Constraint struct {
	ID           string `json:"id"`
	Satisfied    bool   `json:"satisfied"`
	EvidenceHash string `json:"evidence_hash"`
}

// Evaluation is returned by a registered composite that invokes #6 first and
// #10 only for validation-passing candidates.
type Evaluation struct {
	Status               EvaluationStatus `json:"status"`
	ValidationResultHash string           `json:"validation_result_hash"`
	SimulationResultHash string           `json:"simulation_result_hash,omitempty"`
	Objectives           []Objective      `json:"objectives,omitempty"`
	Constraints          []Constraint     `json:"constraints,omitempty"`
}

type CandidateEvaluator interface {
	Descriptor() EvaluatorDescriptor
	EvaluateCandidate(context.Context, Candidate) (Evaluation, error)
}

type Request struct {
	Base                aicontract.FrozenBaseIdentity `json:"base"`
	MaterializationHash aicontract.Hash               `json:"materialization_hash"`
	AllowedTargets      []aicontract.AllowedTarget    `json:"allowed_targets"`
	Dimensions          []Dimension                   `json:"dimensions"`
	Budget              Budget                        `json:"budget"`
}

type StopReason string

const (
	StopCompleted       StopReason = "completed"
	StopCandidateBudget StopReason = "candidate_budget_exhausted"
	StopTimeBudget      StopReason = "time_budget_exhausted"
	StopCanceled        StopReason = "canceled"
	StopEvaluatorError  StopReason = "evaluator_error"
)

type Record struct {
	Candidate  Candidate   `json:"candidate"`
	Evaluation *Evaluation `json:"evaluation,omitempty"`
	ErrorCode  string      `json:"error_code,omitempty"`
	ResultHash string      `json:"result_hash"`
}

const (
	errorTimeBudget = "SEARCH_TIME_BUDGET_EXHAUSTED"
	errorCanceled   = "SEARCH_CANCELED"
	errorEvaluator  = "SEARCH_EVALUATOR_FAILED"
)

type ResultV1 struct {
	Version             string              `json:"version"`
	Descriptor          Descriptor          `json:"descriptor"`
	Evaluator           EvaluatorDescriptor `json:"evaluator"`
	MaterializationHash aicontract.Hash     `json:"materialization_hash"`
	InputHash           string              `json:"input_hash"`
	StopReason          StopReason          `json:"stop_reason"`
	Records             []Record            `json:"records"`
	ResultHash          string              `json:"result_hash"`
}

type Service struct {
	Evaluator  CandidateEvaluator
	Registered EvaluatorDescriptor
}

func (service Service) Search(ctx context.Context, request Request) (ResultV1, error) {
	if ctx == nil || !request.Base.Valid() || !request.MaterializationHash.Valid() || request.Budget.MaxCandidates <= 0 || request.Budget.MaxCandidates > aicontract.V1MaxSearchCandidates || request.Budget.MaxDurationMillis <= 0 || request.Budget.MaxDurationMillis > MaxDurationMillisV1 {
		return ResultV1{}, ErrInvalid
	}
	if service.Evaluator == nil || !service.Registered.Valid() || service.Evaluator.Descriptor() != service.Registered {
		return ResultV1{}, ErrUnavailable
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(request.Budget.MaxDurationMillis)*time.Millisecond)
	defer cancel()
	allowedTargets, err := normalizeAllowedTargets(request.AllowedTargets)
	if err != nil {
		return ResultV1{}, err
	}
	dimensions, err := normalizeDimensions(request.Dimensions, allowedTargets)
	if err != nil {
		return ResultV1{}, err
	}
	normalized := request
	normalized.Dimensions = dimensions
	normalized.AllowedTargets = allowedTargets
	inputHash, err := canonicalHash("eco-guardian.ai-search-input/v1", struct {
		Descriptor Descriptor          `json:"descriptor"`
		Evaluator  EvaluatorDescriptor `json:"evaluator"`
		Request    Request             `json:"request"`
	}{DescriptorV1(), service.Registered, normalized})
	if err != nil {
		return ResultV1{}, errors.Join(ErrInvalid, err)
	}
	result := ResultV1{Version: VersionV1, Descriptor: DescriptorV1(), Evaluator: service.Registered, MaterializationHash: request.MaterializationHash, InputHash: inputHash, Records: []Record{}}
	iterator := newIterator(dimensions)
	for iterator.Next() {
		if len(result.Records) == request.Budget.MaxCandidates {
			result.StopReason = StopCandidateBudget
			return sealResult(result), nil
		}
		if err := runCtx.Err(); err != nil {
			result.StopReason = stopForContext(ctx)
			return sealResult(result), err
		}
		assignments := iterator.Assignments()
		candidateHash, hashErr := canonicalHash("eco-guardian.ai-search-candidate-input/v1", struct {
			SearchInputHash string       `json:"search_input_hash"`
			Ordinal         int          `json:"ordinal"`
			Assignments     []Assignment `json:"assignments"`
		}{inputHash, len(result.Records), assignments})
		if hashErr != nil {
			return ResultV1{}, hashErr
		}
		candidate := Candidate{Ordinal: len(result.Records), InputHash: candidateHash, Assignments: assignments}
		evaluation, evaluateErr := service.Evaluator.EvaluateCandidate(runCtx, candidate)
		if evaluateErr != nil {
			errorCode := errorEvaluator
			if runCtx.Err() != nil {
				result.StopReason = stopForContext(ctx)
				if result.StopReason == StopCanceled {
					errorCode = errorCanceled
				} else {
					errorCode = errorTimeBudget
				}
			} else {
				result.StopReason = StopEvaluatorError
			}
			result.Records = append(result.Records, failedRecord(candidate, errorCode))
			return sealResult(result), evaluateErr
		}
		evaluation, err = normalizeEvaluation(evaluation)
		if err != nil {
			result.StopReason = StopEvaluatorError
			result.Records = append(result.Records, failedRecord(candidate, errorEvaluator))
			return sealResult(result), err
		}
		resultHash, hashErr := canonicalHash("eco-guardian.ai-search-candidate-result/v1", struct {
			InputHash  string      `json:"input_hash"`
			Evaluation *Evaluation `json:"evaluation"`
			ErrorCode  string      `json:"error_code,omitempty"`
		}{candidate.InputHash, &evaluation, ""})
		if hashErr != nil {
			return ResultV1{}, hashErr
		}
		result.Records = append(result.Records, Record{Candidate: candidate, Evaluation: &evaluation, ResultHash: resultHash})
	}
	result.StopReason = StopCompleted
	return sealResult(result), nil
}

func (result ResultV1) Valid() bool {
	if result.Version != VersionV1 || result.Descriptor != DescriptorV1() || !result.Evaluator.Valid() || !result.MaterializationHash.Valid() || !validHash(result.InputHash) || !validStop(result.StopReason) || !validHash(result.ResultHash) {
		return false
	}
	for index, record := range result.Records {
		if record.Candidate.Ordinal != index || !validHash(record.Candidate.InputHash) || !validHash(record.ResultHash) {
			return false
		}
		expectedInput, err := canonicalHash("eco-guardian.ai-search-candidate-input/v1", struct {
			SearchInputHash string       `json:"search_input_hash"`
			Ordinal         int          `json:"ordinal"`
			Assignments     []Assignment `json:"assignments"`
		}{result.InputHash, index, record.Candidate.Assignments})
		if err != nil || expectedInput != record.Candidate.InputHash {
			return false
		}
		if (record.Evaluation == nil) == (record.ErrorCode == "") {
			return false
		}
		if record.Evaluation != nil {
			normalized, err := normalizeEvaluation(*record.Evaluation)
			if err != nil || !equalEvaluation(normalized, *record.Evaluation) {
				return false
			}
		} else if record.ErrorCode != errorTimeBudget && record.ErrorCode != errorCanceled && record.ErrorCode != errorEvaluator {
			return false
		}
		expectedResult, err := canonicalHash("eco-guardian.ai-search-candidate-result/v1", struct {
			InputHash  string      `json:"input_hash"`
			Evaluation *Evaluation `json:"evaluation,omitempty"`
			ErrorCode  string      `json:"error_code,omitempty"`
		}{record.Candidate.InputHash, record.Evaluation, record.ErrorCode})
		if err != nil || expectedResult != record.ResultHash {
			return false
		}
	}
	copy := result
	copy.ResultHash = ""
	hash, err := canonicalHash("eco-guardian.ai-search-result/v1", copy)
	return err == nil && hash == result.ResultHash
}

func sealResult(result ResultV1) ResultV1 {
	result.ResultHash = ""
	result.ResultHash, _ = canonicalHash("eco-guardian.ai-search-result/v1", result)
	return result
}

func failedRecord(candidate Candidate, code string) Record {
	hash, _ := canonicalHash("eco-guardian.ai-search-candidate-result/v1", struct {
		InputHash  string      `json:"input_hash"`
		Evaluation *Evaluation `json:"evaluation,omitempty"`
		ErrorCode  string      `json:"error_code,omitempty"`
	}{candidate.InputHash, nil, code})
	return Record{Candidate: candidate, ErrorCode: code, ResultHash: hash}
}

func normalizeDimensions(values []Dimension, allowed []aicontract.AllowedTarget) ([]Dimension, error) {
	if len(values) == 0 || len(values) > MaxDimensionsV1 {
		return nil, ErrInvalid
	}
	out := make([]Dimension, len(values))
	for index, value := range values {
		if !value.EntityID.Valid() || !value.Path.Valid() || (value.Kind != Decimal && value.Kind != Duration) || !allowedPath(allowed, value.EntityID, value.Path) || (len(value.Values) == 0) == (value.Range == nil) {
			return nil, ErrInvalid
		}
		candidates, err := dimensionValues(value)
		if err != nil {
			return nil, err
		}
		out[index] = Dimension{EntityID: value.EntityID, Path: value.Path, Kind: value.Kind, Values: candidates}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EntityID != out[j].EntityID {
			return out[i].EntityID < out[j].EntityID
		}
		return out[i].Path < out[j].Path
	})
	for index := 1; index < len(out); index++ {
		if out[index-1].EntityID == out[index].EntityID && out[index-1].Path == out[index].Path {
			return nil, ErrInvalid
		}
	}
	return out, nil
}

func dimensionValues(value Dimension) ([]string, error) {
	values := append([]string(nil), value.Values...)
	if value.Range != nil {
		var err error
		values, err = rangeValues(value.Kind, *value.Range)
		if err != nil {
			return nil, err
		}
	}
	type parsedValue struct {
		text     string
		decimal  formula.Decimal
		duration formula.Duration
	}
	parsed := make([]parsedValue, 0, len(values))
	for _, text := range values {
		item := parsedValue{}
		if value.Kind == Decimal {
			decimal, err := formula.ParseDecimal(text)
			if err != nil || decimal.String() != text {
				return nil, ErrInvalid
			}
			item.text, item.decimal = text, decimal
		} else {
			duration, err := formula.ParseDuration(text)
			if err != nil || duration.String() != text {
				return nil, ErrInvalid
			}
			item.text, item.duration = text, duration
		}
		parsed = append(parsed, item)
	}
	sort.Slice(parsed, func(i, j int) bool {
		if value.Kind == Decimal {
			return parsed[i].decimal.Compare(parsed[j].decimal) < 0
		}
		return parsed[i].duration < parsed[j].duration
	})
	out := make([]string, 0, len(parsed))
	for _, item := range parsed {
		if len(out) == 0 || out[len(out)-1] != item.text {
			out = append(out, item.text)
		}
	}
	if len(out) == 0 || len(out) > aicontract.V1MaxSearchCandidates+1 {
		return nil, ErrInvalid
	}
	return out, nil
}

func rangeValues(kind NumericKind, value Range) ([]string, error) {
	values := make([]string, 0)
	if kind == Duration {
		min, minErr := formula.ParseDuration(value.Min)
		max, maxErr := formula.ParseDuration(value.Max)
		step, stepErr := formula.ParseDuration(value.Step)
		if minErr != nil || maxErr != nil || stepErr != nil || min.String() != value.Min || max.String() != value.Max || step.String() != value.Step || step <= 0 || min > max {
			return nil, ErrInvalid
		}
		for current := min; current <= max && len(values) <= aicontract.V1MaxSearchCandidates; current += step {
			values = append(values, current.String())
			if current > formula.Duration(^uint64(0)>>1)-step {
				break
			}
		}
		return values, nil
	}
	min, minErr := formula.ParseDecimal(value.Min)
	max, maxErr := formula.ParseDecimal(value.Max)
	step, stepErr := formula.ParseDecimal(value.Step)
	zero, _ := formula.ParseDecimal("0")
	if minErr != nil || maxErr != nil || stepErr != nil || min.String() != value.Min || max.String() != value.Max || step.String() != value.Step || step.Compare(zero) <= 0 || min.Compare(max) > 0 {
		return nil, ErrInvalid
	}
	for current := min; current.Compare(max) <= 0 && len(values) <= aicontract.V1MaxSearchCandidates; {
		values = append(values, current.String())
		next, err := formula.Add(current, step)
		if err != nil || next.Compare(current) <= 0 {
			return nil, ErrInvalid
		}
		current = next
	}
	return values, nil
}

func normalizeEvaluation(value Evaluation) (Evaluation, error) {
	if !validHash(value.ValidationResultHash) || (value.Status != EvaluationPassed && value.Status != EvaluationValidationBlocked) {
		return Evaluation{}, ErrInvalid
	}
	if value.Status == EvaluationPassed && !validHash(value.SimulationResultHash) || value.Status == EvaluationValidationBlocked && value.SimulationResultHash != "" {
		return Evaluation{}, ErrInvalid
	}
	copy := value
	copy.Objectives = append([]Objective(nil), value.Objectives...)
	for _, objective := range copy.Objectives {
		decimal, err := formula.ParseDecimal(objective.Value)
		if objective.ID == "" || err != nil || decimal.String() != objective.Value {
			return Evaluation{}, ErrInvalid
		}
	}
	sort.Slice(copy.Objectives, func(i, j int) bool { return copy.Objectives[i].ID < copy.Objectives[j].ID })
	copy.Constraints = append([]Constraint(nil), value.Constraints...)
	for _, constraint := range copy.Constraints {
		if constraint.ID == "" || !validHash(constraint.EvidenceHash) {
			return Evaluation{}, ErrInvalid
		}
	}
	sort.Slice(copy.Constraints, func(i, j int) bool { return copy.Constraints[i].ID < copy.Constraints[j].ID })
	for index := 1; index < len(copy.Objectives); index++ {
		if copy.Objectives[index-1].ID == copy.Objectives[index].ID {
			return Evaluation{}, ErrInvalid
		}
	}
	for index := 1; index < len(copy.Constraints); index++ {
		if copy.Constraints[index-1].ID == copy.Constraints[index].ID {
			return Evaluation{}, ErrInvalid
		}
	}
	return copy, nil
}

type iterator struct {
	dimensions []Dimension
	indexes    []int
	started    bool
	done       bool
}

func newIterator(dimensions []Dimension) *iterator {
	return &iterator{dimensions: dimensions, indexes: make([]int, len(dimensions))}
}

func (iterator *iterator) Next() bool {
	if iterator.done {
		return false
	}
	if !iterator.started {
		iterator.started = true
		return true
	}
	for index := len(iterator.indexes) - 1; index >= 0; index-- {
		iterator.indexes[index]++
		if iterator.indexes[index] < len(iterator.dimensions[index].Values) {
			return true
		}
		iterator.indexes[index] = 0
	}
	iterator.done = true
	return false
}

func (iterator *iterator) Assignments() []Assignment {
	values := make([]Assignment, len(iterator.dimensions))
	for index, dimension := range iterator.dimensions {
		values[index] = Assignment{EntityID: dimension.EntityID, Path: dimension.Path, Kind: dimension.Kind, Value: dimension.Values[iterator.indexes[index]]}
	}
	return values
}

func allowedPath(targets []aicontract.AllowedTarget, entityID aicontract.EntityID, path aicontract.FieldPath) bool {
	for _, target := range targets {
		if target.EntityID != entityID {
			continue
		}
		for _, allowed := range target.Paths {
			if allowed.Path != path {
				continue
			}
			for _, operation := range allowed.Operations {
				if operation == aicontract.OperationReplace {
					return true
				}
			}
		}
	}
	return false
}

func normalizeAllowedTargets(values []aicontract.AllowedTarget) ([]aicontract.AllowedTarget, error) {
	if len(values) == 0 {
		return nil, ErrInvalid
	}
	out := append([]aicontract.AllowedTarget(nil), values...)
	for index := range out {
		if !out[index].Valid() {
			return nil, ErrInvalid
		}
		out[index].Paths = append([]aicontract.AllowedPath(nil), out[index].Paths...)
		for pathIndex := range out[index].Paths {
			out[index].Paths[pathIndex].Operations = append([]aicontract.PatchOperationKind(nil), out[index].Paths[pathIndex].Operations...)
			sort.Slice(out[index].Paths[pathIndex].Operations, func(i, j int) bool {
				return out[index].Paths[pathIndex].Operations[i] < out[index].Paths[pathIndex].Operations[j]
			})
		}
		sort.Slice(out[index].Paths, func(i, j int) bool { return out[index].Paths[i].Path < out[index].Paths[j].Path })
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EntityID < out[j].EntityID })
	for index := 1; index < len(out); index++ {
		if out[index-1].EntityID == out[index].EntityID {
			return nil, ErrInvalid
		}
	}
	return out, nil
}

func stopForContext(parent context.Context) StopReason {
	if parent.Err() != nil {
		return StopCanceled
	}
	return StopTimeBudget
}

func validStop(value StopReason) bool {
	return value == StopCompleted || value == StopCandidateBudget || value == StopTimeBudget || value == StopCanceled || value == StopEvaluatorError
}

func equalEvaluation(left, right Evaluation) bool {
	leftJSON, _ := domain.CanonicalJSON(left)
	rightJSON, _ := domain.CanonicalJSON(right)
	return bytes.Equal(leftJSON, rightJSON)
}

func canonicalHash(hashDomain string, value any) (string, error) {
	canonical, err := domain.CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	_, _ = fmt.Fprint(hasher, hashDomain)
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(canonical)
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func mustHash(hashDomain string, value any) string {
	hash, err := canonicalHash(hashDomain, value)
	if err != nil {
		panic(err)
	}
	return hash
}

func validHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
