package planner

import (
	"errors"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/impact"
)

func normalizedFixture(t *testing.T) impact.Input {
	t.Helper()
	project, _ := domain.NewID()
	base, _ := domain.NewID()
	target, _ := domain.NewID()
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	return impact.Input{ProjectID: project, Base: impact.RevisionIdentity{RevisionID: base, ConfigHash: hash, VersionManifestHash: hash, GraphManifestHash: hash}, Target: impact.RevisionIdentity{RevisionID: target, ConfigHash: hash, VersionManifestHash: hash, GraphManifestHash: hash}}
}

func TestDecodeCommandRejectsDuplicateUnknownAndEmptyFilters(t *testing.T) {
	for _, data := range []string{
		`{"project_uuid":"x","project_uuid":"y"}`,
		`{"unknown":true}`,
	} {
		if _, err := DecodeCommand([]byte(data)); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("data=%s err=%v", data, err)
		}
	}
	input := normalizedFixture(t)
	input.Filters.NodeTypes = []string{""}
	if _, _, _, err := Normalize(input); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty filter error=%v", err)
	}
}

func TestNormalizeExpandsDefaultsAndCollidesEquivalentSets(t *testing.T) {
	first := normalizedFixture(t)
	second := first
	second.Filters.RelationshipKinds = []string{"explicit", "explicit"}
	second.Filters.NodeTypes = []string{"skill", "attribute", "skill"}
	first.Filters.NodeTypes = []string{"attribute", "skill"}
	a, bytesA, hashA, err := Normalize(first)
	if err != nil {
		t.Fatal(err)
	}
	b, bytesB, hashB, err := Normalize(second)
	if err != nil {
		t.Fatal(err)
	}
	if hashA != hashB || string(bytesA) != string(bytesB) || a.Limits.MaxDepth != 3 || a.Limits.MaxNodes != 500 || b.Filters.Direction != impact.DirectionIncoming {
		t.Fatalf("normalization mismatch a=%#v b=%#v hashes=%s/%s", a, b, hashA, hashB)
	}
}

func TestNormalizeChangesHashForEverySemanticIdentity(t *testing.T) {
	input := normalizedFixture(t)
	_, _, baseline, err := Normalize(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Filters.Direction = impact.DirectionOutgoing
	_, _, changed, err := Normalize(input)
	if err != nil || changed == baseline {
		t.Fatalf("changed hash=%q baseline=%q err=%v", changed, baseline, err)
	}
	input.Limits.MaxDepth = 7
	if _, _, _, err = Normalize(input); err != ErrLimitExceeded {
		t.Fatalf("limit error=%v", err)
	}
}
