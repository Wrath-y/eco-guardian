package structure

import (
	"encoding/json"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
	"github.com/zouyi/eco-guardian/internal/validation"
	versioningdiff "github.com/zouyi/eco-guardian/internal/versioning/diff"
)

func TestStructuralRuleCapacityAtFiveHundredChangedFormulaUnits(t *testing.T) {
	changes := make([]versioningdiff.FieldChange, 500)
	baseline := make([]validation.FormulaIndexRecord, 500)
	candidate := make([]validation.FormulaIndexRecord, 500)
	for index := range changes {
		entity := capacityStructureID(index + 1)
		output := capacityStructureID(index + 501)
		path := "/payload/formula"
		baseline[index] = formulaRecord(t, entity, output, path, binary("*", selector("self", "power"), selector("self", "power")))
		candidate[index] = formulaRecord(t, entity, output, path, binary("*", binary("*", selector("self", "power"), selector("self", "power")), selector("self", "power")))
		changes[index] = versioningdiff.FieldChange{EntityID: entity, EntityKind: domain.KindCharacter, Path: path, Kind: versioningdiff.Modify, OldValue: json.RawMessage(`"old"`), NewValue: json.RawMessage(`"new"`)}
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	started := time.Now()
	findings := Analyze(changes, baseline, candidate, IndexContract{ASTVersion: formula.ASTSchemaVersion, DSLVersion: formula.DSLVersion, RegistryVersion: "registry-v1"}, V1Rules())
	elapsed := time.Since(started)
	runtime.ReadMemStats(&after)
	payload, err := json.Marshal(findings)
	if err != nil {
		t.Fatal(err)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	if len(findings) != 1000 || len(payload) > 8<<20 || allocated > 128<<20 || elapsed > 2*time.Second {
		t.Fatalf("findings=%d payload=%d allocated=%d elapsed=%s", len(findings), len(payload), allocated, elapsed)
	}
	for index := 1; index < len(findings); index++ {
		if findings[index].ID < findings[index-1].ID {
			t.Fatalf("findings are not deterministically ordered at %d", index)
		}
	}
	t.Logf("risk-structure changed_units=500 findings=%d elapsed=%s allocated_bytes=%d payload_bytes=%d", len(findings), elapsed, allocated, len(payload))
}

func capacityStructureID(value int) domain.ID {
	return domain.ID(fmt.Sprintf("01948c1e-0000-7000-8000-%012d", value))
}
