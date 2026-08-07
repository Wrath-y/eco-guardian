package diff

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
)

type noActiveBaseline struct{}

func (noActiveBaseline) ActiveBaseline(context.Context) (domain.ID, bool, error) {
	return "", false, nil
}

type countingManifestReader struct{ calls int }

func (r *countingManifestReader) Materialize(context.Context, domain.ID) ([]EntityBlob, error) {
	r.calls++
	return nil, nil
}

type manifestsByRevision map[domain.ID][]EntityBlob

func (m manifestsByRevision) Materialize(_ context.Context, revisionID domain.ID) ([]EntityBlob, error) {
	return m[revisionID], nil
}

type generatedDiffFixture struct {
	entities  []domain.ID
	revisions []domain.ID
	maxRead   int
}

func newGeneratedDiffFixture(t *testing.T, entityCount, revisionCount int) *generatedDiffFixture {
	t.Helper()
	fixture := &generatedDiffFixture{entities: make([]domain.ID, entityCount), revisions: make([]domain.ID, revisionCount)}
	for index := range fixture.entities {
		id, err := domain.NewID()
		if err != nil {
			t.Fatal(err)
		}
		fixture.entities[index] = id
	}
	sort.Slice(fixture.entities, func(i, j int) bool { return fixture.entities[i] < fixture.entities[j] })
	for index := range fixture.revisions {
		id, err := domain.NewID()
		if err != nil {
			t.Fatal(err)
		}
		fixture.revisions[index] = id
	}
	return fixture
}

func (f *generatedDiffFixture) MaterializeSegment(_ context.Context, revisionID, afterEntityID domain.ID, limit int) ([]EntityBlob, error) {
	start := 0
	if afterEntityID != "" {
		start = sort.Search(len(f.entities), func(index int) bool { return f.entities[index] > afterEntityID })
	}
	end := start + limit
	if end > len(f.entities) {
		end = len(f.entities)
	}
	if count := end - start; count > f.maxRead {
		f.maxRead = count
	}
	changed := revisionID == f.revisions[len(f.revisions)-1]
	entities := make([]EntityBlob, 0, end-start)
	for index := start; index < end; index++ {
		value := index
		if changed && index%2_500 == 0 {
			value++
		}
		entities = append(entities, EntityBlob{EntityID: f.entities[index], Kind: domain.KindTag, Status: domain.StatusActive, JSON: []byte(fmt.Sprintf(`{"key":"entity-%05d","payload":{"value":%d}}`, index, value))})
	}
	return entities, nil
}

