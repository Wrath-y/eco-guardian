package patch

import (
	"encoding/json"
	"strings"
	"testing"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
)

func FuzzDecodeV1NeverExpandsFrozenScope(f *testing.F) {
	scope := patchDecodeContext()
	valid, _ := json.Marshal(validWirePatch(scope))
	f.Add(valid)
	f.Add([]byte("{\"release\":true}"))
	f.Add([]byte("{\"id\":\"duplicate\",\"id\":\"second\"}"))
	f.Add([]byte("{\"targets\":[{\"operations\":[{\"path\":\"/extensions/evil\"}]}]}"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		value, canonical, err := DecodeV1(raw, scope)
		if err != nil {
			return
		}
		if value.ID != scope.PatchID || value.Schema != scope.Input.Schema || value.Base != scope.Input.Base || value.EvidenceManifestIdentity != scope.EvidenceManifestIdentity {
			t.Fatal("successful decode expanded a frozen identity")
		}
		if len(canonical) == 0 || len(canonical) > scope.Input.Budget.MaxOutputBytes {
			t.Fatalf("canonical bytes=%d", len(canonical))
		}
		hash, err := aicontract.HashDraftPatch(value)
		if err != nil || hash != value.Hash {
			t.Fatalf("patch hash drift hash=%s err=%v", hash, err)
		}
		evidence := map[aicontract.EvidenceID]struct{}{}
		for _, id := range scope.EvidenceIDs {
			evidence[id] = struct{}{}
		}
		for _, target := range value.Targets {
			allowed := findTarget(scope.Input.AllowedTargets, target.EntityID)
			if allowed == nil || target.Kind != allowed.Kind || target.ExpectedEntityVersion != allowed.ExpectedEntityVersion {
				t.Fatal("successful decode expanded target scope")
			}
			for _, operation := range target.Operations {
				if forbiddenMutationPath(operation.Path) || !allowedOperation(*allowed, operation.Path, operation.Kind) {
					t.Fatalf("successful decode exposed path=%s operation=%s", operation.Path, operation.Kind)
				}
				for _, id := range operation.Evidence {
					if _, found := evidence[id]; !found {
						t.Fatalf("successful decode exposed evidence=%s", id)
					}
				}
			}
		}
	})
}

func FuzzPatchPathCannotEscapeForbiddenRoots(f *testing.F) {
	for _, seed := range []string{
		"/payload/cost", "/id", "/status", "/extensions/vendor/secret", "/release", "/registry", "/graph/active", "/payload/~1escaped", "/payload/~2invalid",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, path string) {
		if len(path) > 1024 || !aicontract.FieldPath(path).Valid() {
			return
		}
		scope := patchDecodeContext()
		scope.Input.AllowedTargets[0].Paths[0].Path = aicontract.FieldPath(path)
		scope.Values[0].Path = aicontract.FieldPath(path)
		wire := validWirePatch(scope)
		wire.Targets[0].Operations[0].Path = aicontract.FieldPath(path)
		raw, err := json.Marshal(wire)
		if err != nil {
			return
		}
		value, _, err := DecodeV1(raw, scope)
		if forbiddenMutationPath(aicontract.FieldPath(path)) {
			if err == nil {
				t.Fatalf("forbidden path decoded: %q", path)
			}
			return
		}
		if err == nil && value.Targets[0].Operations[0].Path != aicontract.FieldPath(path) {
			t.Fatalf("path was reinterpreted: input=%q output=%q", path, value.Targets[0].Operations[0].Path)
		}
		tokens, ok := pointerTokens(path)
		if ok && len(tokens) > 0 {
			for _, token := range tokens {
				if strings.Contains(token, "~2") {
					t.Fatalf("invalid escape survived tokenization: %#v", tokens)
				}
			}
		}
	})
}
