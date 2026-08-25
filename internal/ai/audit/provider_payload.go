package audit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
)

var (
	ErrProviderPayloadInvalid = errors.New("provider payload is invalid")
	ErrProviderPayloadLimit   = errors.New("provider payload exceeds limit")
)

type SealedProviderResponse struct {
	Schema           aicontract.VersionIdentity `json:"schema"`
	OriginalBodyHash aicontract.Hash            `json:"original_body_hash"`
	StoredBody       json.RawMessage            `json:"stored_body"`
	StoredBodyHash   aicontract.Hash            `json:"stored_body_hash"`
}

// SafeCanonicalInput rejects caller intent that would require redaction. Input
// hashes are semantic identities, so silently rewriting a goal or constraint
// would be less safe than refusing to admit it.
func (r Redactor) SafeCanonicalInput(input aicontract.AIDesignInputV1) ([]byte, error) {
	canonical, err := aicontract.CanonicalAIDesignInputV1(input)
	if err != nil {
		return nil, ErrProviderPayloadInvalid
	}
	redacted, err := r.ProviderJSON(canonical, aicontract.V1MaxContextBytes)
	if err != nil {
		return nil, ErrProviderPayloadInvalid
	}
	var sanitized aicontract.AIDesignInputV1
	if err = json.Unmarshal(redacted, &sanitized); err != nil {
		return nil, ErrProviderPayloadInvalid
	}
	sanitizedCanonical, err := aicontract.CanonicalAIDesignInputV1(sanitized)
	if err != nil || !bytes.Equal(sanitizedCanonical, canonical) {
		return nil, ErrProviderPayloadInvalid
	}
	return canonical, nil
}

// SealProviderResponse retains the structured response shape only after
// bounded parsing, secret redaction and removal of hidden-reasoning fields.
func (r Redactor) SealProviderResponse(response aiprovider.StructuredResponse, maximumBytes int) (SealedProviderResponse, error) {
	if !response.Valid() || maximumBytes < 1 {
		return SealedProviderResponse{}, ErrProviderPayloadInvalid
	}
	body, err := r.ProviderJSON(response.Body, maximumBytes)
	if err != nil {
		return SealedProviderResponse{}, err
	}
	hash := sha256.Sum256(body)
	return SealedProviderResponse{
		Schema: response.Schema, OriginalBodyHash: response.BodyHash, StoredBody: body,
		StoredBodyHash: aicontract.Hash(hex.EncodeToString(hash[:])),
	}, nil
}

func (r Redactor) ProviderJSON(input []byte, maximumBytes int) ([]byte, error) {
	if len(input) == 0 || len(input) > maximumBytes {
		return nil, ErrProviderPayloadLimit
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, ErrProviderPayloadInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, ErrProviderPayloadInvalid
	}
	encoded, err := json.Marshal(r.providerValue(value))
	if err != nil {
		return nil, ErrProviderPayloadInvalid
	}
	if len(encoded) > maximumBytes {
		return nil, ErrProviderPayloadLimit
	}
	return encoded, nil
}

func (r Redactor) providerValue(value any) any {
	switch current := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(current))
		for key, child := range current {
			if hiddenReasoningKey(key) {
				continue
			}
			if sensitiveKey(key) {
				result[key] = Redacted
			} else {
				result[key] = r.providerValue(child)
			}
		}
		return result
	case []any:
		result := make([]any, len(current))
		for index, child := range current {
			result[index] = r.providerValue(child)
		}
		return result
	case string:
		return r.RedactString(current)
	default:
		return current
	}
}

func hiddenReasoningKey(value string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "_", " ", "_", ".", "_").Replace(value))
	switch normalized {
	case "analysis", "reasoning", "hidden_reasoning", "chain_of_thought", "thoughts", "thinking", "reasoning_content":
		return true
	default:
		return false
	}
}

// SanitizeProviderEvent prepares a Provider event for persistence, SSE or
// logging. Execution-time consumers may separately validate the raw event.
func (r Redactor) SanitizeProviderEvent(event aiprovider.Event, maximumBytes int) (aiprovider.Event, error) {
	if !event.Valid() || maximumBytes < 1 {
		return aiprovider.Event{}, ErrProviderPayloadInvalid
	}
	result := event
	if event.Warning != nil {
		warning := *event.Warning
		warning.Message = r.RedactString(warning.Message)
		result.Warning = &warning
	}
	if event.Error != nil {
		terminal := *event.Error
		terminal.Message = r.RedactString(terminal.Message)
		result.Error = &terminal
	}
	if event.ToolCall != nil {
		call := *event.ToolCall
		arguments, err := r.ProviderJSON(call.Arguments, maximumBytes)
		if err != nil {
			return aiprovider.Event{}, err
		}
		call.Arguments = arguments
		result.ToolCall = &call
	}
	if event.StructuredResponse != nil {
		sealed, err := r.SealProviderResponse(*event.StructuredResponse, maximumBytes)
		if err != nil {
			return aiprovider.Event{}, err
		}
		response := *event.StructuredResponse
		response.Body = sealed.StoredBody
		response.BodyHash = sealed.StoredBodyHash
		result.StructuredResponse = &response
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > maximumBytes {
		return aiprovider.Event{}, ErrProviderPayloadLimit
	}
	if !result.Valid() {
		return aiprovider.Event{}, ErrProviderPayloadInvalid
	}
	return result, nil
}
