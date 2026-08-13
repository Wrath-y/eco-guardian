package projector

// MayFallbackToFull is intentionally code-only: only a missing or unready
// base can retry the same target identity as full. Target conflicts, schema,
// hash, and integrity failures must remain terminal.
func MayFallbackToFull(providerCode string) bool {
	return providerCode == "BASE_SNAPSHOT_NOT_FOUND" || providerCode == "BASE_SNAPSHOT_NOT_READY"
}
