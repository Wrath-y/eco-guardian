package threshold

import (
	"context"
	"strings"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
)

type Clock interface{ Now() time.Time }
type IDGenerator interface{ New() (domain.ID, error) }

type CreateRequest struct {
	ProjectID      domain.ID
	ProposedID     domain.ID
	Origin         Origin
	Body           Body
	BodyHash       string
	CreatedBy      string
	CreatedAt      time.Time
	IdempotencyKey string
	RequestHash    string
}

func (r CreateRequest) Valid() bool {
	if !r.ProjectID.Valid() || !r.ProposedID.Valid() || (r.Origin != OriginStarter && r.Origin != OriginModifiedStarter) || !r.Body.Valid() || !validHash(r.BodyHash) || strings.TrimSpace(r.CreatedBy) == "" || r.CreatedAt.IsZero() || strings.TrimSpace(r.IdempotencyKey) == "" || !validHash(r.RequestHash) {
		return false
	}
	hash, err := r.Body.Hash()
	return err == nil && hash == r.BodyHash
}

type Repository interface {
	GetExactEnabled(context.Context, domain.ID, riskcontract.Identity) (Version, bool, error)
	CreateEnabled(context.Context, CreateRequest) (Version, bool, error)
}

type SelectionCommand struct {
	ProjectID      domain.ID
	Kind           riskcontract.ThresholdSelectionKind
	Existing       *riskcontract.Identity
	ModifiedBody   *Body
	Confirmed      bool
	CreatedBy      string
	IdempotencyKey string
}

type Service struct {
	Repository Repository
	Clock      Clock
	IDs        IDGenerator
	Starter    StarterTemplate
}

func (s Service) Select(ctx context.Context, command SelectionCommand) (Version, bool, error) {
	if s.Repository == nil || s.Clock == nil || s.IDs == nil || !command.ProjectID.Valid() || strings.TrimSpace(command.IdempotencyKey) == "" {
		return Version{}, false, ErrThresholdInvalid
	}
	if command.Kind == riskcontract.ExistingThreshold {
		if command.Existing == nil || !command.Existing.Valid() || command.ModifiedBody != nil || command.Confirmed {
			return Version{}, false, ErrThresholdInvalid
		}
		version, found, err := s.Repository.GetExactEnabled(ctx, command.ProjectID, *command.Existing)
		if err != nil {
			return Version{}, false, err
		}
		if !found || !version.Valid() || !version.Enabled {
			return Version{}, false, ErrThresholdNotConfigured
		}
		return version, true, nil
	}
	if !command.Confirmed || strings.TrimSpace(command.CreatedBy) == "" || command.Existing != nil {
		return Version{}, false, ErrThresholdNotConfigured
	}
	starter := s.Starter
	if !starter.Valid() {
		starter = StarterFixtureV1()
	}
	body := starter.Body
	origin := OriginStarter
	if command.Kind == riskcontract.ModifiedStarter {
		if command.ModifiedBody == nil {
			return Version{}, false, ErrThresholdInvalid
		}
		body = *command.ModifiedBody
		origin = OriginModifiedStarter
	} else if command.Kind != riskcontract.StarterThreshold || command.ModifiedBody != nil {
		return Version{}, false, ErrThresholdInvalid
	}
	normalized, err := body.Normalize()
	if err != nil {
		return Version{}, false, ErrThresholdInvalid
	}
	bodyHash, err := normalized.Hash()
	if err != nil {
		return Version{}, false, ErrThresholdInvalid
	}
	selectionHash, err := requestHash(struct {
		ProjectID domain.ID `json:"project_id"`
		Kind      string    `json:"kind"`
		BodyHash  string    `json:"body_hash"`
		CreatedBy string    `json:"created_by"`
	}{command.ProjectID, string(command.Kind), bodyHash, command.CreatedBy})
	if err != nil {
		return Version{}, false, ErrThresholdInvalid
	}
	id, err := s.IDs.New()
	if err != nil {
		return Version{}, false, err
	}
	request := CreateRequest{ProjectID: command.ProjectID, ProposedID: id, Origin: origin, Body: normalized, BodyHash: bodyHash, CreatedBy: command.CreatedBy, CreatedAt: s.Clock.Now().UTC(), IdempotencyKey: command.IdempotencyKey, RequestHash: selectionHash}
	if !request.Valid() {
		return Version{}, false, ErrThresholdInvalid
	}
	version, replayed, err := s.Repository.CreateEnabled(ctx, request)
	if err != nil {
		return Version{}, false, err
	}
	if !version.Valid() || !version.Enabled || version.BodyHash != bodyHash {
		return Version{}, false, ErrThresholdInvalid
	}
	return version, replayed, nil
}
