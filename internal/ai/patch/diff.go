package patch

import (
	"bytes"
	"encoding/json"
	"io"
	"sort"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
)

type DiffResult struct {
	Diff      aicontract.DraftDiff
	Canonical []byte
	Hash      aicontract.Hash
}

// BuildDiff projects only a previously validated canonical Patch against the
// original values pinned in its decode context. Collection changes show the
// complete resulting field value rather than an ambiguous array instruction.
func BuildDiff(value aicontract.DraftPatchV1, scope DecodeContext) (DiffResult, error) {
	expectedHash, err := aicontract.HashDraftPatch(value)
	if err != nil || expectedHash != value.Hash || value.ID != scope.PatchID || value.Base != scope.Input.Base {
		return DiffResult{}, decodeError(ErrorIdentityMismatch)
	}
	byPath := make(map[string]ValueScope, len(scope.Values))
	for _, item := range scope.Values {
		byPath[string(item.EntityID)+"\x00"+string(item.Path)] = item
	}
	changes := []aicontract.DraftDiffChange{}
	for _, target := range value.Targets {
		for _, operation := range target.Operations {
			item, found := byPath[string(target.EntityID)+"\x00"+string(operation.Path)]
			if !found {
				return DiffResult{}, decodeError(ErrorScopeViolation)
			}
			original, err := aicontract.CanonicalToolPayload(item.Original)
			if err != nil {
				return DiffResult{}, decodeError(ErrorSchemaViolation)
			}
			canonical, err := canonicalChange(item.Original, operation)
			if err != nil {
				return DiffResult{}, err
			}
			if bytes.Equal(original, canonical) {
				return DiffResult{}, decodeError(ErrorNoChange)
			}
			changes = append(changes, aicontract.DraftDiffChange{
				EntityID: target.EntityID, Path: operation.Path, Ordinal: operation.Ordinal, Kind: operation.Kind,
				Original: original, Canonical: canonical,
			})
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].EntityID != changes[j].EntityID {
			return changes[i].EntityID < changes[j].EntityID
		}
		if changes[i].Path != changes[j].Path {
			return changes[i].Path < changes[j].Path
		}
		return changes[i].Ordinal < changes[j].Ordinal
	})
	diff := aicontract.DraftDiff{PatchHash: value.Hash, Changes: changes}
	canonical, err := aicontract.CanonicalDraftDiff(diff)
	if err != nil {
		return DiffResult{}, decodeError(ErrorSchemaViolation)
	}
	hash, err := aicontract.HashDraftDiff(diff)
	if err != nil {
		return DiffResult{}, decodeError(ErrorSchemaViolation)
	}
	return DiffResult{Diff: diff, Canonical: canonical, Hash: hash}, nil
}

func canonicalChange(original json.RawMessage, operation aicontract.DraftOperation) (json.RawMessage, error) {
	if operation.Kind == aicontract.OperationReplace {
		canonical, err := aicontract.CanonicalToolPayload(operation.Value)
		if err != nil {
			return nil, decodeError(ErrorSchemaViolation)
		}
		return canonical, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(original))
	decoder.UseNumber()
	var values []any
	if decoder.Decode(&values) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, decodeError(ErrorCollectionViolation)
	}
	var proposed any
	decoder = json.NewDecoder(bytes.NewReader(operation.Value))
	decoder.UseNumber()
	if decoder.Decode(&proposed) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, decodeError(ErrorCollectionViolation)
	}
	if operation.Kind == aicontract.OperationAdd {
		values = append(values, proposed)
	} else if operation.Kind == aicontract.OperationRemove {
		index := -1
		proposedJSON, _ := json.Marshal(proposed)
		proposedCanonical, _ := aicontract.CanonicalToolPayload(proposedJSON)
		for candidateIndex, candidate := range values {
			candidateJSON, _ := json.Marshal(candidate)
			candidateCanonical, _ := aicontract.CanonicalToolPayload(candidateJSON)
			if bytes.Equal(candidateCanonical, proposedCanonical) {
				if index >= 0 {
					return nil, decodeError(ErrorArrayAmbiguous)
				}
				index = candidateIndex
			}
		}
		if index < 0 {
			return nil, decodeError(ErrorArrayAmbiguous)
		}
		values = append(values[:index], values[index+1:]...)
	} else {
		return nil, decodeError(ErrorCollectionViolation)
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, decodeError(ErrorSchemaViolation)
	}
	canonical, err := aicontract.CanonicalToolPayload(encoded)
	if err != nil {
		return nil, decodeError(ErrorSchemaViolation)
	}
	return canonical, nil
}
