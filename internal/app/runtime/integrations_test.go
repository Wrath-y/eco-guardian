package runtime

import "testing"

func TestUnavailableIntegrationDescriptorsCoverUnappliedModules(t *testing.T) {
	got := UnavailableIntegrationDescriptors()
	want := []ModuleID{
		ModuleGraphProjectionSync,
		ModuleDependencyImpactAnalysis,
		ModuleReproducibleSimulation,
		ModuleBalanceRiskAssessment,
		ModuleAIBalanceDesignWorkflow,
	}
	if len(got) != len(want) {
		t.Fatalf("descriptor count = %d, want %d", len(got), len(want))
	}
	for index, descriptor := range got {
		if descriptor.ID != want[index] || descriptor.Availability != IntegrationUnavailable || descriptor.Reason != "MODULE_NOT_INSTALLED" {
			t.Fatalf("descriptor %d = %#v", index, descriptor)
		}
	}
	got[0].Reason = "mutated"
	if next := UnavailableIntegrationDescriptors(); next[0].Reason != "MODULE_NOT_INSTALLED" {
		t.Fatal("callers must not mutate the descriptor catalog")
	}
}
