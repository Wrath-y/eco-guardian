package backupdomain

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
)

const MaxManifestBytes = 64 << 10

type ManifestBody struct {
	ManifestVersion string                     `json:"manifest_version"`
	BackupID        domain.ID                  `json:"backup_id"`
	ProjectID       domain.ID                  `json:"project_uuid"`
	Type            Type                       `json:"type"`
	Trigger         Trigger                    `json:"trigger"`
	CreatedAt       time.Time                  `json:"created_at"`
	AppVersion      string                     `json:"app_version"`
	SchemaVersion   int                        `json:"schema_version"`
	DBBytes         int64                      `json:"db_bytes"`
	DBSHA256        string                     `json:"db_sha256"`
	Source          SourceIdentity             `json:"source"`
	Extensions      map[string]json.RawMessage `json:"extensions"`
}

type Manifest struct {
	ManifestBody
	ManifestHash string `json:"manifest_hash"`
}

func NewManifest(body ManifestBody) (Manifest, error) {
	body.CreatedAt = body.CreatedAt.UTC()
	if body.Extensions == nil {
		body.Extensions = map[string]json.RawMessage{}
	}
	if !body.Valid() {
		return Manifest{}, ErrInvalidManifest
	}
	hash, err := body.Hash()
	if err != nil {
		return Manifest{}, err
	}
	return Manifest{ManifestBody: body, ManifestHash: hash}, nil
}

func (body ManifestBody) Valid() bool {
	if body.ManifestVersion != ManifestSchemaVersion || !body.BackupID.Valid() || !body.ProjectID.Valid() || !body.Type.Valid() || !body.Trigger.ValidFor(body.Type) || !validTime(body.CreatedAt) || !safeVersion(body.AppVersion) || body.SchemaVersion < 1 || body.DBBytes < 1 || !validHash(body.DBSHA256) || !body.Source.Valid() || body.Extensions == nil {
		return false
	}
	for key, value := range body.Extensions {
		if !safeIdentity(key) || len(key) > 128 || len(value) == 0 || !json.Valid(value) {
			return false
		}
	}
	return true
}

func (body ManifestBody) CanonicalJSON() ([]byte, error) {
	if !body.Valid() {
		return nil, ErrInvalidManifest
	}
	return domain.CanonicalJSON(body)
}

func (body ManifestBody) Hash() (string, error) {
	encoded, err := body.CanonicalJSON()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest), nil
}

func (manifest Manifest) Valid() bool {
	if !manifest.ManifestBody.Valid() || !validHash(manifest.ManifestHash) {
		return false
	}
	hash, err := manifest.ManifestBody.Hash()
	return err == nil && hash == manifest.ManifestHash
}

func (manifest Manifest) CanonicalJSON() ([]byte, error) {
	if !manifest.Valid() {
		return nil, ErrManifestHash
	}
	return domain.CanonicalJSON(manifest)
}

func DecodeManifest(encoded []byte) (Manifest, error) {
	if len(encoded) == 0 || len(encoded) > MaxManifestBytes || !json.Valid(encoded) {
		return Manifest{}, ErrInvalidManifest
	}
	if err := rejectDuplicateFields(encoded); err != nil {
		return Manifest{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Manifest{}, ErrInvalidManifest
	}
	if !manifest.Valid() {
		return Manifest{}, ErrManifestHash
	}
	return manifest, nil
}

func rejectDuplicateFields(encoded []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var visit func() error
	visit = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				keyToken, keyErr := decoder.Token()
				if keyErr != nil {
					return keyErr
				}
				key, keyOK := keyToken.(string)
				if !keyOK || seen[key] {
					return ErrInvalidManifest
				}
				seen[key] = true
				if err = visit(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err = visit(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return ErrInvalidManifest
		}
	}
	return visit()
}
