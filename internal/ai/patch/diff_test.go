package patch

import (
	"bytes"
	"encoding/json"
	"testing"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
)

func TestBuildDiffUsesFrozenOriginalsAndCompleteCollectionResults(t *testing.T) {
	scope := patchDecodeContext()
	raw, _ := json.Marshal(validWirePatch(scope))
	value, _, err := DecodeV1(raw, scope)
	if err != nil {
		t.Fatal(err)
	}
	result, err := BuildDiff(value, scope)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Hash.Valid() || len(result.Canonical) == 0 || result.Diff.PatchHash != value.Hash || len(result.Diff.Changes) != 2 {
		t.Fatalf("diff=%#v", result)
	}
	if string(result.Diff.Changes[0].Original) != "10" || string(result.Diff.Changes[0].Canonical) != "8" {
		t.Fatalf("replace diff=%#v", result.Diff.Changes[0])
	}
	if string(result.Diff.Changes[1].Original) != "[]" || string(result.Diff.Changes[1].Canonical) != `["018f9e40-0000-7000-8000-000000000206"]` {
		t.Fatalf("collection diff=%#v", result.Diff.Changes[1])
	}
	canonical, err := aicontract.CanonicalDraftDiff(result.Diff)
	if err != nil || !bytes.Equal(canonical, result.Canonical) {
		t.Fatalf("canonical err=%v\n%s\n%s", err, canonical, result.Canonical)
	}
}

func TestBuildDiffRejectsNoopAndPatchHashDrift(t *testing.T) {
	scope := patchDecodeContext()
	wire := validWirePatch(scope)
	wire.Targets = wire.Targets[:1]
	wire.Targets[0].Operations[0].Value = json.RawMessage("10")
	raw, _ := json.Marshal(wire)
	value, _, err := DecodeV1(raw, scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = BuildDiff(value, scope); patchErrorCode(err) != ErrorNoChange {
		t.Fatalf("noop err=%v", err)
	}
	value.Hash = aicontract.Hash("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	if _, err = BuildDiff(value, scope); patchErrorCode(err) != ErrorIdentityMismatch {
		t.Fatalf("hash drift err=%v", err)
	}
}
