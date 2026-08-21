package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/simulation/contract"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

type fingerprintRevisionFake struct{ record versioningrevision.Record }

func (f fingerprintRevisionFake) GetRevisionRecord(context.Context, domain.ID) (versioningrevision.Record, error) {
	return f.record, nil
}

func TestSimulationFingerprintResolverUsesOnlyMatchingRevisionManifest(t *testing.T) {
	project, revision := mustSimulationID(), mustSimulationID()
	hash := strings.Repeat("a", 64)
	record := versioningrevision.Record{DisplayRevision: 1, Metadata: versioningrevision.Metadata{RevisionID: revision, ConfigHash: hash, ManifestHash: hash, CreatedAt: time.Now().UTC(), Manifest: versioningrevision.VersionManifest{Entries: []versioningrevision.VersionEntry{{CapabilityID: "schema", ContractVersion: "v1", ImplementationVersion: "v1", State: versioningrevision.Registered}, {CapabilityID: "dsl", ContractVersion: "v1", ImplementationVersion: "v1", State: versioningrevision.Registered}, {CapabilityID: "validator-registry", ContractVersion: "v1", ImplementationVersion: "v1", State: versioningrevision.Registered}, {CapabilityID: "numeric-policy", ContractVersion: "v1", ImplementationVersion: "v1", State: versioningrevision.Registered}}}}}
	registry, err := contract.NewManifestRegistry(contract.RequiredV1Descriptors, contract.V1Descriptors())
	if err != nil {
		t.Fatal(err)
	}
	resolver := SimulationFingerprintResolver{Revisions: fingerprintRevisionFake{record}, Registry: registry}
	input := contract.SimulationInputV1{SchemaVersion: contract.SimulationInputSchemaV1, ProjectID: contract.ID(project), RevisionID: contract.ID(revision), ConfigHash: hash, ManifestHash: hash, SceneID: "scene", SceneVersion: "v1", SceneBodyHash: hash, SampleCount: 1, Metrics: []contract.MetricIdentity{{ID: "metric-dps", Version: "v1"}}}
	if fingerprint, err := resolver.ResolveSimulationFingerprint(context.Background(), input); err != nil || len(fingerprint) != 64 {
		t.Fatalf("fingerprint=%q err=%v", fingerprint, err)
	}
	input.ConfigHash = strings.Repeat("b", 64)
	if _, err := resolver.ResolveSimulationFingerprint(context.Background(), input); err == nil {
		t.Fatal("expected captured revision mismatch")
	}
}
