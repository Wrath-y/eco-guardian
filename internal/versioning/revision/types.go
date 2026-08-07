package revision

import (
	"fmt"
	"strings"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
)

type VersionEntry struct {
	CapabilityID          string            `json:"capability_id"`
	ContractVersion       string            `json:"contract_version"`
	ImplementationVersion string            `json:"implementation_version"`
	State                 RegistrationState `json:"state"`
}

func (v VersionEntry) Valid() bool {
	return strings.TrimSpace(v.CapabilityID) != "" && strings.TrimSpace(v.ContractVersion) != "" &&
		((v.State == Registered && strings.TrimSpace(v.ImplementationVersion) != "") ||
			(v.State == Unregistered && v.ImplementationVersion == ""))
}

// VersionManifest records the implementation identities used to interpret an
// immutable revision. It must be materialized before writing the revision.
type VersionManifest struct {
	Entries []VersionEntry `json:"entries"`
}

func (m VersionManifest) Valid() bool {
	if len(m.Entries) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(m.Entries))
	for _, entry := range m.Entries {
		if !entry.Valid() {
			return false
		}
		if _, duplicate := seen[entry.CapabilityID]; duplicate {
			return false
		}
		seen[entry.CapabilityID] = struct{}{}
	}
	return true
}

type Metadata struct {
	RevisionID       domain.ID       `json:"revision_id"`
	ConfigHash       string          `json:"config_hash"`
	Name             string          `json:"name,omitempty"`
	Description      string          `json:"description,omitempty"`
	ParentRevisionID domain.ID       `json:"parent_revision_id,omitempty"`
	SourceRevisionID domain.ID       `json:"source_revision_id,omitempty"`
	SourceReleaseID  domain.ID       `json:"source_release_id,omitempty"`
	Manifest         VersionManifest `json:"version_manifest"`
	ManifestHash     string          `json:"version_manifest_hash"`
	CreatedAt        time.Time       `json:"created_at"`
}

func (m Metadata) Valid() bool {
	return m.RevisionID.Valid() && validHash(m.ConfigHash) && validHash(m.ManifestHash) && m.Manifest.Valid() && !m.CreatedAt.IsZero()
}

// Record is the immutable revision identity together with its metadata. The
// display sequence is deliberately distinct from the content hash and UUID.
type Record struct {
	DisplayRevision int64    `json:"display_revision"`
	Metadata        Metadata `json:"metadata"`
}

func (r Record) Valid() bool {
	return r.DisplayRevision > 0 && r.Metadata.Valid()
}

// HistoryPage preserves the opaque cursor returned by a repository; callers
// must not infer ordering from UUID or content-hash values.
type HistoryPage struct {
	Items      []Record `json:"items"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

// TimelineEvent is a read-only projection over immutable facts and durable
// release records. It is never stored back on a revision as mutable status.
type TimelineEvent struct {
	ID         string    `json:"id"`
	OccurredAt time.Time `json:"occurred_at"`
	Type       string    `json:"type"`
	RevisionID domain.ID `json:"revision_id"`
	SubjectID  domain.ID `json:"subject_id,omitempty"`
	Status     string    `json:"status,omitempty"`
}

type Detail struct {
	Record            Record          `json:"record"`
	Timeline          []TimelineEvent `json:"timeline"`
	ActiveReleaseID   domain.ID       `json:"active_release_id,omitempty"`
	PointerGeneration int64           `json:"pointer_generation"`
}

// CandidateContext is a reference-only selection for gating or release. It
// never contains entity or manifest copies.
type CandidateContext struct {
	RevisionID        domain.ID `json:"revision_id"`
	ConfigHash        string    `json:"config_hash"`
	ManifestHash      string    `json:"version_manifest_hash"`
	PolicyID          domain.ID `json:"policy_id"`
	BaselineReleaseID domain.ID `json:"baseline_release_id,omitempty"`
}

func (c CandidateContext) Valid() bool {
	return c.RevisionID.Valid() && c.PolicyID.Valid() && validHash(c.ConfigHash) && validHash(c.ManifestHash)
}

func validHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

func (m Metadata) Validate() error {
	if !m.Valid() {
		return fmt.Errorf("invalid revision metadata")
	}
	return nil
}
