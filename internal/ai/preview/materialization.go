package preview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aipatch "github.com/zouyi/eco-guardian/internal/ai/patch"
	"github.com/zouyi/eco-guardian/internal/domain"
)

const ProposalMaterializationVersionV1 = "proposal-materialization-v1"

var (
	ErrProposalMaterializationInvalid = errors.New("AI proposal materialization is invalid")
	ErrProposalBaseMismatch           = errors.New("AI proposal base does not match the frozen revision")
)

type ProposalBaseSnapshot struct {
	Base     aicontract.FrozenBaseIdentity
	Entities []domain.Entity
}

// ProposalBaseReader is intentionally read-only. It cannot save working
// entities, create revisions, insert formal reports, or evaluate a Gate.
type ProposalBaseReader interface {
	ReadProposalBase(context.Context, aicontract.FrozenBaseIdentity) (ProposalBaseSnapshot, error)
}

type ProposalMaterializationV1 struct {
	Version   string
	Base      aicontract.FrozenBaseIdentity
	PatchID   aicontract.PatchID
	PatchHash aicontract.Hash
	Entities  []domain.Entity
	Canonical []byte
	Hash      aicontract.Hash
}

func (v ProposalMaterializationV1) Valid() bool {
	if v.Version != ProposalMaterializationVersionV1 || !v.Base.Valid() || !v.PatchID.Valid() || !v.PatchHash.Valid() || len(v.Entities) == 0 || !v.Hash.Valid() {
		return false
	}
	canonical, hash, err := canonicalProposal(v.Base, v.PatchID, v.PatchHash, v.Entities)
	return err == nil && bytes.Equal(canonical, v.Canonical) && hash == v.Hash
}

func BuildProposalMaterializationV1(
	ctx context.Context,
	reader ProposalBaseReader,
	value aicontract.DraftPatchV1,
	diff aipatch.DiffResult,
) (ProposalMaterializationV1, error) {
	if ctx == nil || reader == nil || !value.Valid() || !diff.Hash.Valid() || diff.Diff.PatchHash != value.Hash {
		return ProposalMaterializationV1{}, ErrProposalMaterializationInvalid
	}
	patchHash, err := aicontract.HashDraftPatch(value)
	if err != nil || patchHash != value.Hash {
		return ProposalMaterializationV1{}, ErrProposalMaterializationInvalid
	}
	diffCanonical, err := aicontract.CanonicalDraftDiff(diff.Diff)
	if err != nil || !bytes.Equal(diffCanonical, diff.Canonical) {
		return ProposalMaterializationV1{}, ErrProposalMaterializationInvalid
	}
	diffHash, err := aicontract.HashDraftDiff(diff.Diff)
	if err != nil || diffHash != diff.Hash {
		return ProposalMaterializationV1{}, ErrProposalMaterializationInvalid
	}
	if !samePatchAndDiff(value, diff.Diff) {
		return ProposalMaterializationV1{}, ErrProposalMaterializationInvalid
	}
	snapshot, err := reader.ReadProposalBase(ctx, value.Base)
	if err != nil {
		return ProposalMaterializationV1{}, err
	}
	if snapshot.Base != value.Base {
		return ProposalMaterializationV1{}, ErrProposalBaseMismatch
	}
	entities, err := cloneProposalEntities(snapshot.Entities)
	if err != nil {
		return ProposalMaterializationV1{}, err
	}
	byID := make(map[aicontract.EntityID]int, len(entities))
	for index, entity := range entities {
		id := aicontract.EntityID(entity.ID)
		if !entity.ID.Valid() {
			return ProposalMaterializationV1{}, ErrProposalMaterializationInvalid
		}
		if _, duplicate := byID[id]; duplicate {
			return ProposalMaterializationV1{}, ErrProposalMaterializationInvalid
		}
		byID[id] = index
	}
	changed := map[aicontract.EntityID]struct{}{}
	for _, change := range diff.Diff.Changes {
		index, found := byID[change.EntityID]
		if !found {
			return ProposalMaterializationV1{}, ErrProposalBaseMismatch
		}
		document, err := entityValue(entities[index])
		if err != nil {
			return ProposalMaterializationV1{}, err
		}
		tokens, ok := proposalPointerTokens(string(change.Path))
		if !ok {
			return ProposalMaterializationV1{}, ErrProposalMaterializationInvalid
		}
		current, ok := proposalValueAt(document, tokens)
		if !ok {
			return ProposalMaterializationV1{}, ErrProposalBaseMismatch
		}
		currentJSON, _ := json.Marshal(current)
		currentCanonical, err := aicontract.CanonicalToolPayload(currentJSON)
		if err != nil || !bytes.Equal(currentCanonical, change.Original) {
			return ProposalMaterializationV1{}, ErrProposalBaseMismatch
		}
		next, err := proposalJSONValue(change.Canonical)
		if err != nil || !proposalSetValue(document, tokens, next) {
			return ProposalMaterializationV1{}, ErrProposalMaterializationInvalid
		}
		encoded, err := json.Marshal(document)
		if err != nil || json.Unmarshal(encoded, &entities[index]) != nil {
			return ProposalMaterializationV1{}, ErrProposalMaterializationInvalid
		}
		changed[change.EntityID] = struct{}{}
	}
	for id := range changed {
		index := byID[id]
		entities[index].EntityVersion++
	}
	sort.Slice(entities, func(i, j int) bool { return entities[i].ID < entities[j].ID })
	canonical, materializationHash, err := canonicalProposal(value.Base, value.ID, value.Hash, entities)
	if err != nil {
		return ProposalMaterializationV1{}, ErrProposalMaterializationInvalid
	}
	result := ProposalMaterializationV1{
		Version: ProposalMaterializationVersionV1, Base: value.Base, PatchID: value.ID, PatchHash: value.Hash,
		Entities: entities, Canonical: canonical, Hash: materializationHash,
	}
	if !result.Valid() {
		return ProposalMaterializationV1{}, ErrProposalMaterializationInvalid
	}
	return result, nil
}

