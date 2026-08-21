package gate

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/simulation/contract"
	"github.com/zouyi/eco-guardian/internal/validation"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

type validationRevisionFake struct{ record versioningrevision.Record }

func (f validationRevisionFake) GetRevisionRecord(context.Context, domain.ID) (versioningrevision.Record, error) {
	return f.record, nil
}

type validationGateFake struct{ result validation.GateResult }

func (f validationGateFake) Check(context.Context, domain.ID, string, validation.VersionManifest) (validation.GateResult, error) {
	return f.result, nil
}

func TestFullValidationAdapterRequiresExactPass(t *testing.T) {
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	manifest := versioningrevision.VersionManifest{Entries: []versioningrevision.VersionEntry{{CapabilityID: "schema", ContractVersion: "v1", ImplementationVersion: "schema", State: versioningrevision.Registered}, {CapabilityID: "dsl", ContractVersion: "v1", ImplementationVersion: "dsl", State: versioningrevision.Registered}, {CapabilityID: "validator-registry", ContractVersion: "v1", ImplementationVersion: "registry", State: versioningrevision.Registered}, {CapabilityID: "numeric-policy", ContractVersion: "v1", ImplementationVersion: "numeric", State: versioningrevision.Registered}}}
	record := versioningrevision.Record{DisplayRevision: 1, Metadata: versioningrevision.Metadata{RevisionID: id, ConfigHash: strings.Repeat("a", 64), Manifest: manifest, ManifestHash: strings.Repeat("b", 64), CreatedAt: time.Now().UTC()}}
	if err = (FullValidationAdapter{Revisions: validationRevisionFake{record}, Gate: validationGateFake{validation.GatePass}}).RequireFull(context.Background(), contract.ID(id)); err != nil {
		t.Fatal(err)
	}
	if err = (FullValidationAdapter{Revisions: validationRevisionFake{record}, Gate: validationGateFake{validation.GateBlocked}}).RequireFull(context.Background(), contract.ID(id)); err == nil {
		t.Fatal("expected blocked validation rejection")
	}
}
