package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
)

// CanonicalJSON produces a stable JSON encoding: maps are key-sorted and no
// insignificant whitespace is emitted. It is the sole input to blob hashes.
func CanonicalJSON(value any) ([]byte, error) {
	var decoded any
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := appendCanonical(&out, decoded); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func appendCanonical(out *bytes.Buffer, value any) error {
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				out.WriteByte(',')
			}
			key, _ := json.Marshal(k)
			out.Write(key)
			out.WriteByte(':')
			if err := appendCanonical(out, v[k]); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	case []any:
		out.WriteByte('[')
		for i, child := range v {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := appendCanonical(out, child); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	case nil, bool, string, float64:
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		out.Write(b)
	default:
		return fmt.Errorf("unsupported canonical value %T", value)
	}
	return nil
}

func BlobHash(value any) (string, []byte, error) {
	canonical, err := CanonicalJSON(value)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(canonical)
	return fmt.Sprintf("%x", sum), canonical, nil
}
