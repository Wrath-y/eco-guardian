package fixture

import (
	"reflect"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
)

func TestFixedV1CoversTheVersionedSimulationSurface(t *testing.T) {
	fixture := FixedV1()
	if fixture.Version != V1 || len(fixture.Entities) != 6 || len(fixture.Templates) != 4 || len(fixture.Metrics) != 5 || len(fixture.SimultaneousEvents) != 2 || fixture.SimultaneousEvents[0].TimeMS != fixture.SimultaneousEvents[1].TimeMS {
		t.Fatalf("fixture=%+v", fixture)
	}
	wantKinds := []domain.EntityKind{domain.KindAttribute, domain.KindTag, domain.KindCharacter, domain.KindSkill, domain.KindItem, domain.KindEffect}
	gotKinds := make([]domain.EntityKind, len(fixture.Entities))
	for index, entity := range fixture.Entities {
		if !entity.ID.Valid() || entity.Status != domain.StatusActive {
			t.Fatalf("invalid fixed entity: %+v", entity)
		}
		gotKinds[index] = entity.Kind
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Fatalf("kinds=%v", gotKinds)
	}
	decimal, err := formula.ParseDecimal(fixture.DecimalBoundary)
	if err != nil || decimal.String() == "" || fixture.DecimalUnit != "damage_point" || fixture.TriggerEvent != "on_damage" || fixture.TriggerBudgetMS != "1" || fixture.StackMax != "2" || fixture.StackCap != "10" || len(fixture.UnavailableMissing) != 1 {
		t.Fatalf("fixture boundary values are not canonical: %+v err=%v", fixture, err)
	}
}
