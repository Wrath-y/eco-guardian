package planner

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/impact"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
)

var (
	ErrInvalidInput  = errors.New("INVALID_IMPACT_INPUT")
	ErrLimitExceeded = errors.New("LIMIT_EXCEEDED")
)

func Normalize(input impact.Input) (impact.Input, []byte, string, error) {
	input.AnalysisContractVersion = impact.AnalysisContractVersion
	if !validRawSet(input.Filters.RelationshipKinds) || !validRawSet(input.Filters.NodeTypes) || !validRawSet(input.Filters.EdgeTypes) {
		return impact.Input{}, nil, "", ErrInvalidInput
	}
	input.Filters.RelationshipKinds = normalizeSet(input.Filters.RelationshipKinds)
	if len(input.Filters.RelationshipKinds) == 0 {
		input.Filters.RelationshipKinds = []string{"explicit"}
	}
	if len(input.Filters.RelationshipKinds) != 1 || input.Filters.RelationshipKinds[0] != "explicit" {
		return impact.Input{}, nil, "", ErrInvalidInput
	}
	input.Filters.NodeTypes = normalizeSet(input.Filters.NodeTypes)
	input.Filters.EdgeTypes = normalizeSet(input.Filters.EdgeTypes)
	if input.Filters.Direction == "" {
		input.Filters.Direction = impact.DirectionIncoming
	}
	if !input.Filters.Direction.Valid() || !validNodeTypes(input.Filters.NodeTypes) || !validEdgeTypes(input.Filters.EdgeTypes) {
		return impact.Input{}, nil, "", ErrInvalidInput
	}
	if input.Limits.MaxDepth == 0 {
		input.Limits.MaxDepth = 3
	}
	if input.Limits.MaxNodes == 0 {
		input.Limits.MaxNodes = 500
	}
	if input.Limits.DefaultPathsPerTarget == 0 {
		input.Limits.DefaultPathsPerTarget = 1
	}
	if input.Limits.ExpandedMaxPaths == 0 {
		input.Limits.ExpandedMaxPaths = 20
	}
	if input.Limits.MaxDepth < 1 || input.Limits.MaxNodes < 1 || input.Limits.DefaultPathsPerTarget != 1 || input.Limits.ExpandedMaxPaths < 1 {
		return impact.Input{}, nil, "", ErrInvalidInput
	}
	if input.Limits.MaxDepth > 6 || input.Limits.MaxNodes > 500 || input.Limits.ExpandedMaxPaths > 100 {
		return impact.Input{}, nil, "", ErrLimitExceeded
	}
	if input.Suspected.MaxSeeds == 0 {
		input.Suspected.MaxSeeds = 20
	}
	if input.Suspected.MaxResults == 0 {
		input.Suspected.MaxResults = 20
	}
	if input.Suspected.GraphMaxDepth == 0 {
		input.Suspected.GraphMaxDepth = 2
	}
	if input.Suspected.MaxSeeds < 1 || input.Suspected.MaxSeeds > 100 || input.Suspected.MaxResults < 1 || input.Suspected.MaxResults > 100 || input.Suspected.GraphMaxDepth < 0 || input.Suspected.GraphMaxDepth > 3 {
		return impact.Input{}, nil, "", ErrLimitExceeded
	}
	if !input.ProjectID.Valid() || !input.Base.Valid() || !input.Target.Valid() || input.Base.RevisionID == input.Target.RevisionID {
		return impact.Input{}, nil, "", ErrInvalidInput
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return impact.Input{}, nil, "", err
	}
	digest := sha256.Sum256(encoded)
	return input, encoded, hex.EncodeToString(digest[:]), nil
}

func DecodeCommand(data []byte) (impact.Command, error) {
	if err := rejectDuplicateMembers(data); err != nil {
		return impact.Command{}, ErrInvalidInput
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var command impact.Command
	if err := decoder.Decode(&command); err != nil {
		return impact.Command{}, ErrInvalidInput
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return impact.Command{}, ErrInvalidInput
	}
	return command, nil
}

func rejectDuplicateMembers(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, isDelimiter := token.(json.Delim)
		if !isDelimiter {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]struct{}{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return ErrInvalidInput
				}
				if _, duplicate := seen[key]; duplicate {
					return ErrInvalidInput
				}
				seen[key] = struct{}{}
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return ErrInvalidInput
		}
	}
	if err := walk(); err != nil {
		return err
	}
	if decoder.More() {
		return ErrInvalidInput
	}
	return nil
}

func normalizeSet(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || value != strings.TrimSpace(value) {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func validRawSet(values []string) bool {
	for _, value := range values {
		if value == "" || value != strings.TrimSpace(value) {
			return false
		}
	}
	return true
}

func validNodeTypes(values []string) bool {
	for _, value := range values {
		if !domain.EntityKind(value).Valid() {
			return false
		}
	}
	return true
}

func validEdgeTypes(values []string) bool {
	allowed := map[string]struct{}{}
	for _, descriptor := range projector.V1Relations() {
		allowed[string(descriptor.Token)] = struct{}{}
	}
	for _, value := range values {
		if _, exists := allowed[value]; !exists {
			return false
		}
	}
	return true
}
