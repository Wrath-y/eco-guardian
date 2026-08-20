package sync

import "strings"

type SnapshotExpectation struct {
	Namespace, Version, ContentHash string
	NodeCount, EdgeCount            int
}

type SnapshotVerification struct {
	Ready             bool
	Warnings, Reasons []string
}

// VerifySnapshot accepts only the exact immutable target identity. Provider
// warning text is never copied into durable Graph state; optional components
// are represented by stable local warning codes instead.
func VerifySnapshot(expected SnapshotExpectation, snapshot Snapshot) SnapshotVerification {
	result := SnapshotVerification{}
	if expected.Namespace == "" || expected.Version == "" || !validHash(expected.ContentHash) || expected.NodeCount < 0 || expected.EdgeCount < 0 {
		return SnapshotVerification{Reasons: []string{"INVALID_EXPECTED_SNAPSHOT"}}
	}
	if snapshot.Namespace != expected.Namespace || snapshot.Version != expected.Version {
		result.Reasons = append(result.Reasons, "SNAPSHOT_IDENTITY_MISMATCH")
	}
	if snapshot.ContentHash != expected.ContentHash {
		result.Reasons = append(result.Reasons, "CONTENT_HASH_MISMATCH")
	}
	if snapshot.NodeCount != expected.NodeCount || snapshot.EdgeCount != expected.EdgeCount {
		result.Reasons = append(result.Reasons, "SNAPSHOT_COUNT_MISMATCH")
	}
	if snapshot.Status != "ready" {
		result.Reasons = append(result.Reasons, "SNAPSHOT_NOT_READY")
	}
	if !snapshot.QueryReady {
		result.Reasons = append(result.Reasons, "QUERY_NOT_READY")
	}
	for _, component := range []string{"graph", "fts"} {
		if componentState(snapshot.Components, component) != "ready" {
			result.Reasons = append(result.Reasons, "COMPONENT_"+strings.ToUpper(component)+"_NOT_READY")
		}
	}
	if vector := componentState(snapshot.Components, "vector"); vector != "" && vector != "ready" {
		result.Warnings = append(result.Warnings, "DEGRADED_VECTOR")
	}
	result.Ready = len(result.Reasons) == 0
	return result
}

func componentState(components []Component, name string) string {
	for _, component := range components {
		if component.Name == name {
			return component.State
		}
	}
	return ""
}
