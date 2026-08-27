// Package backupdomain defines backup and restore facts without importing
// transport, storage, platform, runtime, Graph, or release implementations.
package backupdomain

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
)

const (
	ManifestSchemaVersion = "eco-guardian-backup-manifest-v1"
	EvidenceVersion       = "eco-guardian-backup-evidence-v1"
	CommandVersion        = "eco-guardian-backup-command-v1"
)

var (
	ErrInvalidBackup       = errors.New("backup value is invalid")
	ErrInvalidManifest     = errors.New("backup manifest is invalid")
	ErrManifestHash        = errors.New("backup manifest hash does not match")
	ErrIdempotencyConflict = errors.New("backup idempotency conflict")
)

type Type string

const (
	Manual     Type = "manual"
	Daily      Type = "daily"
	Migration  Type = "migration"
	RestorePre Type = "restore-pre"
	Release    Type = "release"
)

func (value Type) Valid() bool {
	switch value {
	case Manual, Daily, Migration, RestorePre, Release:
		return true
	default:
		return false
	}
}

func (value Type) Mandatory() bool {
	return value == Migration || value == RestorePre || value == Release
}

type Trigger string

const (
	TriggerUser      Trigger = "user"
	TriggerPolicy    Trigger = "policy"
	TriggerMigration Trigger = "migration"
	TriggerRestore   Trigger = "restore"
	TriggerRelease   Trigger = "release"
)

func (value Trigger) ValidFor(kind Type) bool {
	switch kind {
	case Manual:
		return value == TriggerUser
	case Daily:
		return value == TriggerPolicy
	case Migration:
		return value == TriggerMigration
	case RestorePre:
		return value == TriggerRestore
	case Release:
		return value == TriggerRelease
	default:
		return false
	}
}

type ValidationState string

const (
	ValidationValid      ValidationState = "valid"
	ValidationDamaged    ValidationState = "damaged"
	ValidationIncomplete ValidationState = "incomplete"
	ValidationMissing    ValidationState = "missing"
)

func (value ValidationState) Valid() bool {
	return value == ValidationValid || value == ValidationDamaged || value == ValidationIncomplete || value == ValidationMissing
}

type CompatibilityState string

const (
	CompatibilityCurrent CompatibilityState = "current"
	CompatibilityOlder   CompatibilityState = "older"
	CompatibilityNewer   CompatibilityState = "newer"
	CompatibilityUnknown CompatibilityState = "unknown"
)

func (value CompatibilityState) Valid() bool {
	return value == CompatibilityCurrent || value == CompatibilityOlder || value == CompatibilityNewer || value == CompatibilityUnknown
}

type SourceIdentity struct {
	RevisionID  domain.ID `json:"revision_id,omitempty"`
	ReleaseID   domain.ID `json:"release_id,omitempty"`
	MigrationID string    `json:"migration_id,omitempty"`
	CallerJobID domain.ID `json:"caller_job_id,omitempty"`
	RequestHash string    `json:"request_hash,omitempty"`
}

func (value SourceIdentity) Valid() bool {
	if value.RevisionID != "" && !value.RevisionID.Valid() {
		return false
	}
	if value.ReleaseID != "" && !value.ReleaseID.Valid() {
		return false
	}
	if value.CallerJobID != "" && !value.CallerJobID.Valid() {
		return false
	}
	if value.MigrationID != "" && (!safeIdentity(value.MigrationID) || len(value.MigrationID) > 128) {
		return false
	}
	return value.RequestHash == "" || validHash(value.RequestHash)
}

type Result struct {
	EvidenceVersion string         `json:"evidence_version"`
	BackupID        domain.ID      `json:"backup_id"`
	ProjectID       domain.ID      `json:"project_uuid"`
	Type            Type           `json:"type"`
	Trigger         Trigger        `json:"trigger"`
	CreatedAt       time.Time      `json:"created_at"`
	AppVersion      string         `json:"app_version"`
	SchemaVersion   int            `json:"schema_version"`
	DBBytes         int64          `json:"db_bytes"`
	DBSHA256        string         `json:"db_sha256"`
	ManifestHash    string         `json:"manifest_hash"`
	Integrity       string         `json:"integrity"`
	Source          SourceIdentity `json:"source"`
	ResultURL       string         `json:"result_url"`
}

type InventoryRecord struct {
	BackupID      domain.ID          `json:"backup_id"`
	ProjectID     domain.ID          `json:"project_uuid"`
	Type          Type               `json:"type"`
	CreatedAt     time.Time          `json:"created_at"`
	AppVersion    string             `json:"app_version"`
	SchemaVersion int                `json:"schema_version"`
	DBBytes       int64              `json:"db_bytes"`
	DBSHA256      string             `json:"db_sha256"`
	ManifestHash  string             `json:"manifest_hash"`
	Validation    ValidationState    `json:"validation_state"`
	Compatibility CompatibilityState `json:"compatibility_state"`
	Source        SourceIdentity     `json:"source"`
	Retention     string             `json:"retention"`
	ResultURL     string             `json:"result_url"`
}

func (value InventoryRecord) Valid() bool {
	base := value.BackupID.Valid() && value.ProjectID.Valid() && value.Type.Valid() && validTime(value.CreatedAt) && validHash(value.ManifestHash) && value.Validation.Valid() && value.Compatibility.Valid() && value.Source.Valid() && value.Retention != "" && strings.HasPrefix(value.ResultURL, "/api/v1/backups/")
	if !base {
		return false
	}
	if value.Validation == ValidationIncomplete {
		return value.AppVersion == "" && value.SchemaVersion == 0 && value.DBBytes == 0 && value.DBSHA256 == "" && value.Compatibility == CompatibilityUnknown
	}
	return safeVersion(value.AppVersion) && value.SchemaVersion > 0 && value.DBBytes > 0 && validHash(value.DBSHA256)
}

func (value InventoryRecord) Restorable() bool {
	return value.Valid() && value.Validation == ValidationValid && (value.Compatibility == CompatibilityCurrent || value.Compatibility == CompatibilityOlder)
}

func (value Result) Valid() bool {
	return value.EvidenceVersion == EvidenceVersion && value.BackupID.Valid() && value.ProjectID.Valid() && value.Type.Valid() && value.Trigger.ValidFor(value.Type) && validTime(value.CreatedAt) && safeVersion(value.AppVersion) && value.SchemaVersion > 0 && value.DBBytes > 0 && validHash(value.DBSHA256) && validHash(value.ManifestHash) && value.Integrity == "ok" && value.Source.Valid() && strings.HasPrefix(value.ResultURL, "/api/v1/backups/")
}

func DecodeResult(encoded []byte) (Result, error) {
	if len(encoded) < 2 || len(encoded) > 16384 {
		return Result{}, ErrInvalidBackup
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var result Result
	if err := decoder.Decode(&result); err != nil {
		return Result{}, ErrInvalidBackup
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF || !result.Valid() {
		return Result{}, ErrInvalidBackup
	}
	canonical, err := result.CanonicalJSON()
	if err != nil || !bytes.Equal(canonical, encoded) {
		return Result{}, ErrInvalidBackup
	}
	return result, nil
}

func validHash(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == ""
}

func safeVersion(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= 128 && !strings.ContainsAny(value, "\r\n\x00")
}

func safeIdentity(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && !strings.ContainsAny(value, "\r\n\x00/\\")
}

func validTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}
