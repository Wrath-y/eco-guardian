package diff

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
	versioning "github.com/zouyi/eco-guardian/internal/versioning"
)

var ErrTargetRevisionInvalid = fmt.Errorf("target revision is invalid")
var ErrSegmentLimitInvalid = fmt.Errorf("diff segment limit must be between 1 and %d", MaxEntitySegmentSize)

// CompareRevisions reads two immutable manifests and compares stable entity
// identities. Readers own project/unreadable identity checks.
func CompareRevisions(ctx context.Context, reader ManifestReader, baseID, targetID domain.ID) ([]FieldChange, error) {
	base, err := reader.Materialize(ctx, baseID)
	if err != nil {
		return nil, err
	}
	target, err := reader.Materialize(ctx, targetID)
	if err != nil {
		return nil, err
	}
	return Compare(base, target)
}

// CompareRevisionSegment produces at most limit stable-entity ranges of a
// diff. It reads limit+1 entities per manifest to establish continuation and
// never reaches into working state or a writer transaction.
func CompareRevisionSegment(ctx context.Context, reader ManifestSegmentReader, baseID, targetID, afterEntityID domain.ID, limit int) (Segment, error) {
	if !baseID.Valid() || !targetID.Valid() || (afterEntityID != "" && !afterEntityID.Valid()) {
		return Segment{}, ErrTargetRevisionInvalid
	}
	if limit < 1 || limit > MaxEntitySegmentSize {
		return Segment{}, ErrSegmentLimitInvalid
	}
	readLimit := limit + 1
	base, err := reader.MaterializeSegment(ctx, baseID, afterEntityID, readLimit)
	if err != nil {
		return Segment{}, err
	}
	target, err := reader.MaterializeSegment(ctx, targetID, afterEntityID, readLimit)
	if err != nil {
		return Segment{}, err
	}
	byID, err := combineEntities(base, target)
	if err != nil {
		return Segment{}, err
	}
	ids := sortedEntityIDs(byID)
	more := len(ids) > limit
	if more {
		ids = ids[:limit]
	}
	changes, err := compareEntityIDs(byID, ids)
	if err != nil {
		return Segment{}, err
	}
	segment := Segment{Changes: changes}
	if more {
		segment.NextEntityCursor = ids[len(ids)-1]
	}
	return segment, nil
}

// CompareAgainstActiveBaseline compares a candidate only when an active
// release supplies its base. A missing pointer is an explicit first-release
// state and is never converted into an empty change list or a fallback base.
func CompareAgainstActiveBaseline(ctx context.Context, manifests ManifestReader, baselines ActiveBaselineReader, targetID domain.ID) (Comparison, error) {
	if !targetID.Valid() {
		return Comparison{}, ErrTargetRevisionInvalid
	}
	baseID, found, err := baselines.ActiveBaseline(ctx)
	if err != nil {
		return Comparison{}, err
	}
	if !found {
		return Comparison{BaselineState: NoBaseline, TargetRevisionID: targetID}, nil
	}
	if !baseID.Valid() {
		return Comparison{}, fmt.Errorf("active baseline revision is invalid")
	}
	changes, err := CompareRevisions(ctx, manifests, baseID, targetID)
	if err != nil {
		return Comparison{}, err
	}
	return Comparison{BaselineState: BaselineAvailable, BaseRevisionID: baseID, TargetRevisionID: targetID, Changes: changes}, nil
}

// Compare emits deterministic ADD/DELETE/MODIFY facts. Array identity and
// MOVE detection are intentionally delegated to the later schema-aware pass.
func Compare(base, target []EntityBlob) ([]FieldChange, error) {
	byID, err := combineEntities(base, target)
	if err != nil {
		return nil, err
	}
	return compareEntityIDs(byID, sortedEntityIDs(byID))
}

type entityPair struct{ base, target *EntityBlob }

func combineEntities(base, target []EntityBlob) (map[domain.ID]entityPair, error) {
	byID := map[domain.ID]entityPair{}
	for index := range base {
		if !base[index].EntityID.Valid() {
			return nil, fmt.Errorf("invalid base entity identity")
		}
		entry := byID[base[index].EntityID]
		if entry.base != nil {
			return nil, fmt.Errorf("duplicate base entity %s", base[index].EntityID)
		}
		entry.base = &base[index]
		byID[base[index].EntityID] = entry
	}
	for index := range target {
		if !target[index].EntityID.Valid() {
			return nil, fmt.Errorf("invalid target entity identity")
		}
		entry := byID[target[index].EntityID]
		if entry.target != nil {
			return nil, fmt.Errorf("duplicate target entity %s", target[index].EntityID)
		}
		entry.target = &target[index]
		byID[target[index].EntityID] = entry
	}
	return byID, nil
}

