package threshold

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
)

func TestInactiveStarterV1HasExactCanonicalBoundariesAndGoldenHash(t *testing.T) {
	starter := StarterFixtureV1()
	if !starter.Valid() || starter.Enabled || len(starter.Body.Entries) != 20 {
		t.Fatalf("starter=%#v", starter)
	}
	for _, entry := range starter.Body.Entries {
		if entry.Relative.Warning != "0.10" || entry.Relative.Block != "0.25" || entry.Absolute != nil {
			t.Fatalf("starter guessed or changed a boundary: %#v", entry)
		}
	}
	body, err := starter.Body.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"warning":"0.10"`) || !strings.Contains(string(body), `"block":"0.25"`) || !strings.Contains(string(body), `"absolute":null`) {
		t.Fatalf("starter canonical body lost exact decimal/null values: %s", body)
	}
	wantHash := "1483e3397e02eb9d8ce1bb91a070a80018682db569c6dc7f2a8d9bf3d82c4df2"
	if starter.BodyHash != wantHash {
		t.Fatalf("starter body hash=%s", starter.BodyHash)
	}
}

func TestScopeResolutionUsesExactGroupThenNullDefaultAndSingletonOnlyDefault(t *testing.T) {
	group := "alpha"
	body := testBody([]Entry{
		testEntry(nil, "ratio", riskcontract.TargetRange, "0.10", "0.25"),
		testEntry(&group, "ratio", riskcontract.TargetRange, "0.05", "0.20"),
	})
	scope := riskcontract.ThresholdScope{SceneID: "scene", SceneVersion: "v1", MetricID: "metric-resource", MetricVersion: "v1", BalanceGroup: &group, Unit: "ratio", Direction: riskcontract.TargetRange}
	resolution, err := Resolve(body, scope, false)
	if err != nil || resolution.Entry.Relative.Warning != "0.05" || resolution.Evidence.Priority != "exact_balance_group" {
		t.Fatalf("resolution=%#v err=%v", resolution, err)
	}
	resolution, err = Resolve(body, scope, true)
	if err != nil || resolution.Entry.Relative.Warning != "0.10" || resolution.Evidence.Priority != "project_default" {
		t.Fatalf("singleton resolution=%#v err=%v", resolution, err)
	}
	otherGroup := "beta"
	scope.BalanceGroup = &otherGroup
	resolution, err = Resolve(body, scope, false)
	if err != nil || resolution.Entry.Relative.Warning != "0.10" {
		t.Fatalf("fallback resolution=%#v err=%v", resolution, err)
	}
	scope.Unit = "seconds"
	if _, err = Resolve(body, scope, false); !errors.Is(err, ErrThresholdInvalid) {
		t.Fatalf("incompatible unit err=%v", err)
	}
	scope.Unit = "ratio"
	scope.MetricID = "missing"
	missing, err := Resolve(body, scope, false)
	if !errors.Is(err, ErrThresholdNotConfigured) || len(missing.Evidence.TriedKeys) != 2 {
		t.Fatalf("missing=%#v err=%v", missing, err)
	}
}

type thresholdClock struct{ now time.Time }

func (clock thresholdClock) Now() time.Time { return clock.now }

type thresholdIDs struct {
	ids   []domain.ID
	index int
}

func (ids *thresholdIDs) New() (domain.ID, error) {
	if ids.index >= len(ids.ids) {
		return "", errors.New("no test ID")
	}
	id := ids.ids[ids.index]
	ids.index++
	return id, nil
}

type thresholdMemoryRepository struct {
	versions []Version
	replays  map[string]struct {
		hash    string
		version Version
	}
	createCalls int
}

func (repository *thresholdMemoryRepository) GetExactEnabled(_ context.Context, projectID domain.ID, identity riskcontract.Identity) (Version, bool, error) {
	for _, version := range repository.versions {
		if version.ProjectID == projectID && string(version.ID) == identity.ID && fmt.Sprint(version.DisplayVersion) == identity.Version && version.BodyHash == identity.Hash && version.Enabled {
			return version, true, nil
		}
	}
	return Version{}, false, nil
}

func (repository *thresholdMemoryRepository) CreateEnabled(_ context.Context, request CreateRequest) (Version, bool, error) {
	repository.createCalls++
	if replay, found := repository.replays[request.IdempotencyKey]; found {
		if replay.hash != request.RequestHash {
			return Version{}, false, ErrIdempotencyConflict
		}
		return replay.version, true, nil
	}
	version, err := NewVersion(request.ProjectID, request.ProposedID, int64(len(repository.versions)+1), request.Origin, true, request.Body, request.CreatedBy, request.CreatedAt)
	if err != nil {
		return Version{}, false, err
	}
	repository.versions = append(repository.versions, version)
	repository.replays[request.IdempotencyKey] = struct {
		hash    string
		version Version
	}{request.RequestHash, version}
	return version, false, nil
}

func TestSelectionRequiresExplicitConfirmationAndIsIdempotent(t *testing.T) {
	projectID := domain.ID("01948c1e-0000-7000-8000-000000000001")
	repository := &thresholdMemoryRepository{replays: make(map[string]struct {
		hash    string
		version Version
	})}
	ids := &thresholdIDs{ids: []domain.ID{"01948c1e-0000-7000-8000-000000000002", "01948c1e-0000-7000-8000-000000000003", "01948c1e-0000-7000-8000-000000000004", "01948c1e-0000-7000-8000-000000000005"}}
	service := Service{Repository: repository, Clock: thresholdClock{time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)}, IDs: ids, Starter: StarterFixtureV1()}
	command := SelectionCommand{ProjectID: projectID, Kind: riskcontract.StarterThreshold, CreatedBy: "local-user", IdempotencyKey: "enable-starter"}
	if _, _, err := service.Select(context.Background(), command); !errors.Is(err, ErrThresholdNotConfigured) || repository.createCalls != 0 {
		t.Fatalf("unconfirmed starter err=%v calls=%d", err, repository.createCalls)
	}
	command.Confirmed = true
	first, replayed, err := service.Select(context.Background(), command)
	if err != nil || replayed || !first.Enabled || first.Origin != OriginStarter || first.DisplayVersion != 1 {
		t.Fatalf("first=%#v replayed=%v err=%v", first, replayed, err)
	}
	second, replayed, err := service.Select(context.Background(), command)
	if err != nil || !replayed || second.ID != first.ID || second.BodyHash != first.BodyHash || len(repository.versions) != 1 {
		t.Fatalf("second=%#v replayed=%v err=%v versions=%d", second, replayed, err, len(repository.versions))
	}
	modified := first.Body
	modified.Entries = append([]Entry(nil), first.Body.Entries...)
	modified.Entries[0].Relative.Warning = "0.11"
	modifiedCommand := SelectionCommand{ProjectID: projectID, Kind: riskcontract.ModifiedStarter, ModifiedBody: &modified, Confirmed: true, CreatedBy: "local-user", IdempotencyKey: "enable-starter"}
	if _, _, err = service.Select(context.Background(), modifiedCommand); !errors.Is(err, ErrIdempotencyConflict) || len(repository.versions) != 1 {
		t.Fatalf("changed same-key err=%v versions=%d", err, len(repository.versions))
	}
	modifiedCommand.IdempotencyKey = "modified-starter"
	third, replayed, err := service.Select(context.Background(), modifiedCommand)
	if err != nil || replayed || third.ID == first.ID || third.BodyHash == first.BodyHash || third.DisplayVersion != 2 || repository.versions[0].Body.Entries[0].Relative.Warning != "0.10" {
		t.Fatalf("third=%#v replayed=%v err=%v", third, replayed, err)
	}
	existing := riskcontract.Identity{ID: string(third.ID), Version: fmt.Sprint(third.DisplayVersion), Hash: third.BodyHash}
	selected, reused, err := service.Select(context.Background(), SelectionCommand{ProjectID: projectID, Kind: riskcontract.ExistingThreshold, Existing: &existing, IdempotencyKey: "select-existing"})
	if err != nil || !reused || selected.ID != third.ID || len(repository.versions) != 2 {
		t.Fatalf("selected=%#v reused=%v err=%v", selected, reused, err)
	}
}

func testBody(entries []Entry) Body {
	return Body{SchemaVersion: SchemaVersionV1, Source: "test", Assumptions: []string{}, Entries: entries, StructuralRuleVersions: []riskcontract.Identity{{ID: "structure", Version: "v1", Hash: strings.Repeat("a", 64)}}}
}

func testEntry(group *string, unit string, direction riskcontract.MetricDirection, warning, block string) Entry {
	return Entry{SceneID: "scene", SceneVersion: "v1", MetricID: "metric-resource", MetricVersion: "v1", BalanceGroup: group, Unit: unit, Direction: direction, Relative: Boundaries{Warning: warning, Block: block}}
}
