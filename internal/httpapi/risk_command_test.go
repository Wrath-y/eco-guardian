package httpapi

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestDecodeRiskReviewCommandAcceptsFrozenTaggedFixtures(t *testing.T) {
	for _, fixture := range []string{"risk-evaluate-request.json", "risk-decision-request.json"} {
		raw, err := os.ReadFile("../../api/fixtures/" + fixture)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = DecodeRiskReviewCommand(bytes.NewReader(raw)); err != nil {
			t.Fatalf("%s: %v", fixture, err)
		}
	}
}

func TestDecodeRiskReviewCommandRejectsUnknownClientOutcomesAndIncompleteUnions(t *testing.T) {
	raw, err := os.ReadFile("../../api/fixtures/risk-evaluate-request.json")
	if err != nil {
		t.Fatal(err)
	}
	valid := string(raw)
	cases := map[string]string{
		"unknown field":          strings.Replace(valid, `"command": "evaluate",`, `"command": "evaluate", "unknown": true,`, 1),
		"client severity":        strings.Replace(valid, `"command": "evaluate",`, `"command": "evaluate", "severity": "INFO",`, 1),
		"client Gate PASS":       strings.Replace(valid, `"command": "evaluate",`, `"command": "evaluate", "gate_state": "PASS",`, 1),
		"raw override":           strings.Replace(valid, `"command": "evaluate",`, `"command": "evaluate", "override": true,`, 1),
		"missing revision hash":  strings.Replace(valid, `"config_hash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`, "", 1),
		"malformed baseline tag": strings.Replace(valid, `"type": "NO_BASELINE"`, `"type": "CURRENT"`, 1),
		"nested baseline field":  strings.Replace(valid, `"baseline": { "type": "NO_BASELINE" }`, `"baseline": { "type": "NO_BASELINE", "release_id": "01948c1e-0000-7000-8000-000000000099" }`, 1),
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			_, decodeErr := DecodeRiskReviewCommand(strings.NewReader(payload))
			if decodeErr == nil {
				t.Fatal("invalid command accepted")
			}
			message := strings.ToLower(decodeErr.Error())
			for _, secret := range []string{"select ", "insert ", "sqlite", "/users/", "\\users\\", "stack"} {
				if strings.Contains(message, secret) {
					t.Fatalf("unsafe detail leaked: %v", decodeErr)
				}
			}
		})
	}
}

func TestDecodeRiskReviewCommandRejectsNoncanonicalThresholdNumbers(t *testing.T) {
	payload := `{
  "command":"evaluate",
  "candidate":{"revision_id":"01948c1e-0000-7000-8000-000000000001","config_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","version_manifest_hash":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
  "baseline":{"type":"NO_BASELINE"},
  "policy":{"id":"policy","version":"v1","hash":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"},
  "policy_requirements":[{"scene_id":"scene","scene_version":"v1","metric_id":"metric-resource","metric_version":"v1","role":"required"}],
  "threshold":{"type":"MODIFIED_STARTER","confirmed":true,"body":{"schema_version":"v1","source":"starter","assumptions":[],"entries":[{"scene_id":"scene","scene_version":"v1","metric_id":"metric-resource","metric_version":"v1","balance_group":null,"unit":"ratio","direction":"target_range","relative_warning":"01","relative_block":"0.25","absolute_warning":null,"absolute_block":null}],"structural_rule_versions":[{"id":"structure","version":"v1","hash":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}]}},
  "simulation_runs":[{"run_id":"01948c1e-0000-7000-8000-000000000003","result_hash":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}]
}`
	if _, err := DecodeRiskReviewCommand(strings.NewReader(payload)); err == nil {
		t.Fatal("noncanonical decimal string accepted")
	}
	payload = strings.Replace(payload, `"01"`, `0.1`, 1)
	if _, err := DecodeRiskReviewCommand(strings.NewReader(payload)); err == nil {
		t.Fatal("numeric JSON threshold accepted instead of canonical string")
	}
}

func TestDecodeNumericDecisionRequiresNonemptyReasonAndExactItems(t *testing.T) {
	raw, err := os.ReadFile("../../api/fixtures/risk-decision-request.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{
		strings.Replace(string(raw), `"reason": "Accepted for this release after reviewing the exact numeric evidence."`, `"reason": " "`, 1),
		strings.Replace(string(raw), `"item_hash": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"`, `"item_hash": "client-pass"`, 1),
	} {
		if _, err = DecodeRiskReviewCommand(strings.NewReader(payload)); err == nil {
			t.Fatal("invalid numeric decision accepted")
		}
	}
}
