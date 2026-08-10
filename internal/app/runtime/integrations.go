// Package runtime coordinates process-owned services through narrow module and
// platform ports. It does not own project business facts or module repositories.
package runtime

// ModuleID identifies a module that may contribute process-level observations
// and recovery adapters. IDs are stable because runtime status persists them in
// diagnostics and clients use them to explain an unavailable integration.
type ModuleID string

const (
	ModuleGraphProjectionSync      ModuleID = "graph-projection-sync"
	ModuleDependencyImpactAnalysis ModuleID = "dependency-impact-analysis"
	ModuleReproducibleSimulation   ModuleID = "reproducible-simulation"
	ModuleBalanceRiskAssessment    ModuleID = "balance-risk-assessment"
	ModuleAIBalanceDesignWorkflow  ModuleID = "ai-balance-design-workflow"
)

// IntegrationAvailability is deliberately narrower than a capability result.
// It reports whether the owning module has supplied an adapter at all; runtime
// must never infer module readiness or business facts on its behalf.
type IntegrationAvailability string

const IntegrationUnavailable IntegrationAvailability = "unavailable"

// IntegrationDescriptor is safe to expose to runtime status before an owning
// module has been applied. Reason is a stable code, not an implementation
// error, path, provider response, or business payload.
type IntegrationDescriptor struct {
	ID           ModuleID
	Availability IntegrationAvailability
	Reason       string
}

// UnavailableIntegrationDescriptors records the #8–#12 integration seams
// absent from this checkout. The returned slice is independent from the
// package-level list so callers cannot mutate future status observations.
func UnavailableIntegrationDescriptors() []IntegrationDescriptor {
	return []IntegrationDescriptor{
		{ID: ModuleGraphProjectionSync, Availability: IntegrationUnavailable, Reason: "MODULE_NOT_INSTALLED"},
		{ID: ModuleDependencyImpactAnalysis, Availability: IntegrationUnavailable, Reason: "MODULE_NOT_INSTALLED"},
		{ID: ModuleReproducibleSimulation, Availability: IntegrationUnavailable, Reason: "MODULE_NOT_INSTALLED"},
		{ID: ModuleBalanceRiskAssessment, Availability: IntegrationUnavailable, Reason: "MODULE_NOT_INSTALLED"},
		{ID: ModuleAIBalanceDesignWorkflow, Availability: IntegrationUnavailable, Reason: "MODULE_NOT_INSTALLED"},
	}
}
