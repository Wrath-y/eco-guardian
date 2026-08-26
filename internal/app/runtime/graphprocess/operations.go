package graphprocess

import (
	"errors"
	"sort"
)

type OperationID string

const (
	OperationSnapshotLifecycle OperationID = "snapshot_lifecycle"
	OperationTaskPolling       OperationID = "task_polling"
	OperationActivation        OperationID = "activation"
	OperationCoreQuery         OperationID = "core_query"
	OperationGraphFTSReadiness OperationID = "graph_fts_readiness"
	OperationRetrievalBM25     OperationID = "retrieval_bm25"
	OperationRetrievalVector   OperationID = "retrieval_vector"
	OperationRetrievalRerank   OperationID = "retrieval_rerank"
	OperationExplicitRebuild   OperationID = "explicit_rebuild"
)

type LimitRequirement struct {
	Name    string
	Minimum int
}

type OperationDescriptor struct {
	ID                   OperationID
	RequiredAPIVersions  []string
	RequiredSchemas      []string
	RequiredCapabilities []string
	RequiredDependencies []string
	RequiredLimits       []LimitRequirement
}

type OperationRegistry struct {
	descriptors map[OperationID]OperationDescriptor
	ordered     []OperationID
}

func NewOperationRegistry(descriptors []OperationDescriptor) (*OperationRegistry, error) {
	if len(descriptors) == 0 {
		return nil, errors.New("operation registry is empty")
	}
	registry := &OperationRegistry{descriptors: map[OperationID]OperationDescriptor{}}
	for _, descriptor := range descriptors {
		if descriptor.ID == "" || registry.descriptors[descriptor.ID].ID != "" || len(descriptor.RequiredAPIVersions) == 0 || len(descriptor.RequiredSchemas) == 0 {
			return nil, errors.New("operation descriptor is invalid")
		}
		descriptor = cloneOperationDescriptor(descriptor)
		registry.descriptors[descriptor.ID] = descriptor
		registry.ordered = append(registry.ordered, descriptor.ID)
	}
	sort.Slice(registry.ordered, func(left, right int) bool { return registry.ordered[left] < registry.ordered[right] })
	return registry, nil
}

func DefaultOperationRegistry() *OperationRegistry {
	common := func(id OperationID) OperationDescriptor {
		return OperationDescriptor{ID: id, RequiredAPIVersions: []string{"v1"}, RequiredSchemas: []string{"1.0"}}
	}
	descriptors := []OperationDescriptor{}
	add := func(descriptor OperationDescriptor) { descriptors = append(descriptors, descriptor) }
	value := common(OperationSnapshotLifecycle)
	value.RequiredCapabilities = []string{"snapshot_lifecycle"}
	value.RequiredDependencies = []string{"sqlite", "graph_migrations"}
	add(value)
	value = common(OperationTaskPolling)
	value.RequiredCapabilities = []string{"task_polling"}
	add(value)
	value = common(OperationActivation)
	value.RequiredCapabilities = []string{"snapshot_lifecycle"}
	value.RequiredDependencies = []string{"sqlite"}
	add(value)
	value = common(OperationCoreQuery)
	value.RequiredDependencies = []string{"sqlite", "graph_migrations", "core_graph_query"}
	add(value)
	value = common(OperationGraphFTSReadiness)
	value.RequiredDependencies = []string{"sqlite", "graph_migrations", "core_graph_query", "bm25"}
	add(value)
	value = common(OperationRetrievalBM25)
	value.RequiredDependencies = []string{"sqlite", "graph_migrations", "core_graph_query", "bm25"}
	add(value)
	value = common(OperationRetrievalVector)
	value.RequiredDependencies = []string{"sqlite", "graph_migrations", "core_graph_query", "vector"}
	add(value)
	value = common(OperationRetrievalRerank)
	value.RequiredDependencies = []string{"sqlite", "graph_migrations", "core_graph_query", "rerank"}
	add(value)
	value = common(OperationExplicitRebuild)
	value.RequiredCapabilities = []string{"snapshot_lifecycle", "task_polling"}
	value.RequiredDependencies = []string{"sqlite", "graph_migrations"}
	value.RequiredLimits = []LimitRequirement{{Name: "rebuild_components", Minimum: 1}}
	add(value)
	registry, err := NewOperationRegistry(descriptors)
	if err != nil {
		panic(err)
	}
	return registry
}

func (registry *OperationRegistry) Descriptor(id OperationID) (OperationDescriptor, bool) {
	if registry == nil {
		return OperationDescriptor{}, false
	}
	descriptor, ok := registry.descriptors[id]
	return cloneOperationDescriptor(descriptor), ok
}

func (registry *OperationRegistry) Descriptors() []OperationDescriptor {
	if registry == nil {
		return []OperationDescriptor{}
	}
	result := make([]OperationDescriptor, 0, len(registry.ordered))
	for _, id := range registry.ordered {
		result = append(result, cloneOperationDescriptor(registry.descriptors[id]))
	}
	return result
}

func cloneOperationDescriptor(value OperationDescriptor) OperationDescriptor {
	value.RequiredAPIVersions = append([]string(nil), value.RequiredAPIVersions...)
	value.RequiredSchemas = append([]string(nil), value.RequiredSchemas...)
	value.RequiredCapabilities = append([]string(nil), value.RequiredCapabilities...)
	value.RequiredDependencies = append([]string(nil), value.RequiredDependencies...)
	value.RequiredLimits = append([]LimitRequirement(nil), value.RequiredLimits...)
	return value
}
