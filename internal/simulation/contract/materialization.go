package contract

import (
	"github.com/zouyi/eco-guardian/internal/domain"
	"time"
)

type JobMaterialization struct {
	JobID, ProjectID, RevisionID, ScenarioDefinitionID domain.ID
	VerifySourceRunID                                  domain.ID
	CanonicalInput                                     []byte
	InputHash, FingerprintHash                         string
	CancelGeneration                                   int64
	CreatedAt                                          time.Time
}
