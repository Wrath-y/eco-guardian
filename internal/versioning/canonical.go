// Package versioning provides canonical payload helpers shared by the bounded
// versioning domain packages. It has no adapter dependencies.
package versioning

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/zouyi/eco-guardian/internal/domain"
)

func CanonicalJSON(value any) ([]byte, error) { return domain.CanonicalJSON(value) }

func SHA256(bytes []byte) string {
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:])
}