func TestCompareTraversesObjectsWithRFC6901AndStableOrder(t *testing.T) {
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	base := []EntityBlob{{EntityID: id, Kind: domain.KindTag, Status: domain.StatusActive, JSON: []byte(`{"z":1,"payload":{"a/b~c":1,"removed":true},"name":"old"}`)}}
	target := []EntityBlob{{EntityID: id, Kind: domain.KindTag, Status: domain.StatusActive, JSON: []byte(`{"name":"new","payload":{"a/b~c":2,"added":false},"z":1}`)}}
	changes, err := Compare(base, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 4 {
		t.Fatalf("changes=%#v", changes)
	}
	want := []struct {
		path string
		kind ChangeKind
	}{{"/name", Modify}, {"/payload/added", Add}, {"/payload/a~1b~0c", Modify}, {"/payload/removed", Delete}}
	for index, expected := range want {
		if changes[index].Path != expected.path || changes[index].Kind != expected.kind {
			t.Fatalf("change[%d]=%#v want=%#v", index, changes[index], expected)
		}
	}
	if string(changes[0].OldValue) != `"old"` || string(changes[0].NewValue) != `"new"` {
		t.Fatalf("values=%#v", changes[0])
	}
}

func TestCompareReportsEntityAddDeleteInIDOrder(t *testing.T) {
	first, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if first > second {
		first, second = second, first
	}
	changes, err := Compare([]EntityBlob{{EntityID: second, Kind: domain.KindTag, Status: domain.StatusActive, JSON: []byte(`{"id":"second"}`)}}, []EntityBlob{{EntityID: first, Kind: domain.KindTag, Status: domain.StatusActive, JSON: []byte(`{"id":"first"}`)}})
	if err != nil || len(changes) != 2 || changes[0].EntityID != first || changes[0].Kind != Add || changes[1].EntityID != second || changes[1].Kind != Delete {
		t.Fatalf("changes=%#v err=%v", changes, err)
	}
}

func TestCompareArraysUsesDeclaredIdentityLCSMoveAndNestedModify(t *testing.T) {
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	base := []EntityBlob{{EntityID: id, Kind: domain.KindCharacter, Status: domain.StatusActive, JSON: []byte(`{"payload":{"attribute_values":[{"output_attribute_id":"a","value":1},{"output_attribute_id":"b","value":2},{"output_attribute_id":"c","value":3}]}}`)}}
	target := []EntityBlob{{EntityID: id, Kind: domain.KindCharacter, Status: domain.StatusActive, JSON: []byte(`{"payload":{"attribute_values":[{"output_attribute_id":"b","value":20},{"output_attribute_id":"a","value":1},{"output_attribute_id":"c","value":3}]}}`)}}
	changes, err := Compare(base, target)
	if err != nil {
		t.Fatal(err)
	}
	foundMove, foundModify := false, false
	for _, change := range changes {
		if change.Kind == Move && change.Path == "/payload/attribute_values/1" && change.OldOrdinal != nil && *change.OldOrdinal == 0 && change.NewOrdinal != nil && *change.NewOrdinal == 1 {
			foundMove = true
		}
		if change.Kind == Modify && change.Path == "/payload/attribute_values/0/value" && string(change.OldValue) == "2" && string(change.NewValue) == "20" {
			foundModify = true
		}
	}
	if !foundMove || !foundModify {
		t.Fatalf("changes=%#v", changes)
	}
}

func TestComparePreservesUnknownNamespacedExtensionsAsJSON(t *testing.T) {
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	base := []EntityBlob{{EntityID: id, Kind: domain.KindTag, Status: domain.StatusActive, JSON: []byte(`{"extensions":{"vendor.example/rule":{"effect_ids":["x"],"opaque":{"enabled":false}}}}`)}}
	target := []EntityBlob{{EntityID: id, Kind: domain.KindTag, Status: domain.StatusActive, JSON: []byte(`{"extensions":{"vendor.example/rule":{"effect_ids":["x"],"opaque":{"enabled":true}}}}`)}}

	changes, err := Compare(base, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes=%#v", changes)
	}
	change := changes[0]
	if change.Kind != Modify || change.Path != "/extensions/vendor.example~1rule/opaque/enabled" || string(change.OldValue) != "false" || string(change.NewValue) != "true" {
		t.Fatalf("extension change=%#v", change)
	}
}

func TestCompareMatchesEntitiesByStableIDWhenDisplayNameAndKeyChange(t *testing.T) {
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	base := []EntityBlob{{EntityID: id, Kind: domain.KindTag, Status: domain.StatusActive, JSON: []byte(`{"key":"old-key","name":"Old display name"}`)}}
	target := []EntityBlob{{EntityID: id, Kind: domain.KindTag, Status: domain.StatusActive, JSON: []byte(`{"key":"new-key","name":"New display name"}`)}}

	changes, err := Compare(base, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Fatalf("changes=%#v", changes)
	}
	for _, change := range changes {
		if change.EntityID != id || change.Kind != Modify || (change.Path != "/key" && change.Path != "/name") {
			t.Fatalf("display/key change=%#v", change)
		}
	}
}

func TestCompareAgainstActiveBaselineReportsNoBaselineWithoutFallback(t *testing.T) {
	target, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	manifests := &countingManifestReader{}
	comparison, err := CompareAgainstActiveBaseline(context.Background(), manifests, noActiveBaseline{}, target)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.BaselineState != NoBaseline || comparison.BaseRevisionID != "" || comparison.TargetRevisionID != target || comparison.Changes != nil {
		t.Fatalf("comparison=%#v", comparison)
	}
	if manifests.calls != 0 {
		t.Fatalf("materialized %d revisions without an active baseline", manifests.calls)
	}
}

func TestCompareGoldenTombstoneAndReferenceRenameRemainFieldChanges(t *testing.T) {
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	base := []EntityBlob{{EntityID: id, Kind: domain.KindTag, Status: domain.StatusActive, JSON: []byte(`{"status":"active","payload":{"reference":{"id":"stable-reference","display_name":"Old","key":"old"}}}`)}}
	target := []EntityBlob{{EntityID: id, Kind: domain.KindTag, Status: domain.StatusArchived, JSON: []byte(`{"status":"archived","payload":{"reference":{"id":"stable-reference","display_name":"New","key":"new"}}}`)}}

	changes, err := Compare(base, target)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		path string
		old  string
		new  string
	}{
		{"/payload/reference/display_name", `"Old"`, `"New"`},
		{"/payload/reference/key", `"old"`, `"new"`},
		{"/status", `"active"`, `"archived"`},
	}
	if len(changes) != len(want) {
		t.Fatalf("changes=%#v", changes)
	}
	for index, expected := range want {
		change := changes[index]
		if change.EntityID != id || change.Kind != Modify || change.Path != expected.path || string(change.OldValue) != expected.old || string(change.NewValue) != expected.new {
			t.Fatalf("change[%d]=%#v want=%#v", index, change, expected)
		}
	}
}

