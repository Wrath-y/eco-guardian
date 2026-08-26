package projector

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
)

type Reference struct {
	SourceID, TargetID domain.ID
	FieldPath          string
	Ordinal            int
	TargetKind         domain.EntityKind
}
type Revision struct {
	ProjectID, RevisionID domain.ID
	ConfigHash            string
	Entities              []domain.Entity
	References            []Reference
}
type Node struct {
	ID, Type, Label, Text string
	Properties            map[string]any
	Provenance            NodeProvenance
}
type Edge struct {
	ID, From, To, Type, RelationKind string
	Confidence                       int
	Properties                       map[string]any
	Provenance                       EdgeProvenance
}
type NodeProvenance struct {
	ProjectID, RevisionID, ConfigHash, EntityID, EntityKind, ProjectionSchema, Projector string
	EntitySchemaVersion                                                                  int
}
type EdgeProvenance struct {
	ProjectID, RevisionID, ConfigHash, SourceEntityID, FieldPath, ProjectionSchema, Projector string
	Ordinal                                                                                   int
}
type Result struct {
	Nodes []Node
	Edges []Edge
}

func Materialize(revision Revision) ([]domain.Entity, error) {
	if !revision.ProjectID.Valid() || !revision.RevisionID.Valid() || !validHash(revision.ConfigHash) {
		return nil, fmt.Errorf("invalid immutable revision")
	}
	entities := append([]domain.Entity(nil), revision.Entities...)
	sort.Slice(entities, func(i, j int) bool { return entities[i].ID < entities[j].ID })
	seen := map[domain.ID]struct{}{}
	for _, entity := range entities {
		if !entity.ID.Valid() || !entity.Kind.Valid() {
			return nil, fmt.Errorf("invalid entity")
		}
		if _, exists := seen[entity.ID]; exists {
			return nil, fmt.Errorf("duplicate entity %s", entity.ID)
		}
		seen[entity.ID] = struct{}{}
	}
	return entities, nil
}
func Project(descriptor Descriptor, revision Revision) (Result, error) {
	if !descriptor.Valid() {
		return Result{}, fmt.Errorf("invalid projector descriptor")
	}
	entities, err := Materialize(revision)
	if err != nil {
		return Result{}, err
	}
	byID := map[domain.ID]domain.Entity{}
	ids := map[domain.ID]string{}
	nodes := []Node{}
	for _, entity := range entities {
		byID[entity.ID] = entity
		if entity.Status == domain.StatusArchived {
			continue
		}
		id := NodeID(revision.ProjectID, entity)
		label, text, properties, formatErr := descriptor.Formatter.Format(entity)
		if formatErr != nil {
			return Result{}, formatErr
		}
		if _, err := json.Marshal(properties); err != nil {
			return Result{}, fmt.Errorf("non-canonical node properties: %w", err)
		}
		ids[entity.ID] = id
		nodes = append(nodes, Node{ID: id, Type: string(entity.Kind), Label: label, Text: text, Properties: properties, Provenance: NodeProvenance{ProjectID: string(revision.ProjectID), RevisionID: string(revision.RevisionID), ConfigHash: revision.ConfigHash, EntityID: string(entity.ID), EntityKind: string(entity.Kind), EntitySchemaVersion: entity.SchemaVersion, ProjectionSchema: string(descriptor.SchemaVersion), Projector: string(descriptor.Version)}})
	}
	edges := []Edge{}
	edgeIDs := map[string]struct{}{}
	for _, reference := range revision.References {
		token, ok := relationFor(descriptor.Relations, byID[reference.SourceID], reference)
		if !ok {
			continue
		}
		source, sourceOK := byID[reference.SourceID]
		target, targetOK := byID[reference.TargetID]
		if !sourceOK || !targetOK || target.Kind != reference.TargetKind || source.Status == domain.StatusArchived || target.Status == domain.StatusArchived || ids[source.ID] == "" || ids[target.ID] == "" {
			return Result{}, fmt.Errorf("invalid edge endpoint")
		}
		id := EdgeID(ids[source.ID], token, ids[target.ID], reference.FieldPath, reference.Ordinal)
		if _, exists := edgeIDs[id]; exists {
			return Result{}, fmt.Errorf("duplicate edge %s", id)
		}
		edgeIDs[id] = struct{}{}
		edges = append(edges, Edge{ID: id, From: ids[source.ID], To: ids[target.ID], Type: string(token), RelationKind: "explicit", Confidence: 1, Properties: map[string]any{}, Provenance: EdgeProvenance{ProjectID: string(revision.ProjectID), RevisionID: string(revision.RevisionID), ConfigHash: revision.ConfigHash, SourceEntityID: string(source.ID), FieldPath: reference.FieldPath, Ordinal: reference.Ordinal, ProjectionSchema: string(descriptor.SchemaVersion), Projector: string(descriptor.Version)}})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	return Result{Nodes: nodes, Edges: edges}, nil
}

// NodeID is the shared v1 projection identity builder. Consumers that need to
// address an entity in an exact Graph snapshot must use this function instead
// of reproducing the URN format.
func NodeID(projectID domain.ID, entity domain.Entity) string {
	return "urn:eco:" + string(projectID) + ":" + string(entity.Kind) + ":" + string(entity.ID)
}
func EdgeID(from string, token RelationToken, to, path string, ordinal int) string {
	sum := sha256.Sum256([]byte(from + "|" + string(token) + "|" + to + "|" + path + "|" + strconv.Itoa(ordinal)))
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:])
}
func relationFor(relations []RelationDescriptor, source domain.Entity, ref Reference) (RelationToken, bool) {
	for _, relation := range relations {
		if ref.TargetKind != relation.TargetKind || !matchesKind(source.Kind, relation.SourceKinds) || !matchPointer(ref.FieldPath, relation.PathPattern) {
			continue
		}
		return relation.Token, true
	}
	return "", false
}
func matchesKind(kind domain.EntityKind, values []domain.EntityKind) bool {
	for _, value := range values {
		if value == kind {
			return true
		}
	}
	return false
}
func matchPointer(value, pattern string) bool {
	a, b := strings.Split(value, "/"), strings.Split(pattern, "/")
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if b[i] != "*" && a[i] != b[i] {
			return false
		}
		if b[i] == "*" {
			if _, err := strconv.Atoi(a[i]); err != nil {
				return false
			}
		}
	}
	return true
}
func validHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
