package backupdomain

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
)

type Command struct {
	CommandVersion string         `json:"command_version"`
	ProjectID      domain.ID      `json:"project_uuid"`
	Purpose        Type           `json:"purpose"`
	LocalDate      string         `json:"local_date,omitempty"`
	CallerJobID    domain.ID      `json:"caller_job_id,omitempty"`
	CallerHash     string         `json:"caller_request_hash,omitempty"`
	ManualReason   string         `json:"manual_reason,omitempty"`
	Source         SourceIdentity `json:"source"`
}

func (command Command) Valid() bool {
	if command.CommandVersion != CommandVersion || !command.ProjectID.Valid() || !command.Purpose.Valid() || !command.Source.Valid() {
		return false
	}
	if command.Purpose == Daily {
		if _, err := time.Parse("2006-01-02", command.LocalDate); err != nil {
			return false
		}
	} else if command.LocalDate != "" {
		return false
	}
	if command.Purpose.Mandatory() {
		return command.CallerJobID.Valid() && validHash(command.CallerHash)
	}
	if command.CallerJobID != "" || command.CallerHash != "" {
		return false
	}
	return command.Purpose != Manual || (command.ManualReason != "" && command.ManualReason == strings.TrimSpace(command.ManualReason) && len(command.ManualReason) <= 128)
}

func (command Command) CanonicalJSON() ([]byte, error) {
	if !command.Valid() {
		return nil, ErrInvalidBackup
	}
	return domain.CanonicalJSON(command)
}

func DecodeCommand(encoded []byte, target *Command) error {
	if target == nil || len(encoded) < 2 || len(encoded) > 8192 {
		return ErrInvalidBackup
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrInvalidBackup
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF || !target.Valid() {
		return ErrInvalidBackup
	}
	canonical, err := target.CanonicalJSON()
	if err != nil || !bytes.Equal(canonical, encoded) {
		return ErrInvalidBackup
	}
	return nil
}

func (command Command) Hash() (string, error) {
	encoded, err := command.CanonicalJSON()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest), nil
}

func (command Command) UniquenessKey() (string, error) {
	hash, err := command.Hash()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("backup:%s:%s:%s", command.ProjectID, command.Purpose, hash), nil
}

func (result Result) CanonicalJSON() ([]byte, error) {
	if !result.Valid() {
		return nil, ErrInvalidBackup
	}
	return domain.CanonicalJSON(result)
}

func (result Result) Hash() (string, error) {
	encoded, err := result.CanonicalJSON()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest), nil
}

type DailyState string

const (
	DailySuccessful       DailyState = "successful_today"
	DailyInProgress       DailyState = "queued_or_running"
	DailyRequired         DailyState = "required_new_backup"
	DailyAwaitingWaiver   DailyState = "failed_awaiting_confirmation"
	DailyExplicitlyWaived DailyState = "explicit_today_waiver"
)

type DailyObservation struct {
	ProjectID       domain.ID
	LocalDate       string
	BusinessWrite   bool
	SuccessfulToday bool
	InProgress      bool
	FailedJobID     domain.ID
	Waiver          *DailyWaiver
}

func ReduceDailyPolicy(observation DailyObservation) (DailyState, error) {
	if !observation.ProjectID.Valid() {
		return "", ErrInvalidBackup
	}
	date, err := time.Parse("2006-01-02", observation.LocalDate)
	if err != nil || date.Format("2006-01-02") != observation.LocalDate {
		return "", ErrInvalidBackup
	}
	if !observation.BusinessWrite || observation.SuccessfulToday {
		return DailySuccessful, nil
	}
	if observation.InProgress {
		return DailyInProgress, nil
	}
	if observation.Waiver != nil && observation.Waiver.Valid() && observation.Waiver.ProjectID == observation.ProjectID && observation.Waiver.LocalDate == observation.LocalDate && observation.Waiver.FailedJobID == observation.FailedJobID {
		return DailyExplicitlyWaived, nil
	}
	if observation.FailedJobID.Valid() {
		return DailyAwaitingWaiver, nil
	}
	return DailyRequired, nil
}

