package release

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

func TestValidateCommandMaterializesExactImmutableInputsBeforePreflight(t *testing.T) {
	sources, command := validCommandSources(t, "")
	validated, err := ValidateCommand(context.Background(), sources, command)
	if err != nil {
		t.Fatal(err)
	}
	if validated.Candidate.RevisionID != command.CandidateRevisionID || validated.Candidate.BaselineReleaseID != "" || validated.RequestHash == "" {
		t.Fatalf("unexpected validated command: %#v", validated)
	}
	if got, want := sources.calls, []string{"revision", "policy", "pointer"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("read order=%v want %v", got, want)
	}
}

func TestValidateCommandRejectsUnknownAndMismatchedImmutableInputs(t *testing.T) {
	tests := []struct {
		name   string
		change func(*commandSources, *Command)
		want   error
	}{
		{"unknown revision", func(s *commandSources, _ *Command) { s.revisionErr = errors.New("missing") }, ErrCandidateUnknown},
		{"config hash mismatch", func(s *commandSources, c *Command) { c.ConfigHash = strings.Repeat("f", 64) }, ErrCandidateMismatch},
		{"manifest hash mismatch", func(s *commandSources, c *Command) { c.ManifestHash = strings.Repeat("e", 64) }, ErrCandidateMismatch},
		{"unknown policy", func(s *commandSources, _ *Command) { s.policyErr = errors.New("missing") }, ErrPolicyUnknown},
		{"invalid policy", func(s *commandSources, _ *Command) { s.policy.CanonicalHash = "invalid" }, ErrPolicyInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sources, command := validCommandSources(t, "")
			test.change(sources, &command)
			_, err := ValidateCommand(context.Background(), sources, command)
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v want %v", err, test.want)
			}
		})
	}
}

func TestValidateCommandRequiresExplicitAndCurrentBaseline(t *testing.T) {
	baseline := commandID(t)
	tests := []struct {
		name   string
		change func(*commandSources, *Command)
		want   error
	}{
		{"omitted first baseline", func(_ *commandSources, c *Command) { c.BaselineSpecified = false }, ErrCommandInvalid},
		{"first release baseline supplied", func(s *commandSources, c *Command) {
			c.BaselineReleaseID = baseline
			s.release = releaseRecord(t, baseline)
		}, ErrBaselineConflict},
		{"unknown baseline", func(s *commandSources, c *Command) {
			s.pointer.ReleaseID = baseline
			c.BaselineReleaseID = baseline
			s.releaseErr = errors.New("missing")
		}, ErrBaselineUnknown},
		{"different current pointer", func(s *commandSources, c *Command) {
			s.pointer.ReleaseID = commandID(t)
			c.BaselineReleaseID = baseline
			s.release = releaseRecord(t, baseline)
		}, ErrBaselineConflict},
		{"missing establish baseline confirmation", func(_ *commandSources, c *Command) { c.Confirmations = nil }, ErrBaselineConfirmation},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sources, command := validCommandSources(t, "")
			test.change(sources, &command)
			_, err := ValidateCommand(context.Background(), sources, command)
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v want %v", err, test.want)
			}
		})
	}
}

func TestCommandValidationRejectsNonCanonicalRequestFieldsAndCanonicalizesNullBaseline(t *testing.T) {
	_, command := validCommandSources(t, "")
	for _, change := range []func(*Command){
		func(c *Command) { c.IdempotencyKey = " key " },
		func(c *Command) { c.Notes = strings.Repeat("x", 10001) },
		func(c *Command) { c.Confirmations = append(c.Confirmations, c.Confirmations[0]) },
		func(c *Command) { c.Confirmations[0].Confirmed = false },
		func(c *Command) { c.Override = &OverrideAudit{GateResultID: commandID(t), Reason: "", Confirmed: true} },
	} {
		copy := command
		copy.Confirmations = append([]Confirmation(nil), command.Confirmations...)
		change(&copy)
		if copy.Valid() {
			t.Fatalf("invalid command accepted: %#v", copy)
		}
	}
	canonical, err := command.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(canonical), `"expected_baseline_release_id":null`) || strings.Contains(string(canonical), "Idempotency") {
		t.Fatalf("canonical request did not preserve explicit null baseline: %s", canonical)
	}
}