func sortedEntityIDs(byID map[domain.ID]entityPair) []domain.ID {
	ids := make([]domain.ID, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func compareEntityIDs(byID map[domain.ID]entityPair, ids []domain.ID) ([]FieldChange, error) {
	changes := make([]FieldChange, 0)
	for _, id := range ids {
		entry := byID[id]
		switch {
		case entry.base == nil:
			changes = append(changes, FieldChange{EntityID: id, EntityKind: entry.target.Kind, Path: "/", Kind: Add, NewValue: append(json.RawMessage(nil), entry.target.JSON...)})
		case entry.target == nil:
			changes = append(changes, FieldChange{EntityID: id, EntityKind: entry.base.Kind, Path: "/", Kind: Delete, OldValue: append(json.RawMessage(nil), entry.base.JSON...)})
		default:
			if entry.base.Kind != entry.target.Kind {
				return nil, fmt.Errorf("entity %s changes kind", id)
			}
			var oldValue, newValue any
			if err := decodeJSON(entry.base.JSON, &oldValue); err != nil {
				return nil, err
			}
			if err := decodeJSON(entry.target.JSON, &newValue); err != nil {
				return nil, err
			}
			if err := compareValue(id, entry.base.Kind, "", oldValue, newValue, &changes); err != nil {
				return nil, err
			}
		}
	}
	sortChanges(changes)
	return changes, nil
}

func decodeJSON(raw []byte, into *any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(into); err != nil {
		return fmt.Errorf("decode diff JSON: %w", err)
	}
	return nil
}

func compareValue(id domain.ID, kind domain.EntityKind, path string, oldValue, newValue any, changes *[]FieldChange) error {
	oldObject, oldIsObject := oldValue.(map[string]any)
	newObject, newIsObject := newValue.(map[string]any)
	if oldIsObject && newIsObject {
		keys := map[string]struct{}{}
		for key := range oldObject {
			keys[key] = struct{}{}
		}
		for key := range newObject {
			keys[key] = struct{}{}
		}
		ordered := make([]string, 0, len(keys))
		for key := range keys {
			ordered = append(ordered, key)
		}
		sort.Slice(ordered, func(i, j int) bool { return bytes.Compare([]byte(ordered[i]), []byte(ordered[j])) < 0 })
		for _, key := range ordered {
			nextPath := path + "/" + escapePointer(key)
			oldChild, oldOK := oldObject[key]
			newChild, newOK := newObject[key]
			switch {
			case !oldOK:
				raw, err := canonical(newChild)
				if err != nil {
					return err
				}
				*changes = append(*changes, FieldChange{EntityID: id, EntityKind: kind, Path: nextPath, Kind: Add, NewValue: raw})
			case !newOK:
				raw, err := canonical(oldChild)
				if err != nil {
					return err
				}
				*changes = append(*changes, FieldChange{EntityID: id, EntityKind: kind, Path: nextPath, Kind: Delete, OldValue: raw})
			default:
				if err := compareValue(id, kind, nextPath, oldChild, newChild, changes); err != nil {
					return err
				}
			}
		}
		return nil
	}
	oldArray, oldIsArray := oldValue.([]any)
	newArray, newIsArray := newValue.([]any)
	if oldIsArray && newIsArray {
		return compareArray(id, kind, path, oldArray, newArray, changes)
	}
	oldRaw, err := canonical(oldValue)
	if err != nil {
		return err
	}
	newRaw, err := canonical(newValue)
	if err != nil {
		return err
	}
	if bytes.Equal(oldRaw, newRaw) {
		return nil
	}
	if path == "" {
		path = "/"
	}
	*changes = append(*changes, FieldChange{EntityID: id, EntityKind: kind, Path: path, Kind: Modify, OldValue: oldRaw, NewValue: newRaw})
	return nil
}

var declaredArrayIdentity = map[string]string{
	"/payload/attribute_values": "output_attribute_id",
	"/payload/trigger_blocks":   "id",
	"/payload/rule_blocks":      "id",
	"/payload/modifiers":        "id",
}

func compareArray(id domain.ID, kind domain.EntityKind, path string, oldArray, newArray []any, changes *[]FieldChange) error {
	oldTokens, oldKeyed, err := arrayTokens(path, oldArray)
	if err != nil {
		return err
	}
	newTokens, newKeyed, err := arrayTokens(path, newArray)
	if err != nil {
		return err
	}
	oldIndex, newIndex := tokenIndexes(oldTokens), tokenIndexes(newTokens)
	pairs := lcsPairs(oldTokens, newTokens)
	inLCS := map[string]bool{}
	for _, pair := range pairs {
		inLCS[oldTokens[pair[0]]] = true
	}
	for oldOrdinal, token := range oldTokens {
		newOrdinal, exists := newIndex[token]
		if !exists {
			raw, err := canonical(oldArray[oldOrdinal])
			if err != nil {
				return err
			}
			ordinal := oldOrdinal
			*changes = append(*changes, FieldChange{EntityID: id, EntityKind: kind, Path: arrayPath(path, oldOrdinal), Kind: Delete, OldValue: raw, OldOrdinal: &ordinal})
			continue
		}
		if !inLCS[token] {
			oldValue, newValue := oldOrdinal, newOrdinal
			*changes = append(*changes, FieldChange{EntityID: id, EntityKind: kind, Path: arrayPath(path, newOrdinal), Kind: Move, OldOrdinal: &oldValue, NewOrdinal: &newValue})
		}
		if oldKeyed[token] && newKeyed[token] {
			if err := compareValue(id, kind, arrayPath(path, newOrdinal), oldArray[oldOrdinal], newArray[newOrdinal], changes); err != nil {
				return err
			}
		}
	}
	for newOrdinal, token := range newTokens {
		if _, exists := oldIndex[token]; exists {
			continue
		}
		raw, err := canonical(newArray[newOrdinal])
		if err != nil {
			return err
		}
		ordinal := newOrdinal
		*changes = append(*changes, FieldChange{EntityID: id, EntityKind: kind, Path: arrayPath(path, newOrdinal), Kind: Add, NewValue: raw, NewOrdinal: &ordinal})
	}
	return nil
}

func arrayTokens(path string, values []any) ([]string, map[string]bool, error) {
	tokens, keyed := make([]string, len(values)), map[string]bool{}
	identityField := declaredArrayIdentity[path]
	if identityField != "" {
		candidateTokens := make([]string, len(values))
		seenKeys := map[string]bool{}
		allUnique := true
		for index, value := range values {
			object, ok := value.(map[string]any)
			if !ok {
				allUnique = false
				break
			}
			candidate, present := object[identityField]
			if !present {
				allUnique = false
				break
			}
			raw, err := canonical(candidate)
			if err != nil {
				return nil, nil, err
			}
			token := "key:" + string(raw)
			if seenKeys[token] {
				allUnique = false
				break
			}
			seenKeys[token] = true
			candidateTokens[index] = token
		}
		if allUnique {
			for index, token := range candidateTokens {
				tokens[index], keyed[token] = token, true
			}
			return tokens, keyed, nil
		}
	}
	occurrences := map[string]int{}
	for index, value := range values {
		raw, err := canonical(value)
		if err != nil {
			return nil, nil, err
		}
		hash := versioning.SHA256(raw)
		occurrence := occurrences[hash]
		occurrences[hash]++
		tokens[index] = fmt.Sprintf("hash:%s:%d", hash, occurrence)
	}
	return tokens, keyed, nil
}

func tokenIndexes(tokens []string) map[string]int {
	indexes := make(map[string]int, len(tokens))
	for index, token := range tokens {
		indexes[token] = index
	}
	return indexes
}
func arrayPath(path string, ordinal int) string { return fmt.Sprintf("%s/%d", path, ordinal) }

func lcsPairs(oldTokens, newTokens []string) [][2]int {
	dp := make([][]int, len(oldTokens)+1)
	for index := range dp {
		dp[index] = make([]int, len(newTokens)+1)
	}
	for i := len(oldTokens) - 1; i >= 0; i-- {
		for j := len(newTokens) - 1; j >= 0; j-- {
			if oldTokens[i] == newTokens[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	pairs := make([][2]int, 0, dp[0][0])
	for i, j := 0, 0; i < len(oldTokens) && j < len(newTokens); {
		if oldTokens[i] == newTokens[j] {
			pairs = append(pairs, [2]int{i, j})
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			i++
		} else {
			j++
		}
	}
	return pairs
}

func canonical(value any) (json.RawMessage, error) {
	raw, err := versioning.CanonicalJSON(value)
	return json.RawMessage(raw), err
}
func escapePointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}
func sortChanges(changes []FieldChange) {
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].EntityID != changes[j].EntityID {
			return changes[i].EntityID < changes[j].EntityID
		}
		if changes[i].Path != changes[j].Path {
			return bytes.Compare([]byte(changes[i].Path), []byte(changes[j].Path)) < 0
		}
		return changes[i].Kind < changes[j].Kind
	})
}
