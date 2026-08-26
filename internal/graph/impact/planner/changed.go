package planner

import (
	"context"
	"sort"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/impact"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
	versioningdiff "github.com/zouyi/eco-guardian/internal/versioning/diff"
)

func ChangedSet(ctx context.Context, reader impact.DiffReader, input impact.Input, targetEntities []domain.Entity) ([]impact.ChangedEntity, error) {
	changes, err := versioningdiff.CompareRevisions(ctx, reader, input.Base.RevisionID, input.Target.RevisionID)
	if err != nil {
		return nil, err
	}
	byID := map[domain.ID]*impact.ChangedEntity{}
	for _, change := range changes {
		entry := byID[change.EntityID]
		if entry == nil {
			entry = &impact.ChangedEntity{EntityID: change.EntityID, Kind: change.EntityKind, ChangeKind: change.Kind}
			byID[change.EntityID] = entry
		}
		entry.ChangeKind = dominantChange(entry.ChangeKind, change.Kind)
		entry.FieldPaths = append(entry.FieldPaths, change.Path)
	}
	entities := make(map[domain.ID]domain.Entity, len(targetEntities))
	for _, entity := range targetEntities {
		entities[entity.ID] = entity
	}
	result := make([]impact.ChangedEntity, 0, len(byID))
	for _, entry := range byID {
		entry.FieldPaths = normalizeSet(entry.FieldPaths)
		entity, exists := entities[entry.EntityID]
		if exists && entity.Status != domain.StatusArchived && matchesNodeFilter(string(entity.Kind), input.Filters.NodeTypes) {
			entry.TargetNodeID = projector.NodeID(input.ProjectID, entity)
			entry.QueryEligible = true
		} else if !exists || entity.Status == domain.StatusArchived {
			entry.Ineligibility = "not_in_target_graph"
		} else {
			entry.Ineligibility = "filtered_node_type"
		}
		result = append(result, *entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].EntityID < result[j].EntityID })
	return result, nil
}

func dominantChange(current, candidate versioningdiff.ChangeKind) versioningdiff.ChangeKind {
	order := map[versioningdiff.ChangeKind]int{versioningdiff.Modify: 1, versioningdiff.Move: 2, versioningdiff.Add: 3, versioningdiff.Delete: 4}
	if order[candidate] > order[current] {
		return candidate
	}
	return current
}

func matchesNodeFilter(kind string, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, filter := range filters {
		if filter == kind {
			return true
		}
	}
	return false
}