func TestCompareArraysCoversInsertDeleteAndDuplicateIdentityFallback(t *testing.T) {
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	t.Run("insert delete", func(t *testing.T) {
		base := []EntityBlob{{EntityID: id, Kind: domain.KindCharacter, Status: domain.StatusActive, JSON: []byte(`{"payload":{"attribute_values":[{"output_attribute_id":"a"},{"output_attribute_id":"b"}]}}`)}}
		target := []EntityBlob{{EntityID: id, Kind: domain.KindCharacter, Status: domain.StatusActive, JSON: []byte(`{"payload":{"attribute_values":[{"output_attribute_id":"a"},{"output_attribute_id":"c"}]}}`)}}
		changes, err := Compare(base, target)
		if err != nil {
			t.Fatal(err)
		}
		var added, deleted bool
		for _, change := range changes {
			if change.Path != "/payload/attribute_values/1" {
				continue
			}
			if change.Kind == Add && change.NewOrdinal != nil && *change.NewOrdinal == 1 {
				added = true
			}
			if change.Kind == Delete && change.OldOrdinal != nil && *change.OldOrdinal == 1 {
				deleted = true
			}
		}
		if !added || !deleted {
			t.Fatalf("changes=%#v", changes)
		}
	})
	t.Run("duplicate declared identity falls back for the whole array", func(t *testing.T) {
		base := []EntityBlob{{EntityID: id, Kind: domain.KindCharacter, Status: domain.StatusActive, JSON: []byte(`{"payload":{"attribute_values":[{"output_attribute_id":"same","value":1},{"output_attribute_id":"same","value":2}]}}`)}}
		target := []EntityBlob{{EntityID: id, Kind: domain.KindCharacter, Status: domain.StatusActive, JSON: []byte(`{"payload":{"attribute_values":[{"output_attribute_id":"same","value":2},{"output_attribute_id":"same","value":1}]}}`)}}
		changes, err := Compare(base, target)
		if err != nil {
			t.Fatal(err)
		}
		foundMove := false
		for _, change := range changes {
			if change.Kind == Move {
				foundMove = true
			}
			if change.Path == "/payload/attribute_values/0/value" || change.Path == "/payload/attribute_values/1/value" {
				t.Fatalf("duplicate identity was treated as stable: %#v", changes)
			}
		}
		if !foundMove {
			t.Fatalf("changes=%#v", changes)
		}
	})
}

func TestCompareIsStableForRepeatedUnkeyedValuesAndShuffledManifestOrder(t *testing.T) {
	first, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	base := []EntityBlob{
		{EntityID: first, Kind: domain.KindTag, Status: domain.StatusActive, JSON: []byte(`{"payload":{"values":["x","x","y"],"object":{"alpha":1,"beta":2}}}`)},
		{EntityID: second, Kind: domain.KindSkill, Status: domain.StatusActive, JSON: []byte(`{"name":"old","key":"old-key"}`)},
	}
	target := []EntityBlob{
		{EntityID: first, Kind: domain.KindTag, Status: domain.StatusActive, JSON: []byte(`{"payload":{"object":{"beta":3,"alpha":1},"values":["x","y","x"]}}`)},
		{EntityID: second, Kind: domain.KindSkill, Status: domain.StatusActive, JSON: []byte(`{"key":"new-key","name":"new"}`)},
	}
	want, err := Compare(base, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatal("expected repeated-value and scalar changes")
	}
	for iteration := 0; iteration < 32; iteration++ {
		shuffledBase := []EntityBlob{base[iteration%2], base[(iteration+1)%2]}
		shuffledTarget := []EntityBlob{target[(iteration+1)%2], target[iteration%2]}
		got, err := Compare(shuffledBase, shuffledTarget)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("iteration=%d changes=%#v want=%#v", iteration, got, want)
		}
	}
}

func TestCompareRevisionsReturnsEmptyForDistinctSameContentRevisions(t *testing.T) {
	baseID, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	targetID, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	entityID, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	entities := []EntityBlob{{EntityID: entityID, Kind: domain.KindTag, Status: domain.StatusActive, JSON: []byte(`{"key":"same","name":"Same"}`)}}
	changes, err := CompareRevisions(context.Background(), manifestsByRevision{baseID: entities, targetID: entities}, baseID, targetID)
	if err != nil || len(changes) != 0 {
		t.Fatalf("changes=%#v err=%v", changes, err)
	}
}

func TestCompareRevisionSegmentBoundsGeneratedTenThousandEntityFixture(t *testing.T) {
	fixture := newGeneratedDiffFixture(t, 10_000, 200)
	baseID, targetID := fixture.revisions[0], fixture.revisions[len(fixture.revisions)-1]
	var after domain.ID
	changes := 0
	for segments := 0; ; segments++ {
		segment, err := CompareRevisionSegment(context.Background(), fixture, baseID, targetID, after, DefaultEntitySegmentSize)
		if err != nil {
			t.Fatal(err)
		}
		changes += len(segment.Changes)
		if segment.NextEntityCursor == "" {
			if segments != 49 {
				t.Fatalf("segments=%d, want 50", segments+1)
			}
			break
		}
		after = segment.NextEntityCursor
	}
	if fixture.maxRead > DefaultEntitySegmentSize+1 {
		t.Fatalf("segment read=%d, want at most %d", fixture.maxRead, DefaultEntitySegmentSize+1)
	}
	if changes != 4 {
		t.Fatalf("changes=%d, want 4", changes)
	}
}
