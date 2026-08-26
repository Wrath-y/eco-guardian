package recovery

import (
	"context"
	"reflect"
	"testing"
)

func TestDeterministicStageAdapterKeepsModuleOrderAndAggregatesRefusals(t *testing.T) {
	calls := []string{}
	scanner := func(name string, result ScanResult) ScannerFunc {
		return func(context.Context) (ScanResult, error) {
			calls = append(calls, name)
			return result, nil
		}
	}
	adapter := DeterministicStageAdapter{
		Impact:     scanner("impact", ScanResult{Scanned: 1, Recovered: 1}),
		Simulation: scanner("simulation", ScanResult{Scanned: 2, Recovered: 1, RecoveryRequired: 1}),
		Risk:       scanner("risk", ScanResult{Scanned: 1, RecoveryRequired: 1}),
	}
	result, err := adapter.ScanAndRecover(context.Background())
	if err != nil || !reflect.DeepEqual(calls, []string{"impact", "simulation", "risk"}) || result.Scanned != 4 || result.Recovered != 2 || result.RecoveryRequired != 2 {
		t.Fatalf("calls=%v result=%#v err=%v", calls, result, err)
	}
}
