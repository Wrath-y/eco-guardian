package planner

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/impact"
	versioningdiff "github.com/zouyi/eco-guardian/internal/versioning/diff"
)

type manifestFake map[domain.ID][]versioningdiff.EntityBlob

func (f manifestFake) Materialize(_ context.Context, revisionID domain.ID) ([]versioningdiff.EntityBlob, error) {
	return append([]versioningdiff.EntityBlob(nil), f[revisionID]...), nil
}

func entityBlob(id domain.ID, kind domain.EntityKind, status domain.EntityStatus, value string) versioningdiff.EntityBlob {
	return versioningdiff.EntityBlob{EntityID: id, Kind: kind, Status: status, JSON: json.RawMessage(value)}
}

func TestChangedSetIsStableAndRetainsTombstonesAndFilteredStarts(t *testing.T) {
	input := normalizedFixture(t)
	input.Filters.NodeTypes = []string{"skill"}
	changedID, _ := domain.NewID()
	deletedID, _ := domain.NewID()
	filteredID, _ := domain.NewID()
	base := []versioningdiff.EntityBlob{entityBlob(changedID, domain.KindSkill, domain.StatusActive, `{"name":"old","payload":{"x":1}}`), entityBlob(deletedID, domain.KindItem, domain.StatusActive, `{"name":"gone"}`), entityBlob(filteredID, domain.KindAttribute, domain.StatusActive, `{"name":"old"}`)}
	target := []versioningdiff.EntityBlob{entityBlob(filteredID, domain.KindAttribute, domain.StatusActive, `{"name":"new"}`), entityBlob(changedID, domain.KindSkill, domain.StatusActive, `{"name":"new","payload":{"x":2}}`)}
	reader := manifestFake{input.Base.RevisionID: base, input.Target.RevisionID: target}
	targetEntities := []domain.Entity{{ID: filteredID, Kind: domain.KindAttribute, Status: domain.StatusActive}, {ID: changedID, Kind: domain.KindSkill, Status: domain.StatusActive}}
	result, err := ChangedSet(context.Background(), reader, input, targetEntities)
	if err != nil || len(result) != 3 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	for index := 1; index < len(result); index++ {
		if result[index-1].EntityID > result[index].EntityID {
			t.Fatalf("unstable order=%#v", result)
		}
	}
	byID := map[domain.ID]impact.ChangedEntity{}
	for _, item := range result {
		byID[item.EntityID] = item
	}
	if !byID[changedID].QueryEligible || byID[deletedID].Ineligibility != "not_in_target_graph" || byID[filteredID].Ineligibility != "filtered_node_type" || len(byID[changedID].FieldPaths) < 2 {
		t.Fatalf("changed=%#v", byID)
	}
}

func TestEqualContentNamedCheckpointsReturnEmptyChangedSet(t *testing.T) {
	input := normalizedFixture(t)
	entityID, _ := domain.NewID()
	rows := []versioningdiff.EntityBlob{entityBlob(entityID, domain.KindSkill, domain.StatusActive, `{"name":"same"}`)}
	result, err := ChangedSet(context.Background(), manifestFake{input.Base.RevisionID: rows, input.Target.RevisionID: rows}, input, nil)
	if err != nil || len(result) != 0 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}