type commandSources struct {
	revision    versioningrevision.Record
	policy      versioningpolicy.ReleasePolicy
	release     Release
	pointer     ActivePointer
	revisionErr error
	policyErr   error
	releaseErr  error
	pointerErr  error
	calls       []string
}

func (s *commandSources) GetRevisionRecord(_ context.Context, _ domain.ID) (versioningrevision.Record, error) {
	s.calls = append(s.calls, "revision")
	return s.revision, s.revisionErr
}
func (s *commandSources) GetPolicy(_ context.Context, _ domain.ID) (versioningpolicy.ReleasePolicy, error) {
	s.calls = append(s.calls, "policy")
	return s.policy, s.policyErr
}
func (s *commandSources) GetRelease(_ context.Context, _ domain.ID) (Release, error) {
	s.calls = append(s.calls, "release")
	return s.release, s.releaseErr
}
func (s *commandSources) GetActivePointer(_ context.Context) (ActivePointer, error) {
	s.calls = append(s.calls, "pointer")
	return s.pointer, s.pointerErr
}

func validCommandSources(t *testing.T, baseline domain.ID) (*commandSources, Command) {
	t.Helper()
	revisionID, policyID := commandID(t), commandID(t)
	manifest := versioningrevision.VersionManifest{Entries: []versioningrevision.VersionEntry{{CapabilityID: "schema", ContractVersion: "schema-v1", ImplementationVersion: "schema-v1", State: versioningrevision.Registered}}}
	manifestHash, err := manifest.Hash()
	if err != nil {
		t.Fatal(err)
	}
	revision := versioningrevision.Record{DisplayRevision: 1, Metadata: versioningrevision.Metadata{RevisionID: revisionID, ConfigHash: strings.Repeat("a", 64), Manifest: manifest, ManifestHash: manifestHash, CreatedAt: time.Now().UTC()}}
	definition := versioningpolicy.Definition{Scenes: []versioningpolicy.Scene{{ID: "required", Required: true, Metrics: []versioningpolicy.Metric{{ID: "damage", Required: true}}}}, Samples: 1000, ThresholdID: "threshold-v1", ThresholdOn: true, Capabilities: []versioningpolicy.CapabilityRequirement{{CapabilityID: "validation", GateID: "full", ContractVersion: "validation-v1"}}}
	policy := versioningpolicy.ReleasePolicy{Definition: definition, ID: policyID, DisplayVersion: 1, CreatedAt: time.Now().UTC()}
	policy.CanonicalHash, err = policy.Hash()
	if err != nil {
		t.Fatal(err)
	}
	command := Command{CandidateRevisionID: revisionID, ConfigHash: revision.Metadata.ConfigHash, ManifestHash: manifestHash, PolicyID: policyID, BaselineReleaseID: baseline, BaselineSpecified: true, Confirmations: []Confirmation{{Kind: ConfirmationEstablishBaseline, Confirmed: true}}, IdempotencyKey: "release-key-1"}
	sources := &commandSources{revision: revision, policy: policy, pointer: ActivePointer{ReleaseID: baseline, Generation: 0}}
	if baseline != "" {
		sources.release = releaseRecord(t, baseline)
		command.Confirmations = nil
	}
	return sources, command
}

func releaseRecord(t *testing.T, id domain.ID) Release {
	t.Helper()
	return Release{ID: id, RevisionID: commandID(t), PolicyID: commandID(t), IntentID: commandID(t), CreatedAt: time.Now().UTC()}
}

func commandID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