type DailyWaiver struct {
	Version     string    `json:"version"`
	ProjectID   domain.ID `json:"project_uuid"`
	LocalDate   string    `json:"local_date"`
	FailedJobID domain.ID `json:"failed_backup_job_id"`
	Confirmed   bool      `json:"confirmed"`
	ConfirmedBy string    `json:"confirmed_by"`
	ConfirmedAt time.Time `json:"confirmed_at"`
}

func (waiver DailyWaiver) Valid() bool {
	_, dateErr := time.Parse("2006-01-02", waiver.LocalDate)
	return waiver.Version == "daily-waiver-v1" && waiver.ProjectID.Valid() && dateErr == nil && waiver.FailedJobID.Valid() && waiver.Confirmed && safeIdentity(waiver.ConfirmedBy) && len(waiver.ConfirmedBy) <= 128 && validTime(waiver.ConfirmedAt)
}

func (waiver DailyWaiver) CanonicalJSON() ([]byte, error) {
	if !waiver.Valid() {
		return nil, ErrInvalidBackup
	}
	return domain.CanonicalJSON(waiver)
}

func DecodeDailyWaiver(encoded []byte) (DailyWaiver, error) {
	if len(encoded) < 2 || len(encoded) > 8192 {
		return DailyWaiver{}, ErrInvalidBackup
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var waiver DailyWaiver
	if err := decoder.Decode(&waiver); err != nil {
		return DailyWaiver{}, ErrInvalidBackup
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF || !waiver.Valid() {
		return DailyWaiver{}, ErrInvalidBackup
	}
	canonical, err := waiver.CanonicalJSON()
	if err != nil || !bytes.Equal(canonical, encoded) {
		return DailyWaiver{}, ErrInvalidBackup
	}
	return waiver, nil
}

type Artifact struct {
	ID         domain.ID
	Type       Type
	CreatedAt  time.Time
	Validation ValidationState
	Published  bool
	Leased     bool
}

func SelectRetention(artifacts []Artifact, dailyLimit, releaseMigrationLimit int) ([]domain.ID, error) {
	if dailyLimit < 1 || releaseMigrationLimit < 1 {
		return nil, ErrInvalidBackup
	}
	daily := []Artifact{}
	releaseMigration := []Artifact{}
	for _, artifact := range artifacts {
		if !artifact.ID.Valid() || !artifact.Type.Valid() || !validTime(artifact.CreatedAt) || !artifact.Validation.Valid() {
			return nil, ErrInvalidBackup
		}
		if !artifact.Published || artifact.Validation != ValidationValid || artifact.Leased {
			continue
		}
		switch artifact.Type {
		case Daily:
			daily = append(daily, artifact)
		case Release, Migration:
			releaseMigration = append(releaseMigration, artifact)
		}
	}
	sortNewest := func(values []Artifact) {
		sort.Slice(values, func(left, right int) bool {
			if !values[left].CreatedAt.Equal(values[right].CreatedAt) {
				return values[left].CreatedAt.After(values[right].CreatedAt)
			}
			return values[left].ID > values[right].ID
		})
	}
	sortNewest(daily)
	sortNewest(releaseMigration)
	selected := []Artifact{}
	if len(daily) > dailyLimit {
		selected = append(selected, daily[dailyLimit:]...)
	}
	if len(releaseMigration) > releaseMigrationLimit {
		selected = append(selected, releaseMigration[releaseMigrationLimit:]...)
	}
	sort.Slice(selected, func(left, right int) bool {
		if !selected[left].CreatedAt.Equal(selected[right].CreatedAt) {
			return selected[left].CreatedAt.Before(selected[right].CreatedAt)
		}
		return selected[left].ID < selected[right].ID
	})
	result := make([]domain.ID, len(selected))
	for index := range selected {
		result[index] = selected[index].ID
	}
	return result, nil
}

func canonicalMap(value map[string]any) ([]byte, error) {
	if value == nil {
		value = map[string]any{}
	}
	return domain.CanonicalJSON(value)
}

func validRawJSON(value json.RawMessage) bool { return len(value) > 0 && json.Valid(value) }
