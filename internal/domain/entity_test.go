package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEntityIdentityAndPresencePatch(t *testing.T) {
	now := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	e, err := NewEntity(KindAttribute, EntityDraft{Key: "power", Name: "Power", Payload: map[string]json.RawMessage{}, Extensions: map[string]json.RawMessage{"com.example/future": json.RawMessage(` { "kept" : true } `)}}, now)
	if err != nil || !e.ID.Valid() || e.EntityVersion != 1 {
		t.Fatalf("new entity = %#v, %v", e, err)
	}
	p, err := e.ApplyPatch(EntityPatch{"name": json.RawMessage(`"Might"`)}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Might" || p.EntityVersion != 2 || string(p.Extensions["com.example/future"]) != ` { "kept" : true } ` {
		t.Fatalf("patch lost extension: %#v", p)
	}
	if _, err := e.ApplyPatch(EntityPatch{"id": json.RawMessage(`"x"`)}, now); err == nil {
		t.Fatal("immutable id accepted")
	}
}
