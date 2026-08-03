package domain

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type ID string

func NewID() (ID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("allocate UUIDv7: %w", err)
	}
	return ID(id.String()), nil
}

func (id ID) Valid() bool {
	u, err := uuid.Parse(string(id))
	return err == nil && u.Version() == 7
}

type EntityKind string

const (
	KindAttribute EntityKind = "attribute"
	KindTag       EntityKind = "tag"
	KindCharacter EntityKind = "character"
	KindSkill     EntityKind = "skill"
	KindItem      EntityKind = "item"
	KindEffect    EntityKind = "effect"
)

func (k EntityKind) Valid() bool {
	switch k {
	case KindAttribute, KindTag, KindCharacter, KindSkill, KindItem, KindEffect:
		return true
	default:
		return false
	}
}

type EntityStatus string

const (
	StatusActive   EntityStatus = "active"
	StatusArchived EntityStatus = "archived"
)

// Entity is the immutable-at-identity envelope persisted in canonical form.
// Extensions use RawMessage so unrecognised namespace values round-trip exactly.
type Entity struct {
	ID            ID                         `json:"id"`
	Kind          EntityKind                 `json:"kind"`
	Key           string                     `json:"key"`
	Name          string                     `json:"name"`
	Description   string                     `json:"description,omitempty"`
	TagIDs        []ID                       `json:"tag_ids"`
	BalanceGroup  string                     `json:"balance_group,omitempty"`
	Status        EntityStatus               `json:"status"`
	SchemaVersion int                        `json:"schema_version"`
	Payload       map[string]json.RawMessage `json:"payload"`
	Extensions    map[string]json.RawMessage `json:"extensions"`
	EntityVersion int64                      `json:"entity_version"`
	CreatedAt     time.Time                  `json:"created_at"`
	UpdatedAt     time.Time                  `json:"updated_at"`
}

type EntityDraft struct {
	Key          string                     `json:"key"`
	Name         string                     `json:"name"`
	Description  string                     `json:"description,omitempty"`
	TagIDs       []ID                       `json:"tag_ids"`
	BalanceGroup string                     `json:"balance_group,omitempty"`
	Payload      map[string]json.RawMessage `json:"payload"`
	Extensions   map[string]json.RawMessage `json:"extensions"`
}

// EntityPatch deliberately records presence: absent fields do not erase values,
// while arrays and objects present in the patch replace their entire field value.
type EntityPatch map[string]json.RawMessage

func NewEntity(kind EntityKind, draft EntityDraft, now time.Time) (Entity, error) {
	id, err := NewID()
	if err != nil {
		return Entity{}, err
	}
	if draft.TagIDs == nil {
		draft.TagIDs = []ID{}
	}
	if draft.Payload == nil {
		draft.Payload = map[string]json.RawMessage{}
	}
	if draft.Extensions == nil {
		draft.Extensions = map[string]json.RawMessage{}
	}
	return Entity{ID: id, Kind: kind, Key: draft.Key, Name: draft.Name, Description: draft.Description, TagIDs: draft.TagIDs, BalanceGroup: draft.BalanceGroup, Status: StatusActive, SchemaVersion: 1, Payload: draft.Payload, Extensions: draft.Extensions, EntityVersion: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}, nil
}

func (e Entity) ApplyPatch(patch EntityPatch, now time.Time) (Entity, error) {
	next := e
	for field, raw := range patch {
		switch field {
		case "key", "name", "description", "balance_group":
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				return Entity{}, fmt.Errorf("%s: %w", field, err)
			}
			switch field {
			case "key":
				next.Key = value
			case "name":
				next.Name = value
			case "description":
				next.Description = value
			case "balance_group":
				next.BalanceGroup = value
			}
		case "tag_ids":
			if err := json.Unmarshal(raw, &next.TagIDs); err != nil {
				return Entity{}, fmt.Errorf("tag_ids: %w", err)
			}
		case "payload":
			if err := json.Unmarshal(raw, &next.Payload); err != nil {
				return Entity{}, fmt.Errorf("payload: %w", err)
			}
		case "extensions":
			if err := json.Unmarshal(raw, &next.Extensions); err != nil {
				return Entity{}, fmt.Errorf("extensions: %w", err)
			}
		case "id", "kind", "status", "schema_version", "entity_version", "created_at", "updated_at":
			return Entity{}, fmt.Errorf("%s is immutable", field)
		default:
			return Entity{}, fmt.Errorf("unsupported patch field %q", field)
		}
	}
	next.EntityVersion++
	next.UpdatedAt = now.UTC()
	return next, nil
}

type RevisionSummary struct {
	ID              ID        `json:"id"`
	DisplayRevision int64     `json:"display_revision"`
	ConfigHash      string    `json:"config_hash"`
	CreatedAt       time.Time `json:"created_at"`
}
