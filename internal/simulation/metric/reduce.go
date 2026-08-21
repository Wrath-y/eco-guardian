package metric

import (
	"errors"
	"fmt"
	"sort"

	"github.com/zouyi/eco-guardian/internal/formula"
)

const (
	AggregationV1 = "mean-decimal-v1"
	ConfidenceV1  = "min-max-interval-v1"
)

var ErrReductionInvalid = errors.New("simulation metric reduction is invalid")

type AggregateResult struct {
	Descriptor     Descriptor
	Status         Status
	Value          *formula.Decimal
	ConfidenceLow  *formula.Decimal
	ConfidenceHigh *formula.Decimal
	SampleCount    int
	Unavailable    *UnavailableReason
}

func (result AggregateResult) Valid() bool {
	if !result.Descriptor.Valid() || result.SampleCount < 1 {
		return false
	}
	if result.Status == Unavailable {
		return result.Value == nil && result.ConfidenceLow == nil && result.ConfidenceHigh == nil && result.Unavailable != nil && result.Unavailable.Valid()
	}
	return result.Status == Available && result.Value != nil && result.ConfidenceLow != nil && result.ConfidenceHigh != nil && result.Unavailable == nil
}

// Reduce copies and sorts samples by ordinal, then invokes modules by their raw
// UTF-8 IDs. No worker completion order or map iteration enters aggregation.
func Reduce(registry *Registry, samples []Sample) ([]AggregateResult, error) {
	if registry == nil || len(samples) == 0 {
		return nil, ErrReductionInvalid
	}
	ordered := append([]Sample(nil), samples...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Ordinal < ordered[j].Ordinal })
	for index := 1; index < len(ordered); index++ {
		if ordered[index-1].Ordinal == ordered[index].Ordinal {
			return nil, fmt.Errorf("%w: duplicate sample ordinal", ErrReductionInvalid)
		}
	}
	descriptors := registry.Descriptors()
	results := make([]AggregateResult, 0, len(descriptors))
	for _, descriptor := range descriptors {
		module, _ := registry.Module(descriptor.ID)
		result, err := reduceModule(module, ordered)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func reduceModule(module Module, samples []Sample) (AggregateResult, error) {
	descriptor := module.Descriptor()
	var total, low, high *formula.Decimal
	missing := []string{}
	for _, sample := range samples {
		result, err := module.Evaluate(sample)
		if err != nil {
			return AggregateResult{}, err
		}
		if result.Status == Unavailable {
			missing = append(missing, result.Unavailable.Missing...)
			continue
		}
		if total == nil {
			value := *result.Value
			total, low, high = &value, &value, &value
			continue
		}
		value, err := formula.Add(*total, *result.Value)
		if err != nil {
			return AggregateResult{}, err
		}
		total = &value
		if result.Value.Compare(*low) < 0 {
			value := *result.Value
			low = &value
		}
		if result.Value.Compare(*high) > 0 {
			value := *result.Value
			high = &value
		}
	}
	if len(missing) > 0 || total == nil {
		return AggregateResult{Descriptor: descriptor, Status: Unavailable, SampleCount: len(samples), Unavailable: unavailableReason(missing)}, nil
	}
	count, err := formula.ParseDecimal(fmt.Sprintf("%d", len(samples)))
	if err != nil {
		return AggregateResult{}, err
	}
	mean, err := formula.Divide(*total, count)
	if err != nil {
		return AggregateResult{}, err
	}
	return AggregateResult{Descriptor: descriptor, Status: Available, Value: &mean, ConfidenceLow: low, ConfidenceHigh: high, SampleCount: len(samples)}, nil
}

func unavailableReason(missing []string) *UnavailableReason {
	unique := map[string]struct{}{}
	for _, item := range missing {
		unique[item] = struct{}{}
	}
	items := make([]string, 0, len(unique))
	for item := range unique {
		items = append(items, item)
	}
	sort.Strings(items)
	return &UnavailableReason{Code: "MISSING_STRUCTURED_INPUT", Missing: items, Message: "Required structured observations are unavailable"}
}