func canonicalProposal(base aicontract.FrozenBaseIdentity, patchID aicontract.PatchID, patchHash aicontract.Hash, entities []domain.Entity) ([]byte, aicontract.Hash, error) {
	canonicalValue := map[string]any{
		"version": ProposalMaterializationVersionV1, "base": base, "patch_id": patchID,
		"patch_hash": patchHash, "entities": entities,
	}
	canonical, err := domain.CanonicalJSON(canonicalValue)
	if err != nil {
		return nil, "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("eco-guardian.ai-proposal-materialization/v1"))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(canonical)
	return canonical, aicontract.Hash(hex.EncodeToString(hash.Sum(nil))), nil
}

func samePatchAndDiff(value aicontract.DraftPatchV1, diff aicontract.DraftDiff) bool {
	operations := map[string]aicontract.DraftOperation{}
	for _, target := range value.Targets {
		for _, operation := range target.Operations {
			key := string(target.EntityID) + "\x00" + string(operation.Path) + "\x00" + strconv.Itoa(operation.Ordinal)
			if _, duplicate := operations[key]; duplicate {
				return false
			}
			operations[key] = operation
		}
	}
	if len(operations) != len(diff.Changes) {
		return false
	}
	for _, change := range diff.Changes {
		key := string(change.EntityID) + "\x00" + string(change.Path) + "\x00" + strconv.Itoa(change.Ordinal)
		operation, found := operations[key]
		if !found || operation.Kind != change.Kind || !diffChangeMatchesOperation(change, operation) {
			return false
		}
	}
	return true
}

func diffChangeMatchesOperation(change aicontract.DraftDiffChange, operation aicontract.DraftOperation) bool {
	if operation.Kind == aicontract.OperationReplace {
		canonical, err := aicontract.CanonicalToolPayload(operation.Value)
		return err == nil && bytes.Equal(canonical, change.Canonical)
	}
	var values []json.RawMessage
	if json.Unmarshal(change.Original, &values) != nil {
		return false
	}
	proposed, err := aicontract.CanonicalToolPayload(operation.Value)
	if err != nil {
		return false
	}
	if operation.Kind == aicontract.OperationAdd {
		values = append(values, append(json.RawMessage(nil), operation.Value...))
	} else if operation.Kind == aicontract.OperationRemove {
		index := -1
		for candidateIndex, candidate := range values {
			canonical, err := aicontract.CanonicalToolPayload(candidate)
			if err == nil && bytes.Equal(canonical, proposed) {
				if index >= 0 {
					return false
				}
				index = candidateIndex
			}
		}
		if index < 0 {
			return false
		}
		values = append(values[:index], values[index+1:]...)
	} else {
		return false
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return false
	}
	canonical, err := aicontract.CanonicalToolPayload(encoded)
	return err == nil && bytes.Equal(canonical, change.Canonical)
}

func cloneProposalEntities(values []domain.Entity) ([]domain.Entity, error) {
	body, err := json.Marshal(values)
	if err != nil {
		return nil, ErrProposalMaterializationInvalid
	}
	var result []domain.Entity
	if json.Unmarshal(body, &result) != nil {
		return nil, ErrProposalMaterializationInvalid
	}
	return result, nil
}

func entityValue(entity domain.Entity) (map[string]any, error) {
	body, err := json.Marshal(entity)
	if err != nil {
		return nil, ErrProposalMaterializationInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var result map[string]any
	if decoder.Decode(&result) != nil {
		return nil, ErrProposalMaterializationInvalid
	}
	return result, nil
}

func proposalJSONValue(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var result any
	if decoder.Decode(&result) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, ErrProposalMaterializationInvalid
	}
	return result, nil
}

func proposalPointerTokens(pointer string) ([]string, bool) {
	if pointer == "" || pointer[0] != '/' {
		return nil, false
	}
	values := strings.Split(pointer[1:], "/")
	for index, value := range values {
		var builder strings.Builder
		for cursor := 0; cursor < len(value); cursor++ {
			if value[cursor] != '~' {
				builder.WriteByte(value[cursor])
				continue
			}
			if cursor+1 >= len(value) || value[cursor+1] != '0' && value[cursor+1] != '1' {
				return nil, false
			}
			cursor++
			if value[cursor] == '0' {
				builder.WriteByte('~')
			} else {
				builder.WriteByte('/')
			}
		}
		values[index] = builder.String()
	}
	return values, true
}

func proposalValueAt(value any, tokens []string) (any, bool) {
	current := value
	for _, token := range tokens {
		switch typed := current.(type) {
		case map[string]any:
			var found bool
			current, found = typed[token]
			if !found {
				return nil, false
			}
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, false
			}
			current = typed[index]
		default:
			return nil, false
		}
	}
	return current, true
}

func proposalSetValue(document map[string]any, tokens []string, value any) bool {
	if len(tokens) == 0 {
		return false
	}
	var current any = document
	for _, token := range tokens[:len(tokens)-1] {
		switch typed := current.(type) {
		case map[string]any:
			next, found := typed[token]
			if !found {
				return false
			}
			current = next
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(typed) {
				return false
			}
			current = typed[index]
		default:
			return false
		}
	}
	last := tokens[len(tokens)-1]
	switch typed := current.(type) {
	case map[string]any:
		if _, found := typed[last]; !found {
			return false
		}
		typed[last] = value
		return true
	case []any:
		index, err := strconv.Atoi(last)
		if err != nil || index < 0 || index >= len(typed) {
			return false
		}
		typed[index] = value
		return true
	default:
		return false
	}
}
