package contract

import (
	"crypto/sha256"
	"encoding/hex"
)

const checkpointDomainSeparatorV1 = "eco-guardian/simulation-checkpoint/v1\x00"

// CheckpointAccumulatorHash is the immutable identity of a completed sample
// accumulator. Storage and recovery use this single domain-separated rule.
func CheckpointAccumulatorHash(accumulator string) string {
	digest := sha256.Sum256([]byte(checkpointDomainSeparatorV1 + accumulator))
	return hex.EncodeToString(digest[:])
}
