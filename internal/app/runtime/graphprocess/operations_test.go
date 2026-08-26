package graphprocess

import (
	"reflect"
	"testing"
)

func TestDefaultOperationRegistryIsClosedOrderedAndDetached(t *testing.T) {
	registry := DefaultOperationRegistry()
	descriptors := registry.Descriptors()
	want := []OperationID{
		OperationActivation, OperationCoreQuery, OperationExplicitRebuild, OperationGraphFTSReadiness,
		OperationRetrievalBM25, OperationRetrievalRerank, OperationRetrievalVector, OperationSnapshotLifecycle, OperationTaskPolling,
	}
	got := make([]OperationID, len(descriptors))
	for index := range descriptors {
		got[index] = descriptors[index].ID
		if !reflect.DeepEqual(descriptors[index].RequiredAPIVersions, []string{"v1"}) || !reflect.DeepEqual(descriptors[index].RequiredSchemas, []string{"1.0"}) {
			t.Fatalf("descriptor=%#v", descriptors[index])
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ids=%v want=%v", got, want)
	}
	descriptors[0].RequiredAPIVersions[0] = "mutated"
	detached, _ := registry.Descriptor(OperationActivation)
	if detached.RequiredAPIVersions[0] != "v1" {
		t.Fatal("registry descriptor was mutated through accessor")
	}
}

func TestOperationDescriptorsBindSpecificHealthInputs(t *testing.T) {
	registry := DefaultOperationRegistry()
	rebuild, ok := registry.Descriptor(OperationExplicitRebuild)
	if !ok || !reflect.DeepEqual(rebuild.RequiredCapabilities, []string{"snapshot_lifecycle", "task_polling"}) || !reflect.DeepEqual(rebuild.RequiredLimits, []LimitRequirement{{Name: "rebuild_components", Minimum: 1}}) {
		t.Fatalf("rebuild=%#v", rebuild)
	}
	graphFTS, _ := registry.Descriptor(OperationGraphFTSReadiness)
	if !reflect.DeepEqual(graphFTS.RequiredDependencies, []string{"sqlite", "graph_migrations", "core_graph_query", "bm25"}) {
		t.Fatalf("graph FTS=%#v", graphFTS)
	}
	vector, _ := registry.Descriptor(OperationRetrievalVector)
	if !reflect.DeepEqual(vector.RequiredDependencies, []string{"sqlite", "graph_migrations", "core_graph_query", "vector"}) {
		t.Fatalf("vector=%#v", vector)
	}
}

func TestOperationRegistryRejectsDuplicateAndIncompleteDescriptors(t *testing.T) {
	valid := OperationDescriptor{ID: OperationCoreQuery, RequiredAPIVersions: []string{"v1"}, RequiredSchemas: []string{"1.0"}}
	for _, descriptors := range [][]OperationDescriptor{{}, {valid, valid}, {{ID: OperationCoreQuery}}} {
		if _, err := NewOperationRegistry(descriptors); err == nil {
			t.Fatalf("registry accepted %#v", descriptors)
		}
	}
}
