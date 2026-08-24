package materialization

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
	"github.com/zouyi/eco-guardian/internal/validation"
)

func TestBuildIsCanonicalAcrossEntityOrder(t *testing.T) {
	source := completeSource(t)
	first, err := Build(source)
	if err != nil {
		t.Fatal(err)
	}
	source.Entities[0], source.Entities[len(source.Entities)-1] = source.Entities[len(source.Entities)-1], source.Entities[0]
	second, err := Build(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(first.Canonical) != string(second.Canonical) || first.MaterializationHash != second.MaterializationHash {
		t.Fatalf("materialization drifted\n%s\n%s", first.Canonical, second.Canonical)
	}
	if len(first.FormulaBindings) != 1 || len(first.TriggerRules) != 2 || len(first.Modifiers) != 1 || len(first.StackRules) != 1 {
		t.Fatalf("unexpected counts: %+v", first)
	}
}

func TestBuildRejectsMissingFormulaArtifact(t *testing.T) {
	source := completeSource(t)
	source.Formulas = nil
	_, err := Build(source)
	var diagnostic Diagnostic
	if !errors.As(err, &diagnostic) || diagnostic.Code != DiagnosticArtifactMissing {
		t.Fatalf("err=%v", err)
	}
}

func TestBuildExcludesExtensions(t *testing.T) {
	source := completeSource(t)
	source.Entities[0].Extensions = map[string]json.RawMessage{"future": json.RawMessage(`{"modifiers":[{"attribute_id":"not-a-rule"}]}`)}
	set, err := Build(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Modifiers) != 1 {
		t.Fatalf("extension became behavior: %+v", set.Modifiers)
	}
}

func TestBuildRejectsUnknownRuleAndDanglingReference(t *testing.T) {
	source := completeSource(t)
	source.Entities[3].Payload["rule_blocks"] = json.RawMessage(`[ {"event":"unknown","target":{"type":"self"}} ]`)
	_, err := Build(source)
	var diagnostic Diagnostic
	if !errors.As(err, &diagnostic) || diagnostic.Code != DiagnosticPayloadInvalid {
		t.Fatalf("err=%v", err)
	}
	source = completeSource(t)
	source.Entities[2].Payload["modifiers"] = json.RawMessage(`[ {"attribute_id":"` + string(newID(t)) + `","operation":"Add","value":"1"} ]`)
	_, err = Build(source)
	if !errors.As(err, &diagnostic) || diagnostic.Code != DiagnosticReferenceInvalid {
		t.Fatalf("err=%v", err)
	}
	source = completeSource(t)
	source.Entities[2].Payload["stack_rule"] = json.RawMessage(`{"operation":"Add","priority":"1","max_stacks":"1","refresh_policy":"refresh","cap":"1.0"}`)
	_, err = Build(source)
	if !errors.As(err, &diagnostic) || diagnostic.Code != DiagnosticPayloadInvalid {
		t.Fatalf("err=%v", err)
	}
	source = completeSource(t)
	source.Formulas[0].Reads = []validation.FormulaRead{{Scope: "self", Symbol: "power", Span: formula.Span{StartByte: -1, EndByte: 1}}}
	_, err = Build(source)
	if !errors.As(err, &diagnostic) || diagnostic.Code != DiagnosticArtifactInvalid {
		t.Fatalf("err=%v", err)
	}
}

type sourceReaderFake struct {
	source Source
	reads  int
}

func (f *sourceReaderFake) ReadRuleSource(context.Context, domain.ID) (Source, error) {
	f.reads++
	return f.source, nil
}

func TestCacheRebuildsCorruptDerivedValue(t *testing.T) {
	source := completeSource(t)
	reader := &sourceReaderFake{source: source}
	cache := &Cache{}
	first, err := cache.Materialize(context.Background(), reader, source.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	key, err := cacheKey(source)
	if err != nil {
		t.Fatal(err)
	}
	cache.values[key] = RuleSetV1{ContractVersion: ContractVersionV1, ProjectID: source.ProjectID, RevisionID: source.RevisionID, ConfigHash: source.ConfigHash, Certification: source.Certification, Canonical: []byte("corrupt"), MaterializationHash: hash("bad")}
	second, err := cache.Materialize(context.Background(), reader, source.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if first.MaterializationHash != second.MaterializationHash || Verify(second) != nil {
		t.Fatalf("cache did not rebuild: %+v", second)
	}
}

func FuzzBuildRejectsMalformedKnownRulesWithoutPanic(f *testing.F) {
	f.Add(`{"operation":"Add","priority":"1","max_stacks":"1","refresh_policy":"refresh","cap":"10"}`)
	f.Add(`{"operation":"unknown"}`)
	f.Fuzz(func(t *testing.T, raw string) {
		source := completeSource(t)
		source.Entities[2].Payload["stack_rule"] = json.RawMessage(raw)
		_, _ = Build(source)
	})
}

func TestBuildCrossProcess(t *testing.T) {
	if encoded := os.Getenv("ECO_RULE_SOURCE"); encoded != "" {
		raw, err := base64.RawStdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		var source Source
		if err = json.Unmarshal(raw, &source); err != nil {
			t.Fatal(err)
		}
		set, err := Build(source)
		if err != nil {
			t.Fatal(err)
		}
		if set.MaterializationHash != os.Getenv("ECO_RULE_HASH") {
			t.Fatalf("hash=%s", set.MaterializationHash)
		}
		return
	}
	source := completeSource(t)
	set, err := Build(source)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestBuildCrossProcess$")
	command.Env = append(os.Environ(), "ECO_RULE_SOURCE="+base64.RawStdEncoding.EncodeToString(raw), "ECO_RULE_HASH="+set.MaterializationHash)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("cross-process materialization: %v\n%s", err, output)
	}
}

func completeSource(t *testing.T) Source {
	t.Helper()
	project, revision, run := newID(t), newID(t), newID(t)
	attribute, tag, effect, skill := newID(t), newID(t), newID(t), newID(t)
	ast := formula.NewAST(formula.Node{Kind: formula.NodeDecimal, Span: formula.Span{StartByte: 0, EndByte: 1}, Text: "1"})
	astBytes, err := ast.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	astHash, err := ast.Hash()
	if err != nil {
		t.Fatal(err)
	}
	entity := func(id domain.ID, kind domain.EntityKind, payload map[string]json.RawMessage) domain.Entity {
		return domain.Entity{ID: id, Kind: kind, Key: string(kind), Name: string(kind), Status: domain.StatusActive, SchemaVersion: 1, Payload: payload, Extensions: map[string]json.RawMessage{}}
	}
	attributeEntity := entity(attribute, domain.KindAttribute, map[string]json.RawMessage{})
	tagEntity := entity(tag, domain.KindTag, map[string]json.RawMessage{})
	effectEntity := entity(effect, domain.KindEffect, map[string]json.RawMessage{
		"modifiers":      json.RawMessage(`[ {"attribute_id":"` + string(attribute) + `","operation":"Add","value":"1"} ]`),
		"trigger_blocks": json.RawMessage(`[ {"event":"on_hit","target":{"type":"self"},"effect_ids":["` + string(effect) + `"],"termination_budget":"1"} ]`),
		"stack_rule":     json.RawMessage(`{"operation":"Add","priority":"1","max_stacks":"1","refresh_policy":"refresh","cap":"10"}`),
	})
	skillEntity := entity(skill, domain.KindSkill, map[string]json.RawMessage{
		"costs":       json.RawMessage(`[ {"output_attribute_id":"` + string(attribute) + `","expression":"1"} ]`),
		"rule_blocks": json.RawMessage(`[ {"event":"on_use","target":{"type":"targets_with_tag","tag_id":"` + string(tag) + `"},"effect_ids":["` + string(effect) + `"]} ]`),
	})
	versions := validation.VersionManifest{Schema: "v1", DSL: formula.DSLVersion, Registry: "registry-v1", NumericPolicy: "numeric-v1"}
	return Source{ProjectID: project, RevisionID: revision, ConfigHash: hash("config"), Certification: Certification{RunID: run, ResultHash: hash("result"), Versions: versions}, Entities: []domain.Entity{attributeEntity, tagEntity, effectEntity, skillEntity}, Formulas: []validation.FormulaIndexRecord{{SourceID: skill, FieldPath: "/payload/costs/0/expression", OutputAttributeID: attribute, FormulaHash: hash("formula"), ASTHash: astHash, AST: astBytes, ASTVersion: formula.ASTSchemaVersion, DSLVersion: formula.DSLVersion, RegistryVersion: "registry-v1"}}}
}

func newID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func hash(value string) string {
	return "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
}
