package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/zouyi/eco-guardian/internal/httpapi/riskdto"
)

func TestRuntimeV1GeneratedDTOFixturesAndDigestManifest(t *testing.T) {
	root := "../../api/fixtures/runtime-v1"
	manifestBody, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		FixtureVersion string            `json:"fixture_version"`
		SchemaVersion  int               `json:"runtime_status_schema_version"`
		Files          map[string]string `json:"files"`
	}
	if err = json.Unmarshal(manifestBody, &manifest); err != nil || manifest.FixtureVersion != "1.0" || manifest.SchemaVersion != 1 || len(manifest.Files) != 3 {
		t.Fatalf("runtime fixture manifest=%#v err=%v", manifest, err)
	}
	for name, expected := range manifest.Files {
		body, readErr := os.ReadFile(filepath.Join(root, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		digest := sha256.Sum256(body)
		if actual := hex.EncodeToString(digest[:]); actual != expected {
			t.Fatalf("fixture %s digest=%s want=%s", name, actual, expected)
		}
	}
	matrixBody, err := os.ReadFile(filepath.Join(root, "runtime-status-matrix.json"))
	if err != nil {
		t.Fatal(err)
	}
	var matrix []riskdto.RuntimeStatusResource
	if err = json.Unmarshal(matrixBody, &matrix); err != nil || len(matrix) != 3 {
		t.Fatalf("runtime matrix entries=%d err=%v", len(matrix), err)
	}
	seenPhases, seenModes := map[string]bool{}, map[string]bool{}
	for _, status := range matrix {
		seenPhases[string(status.Phase)] = true
		seenModes[string(status.Build.PackageMode)] = true
		encoded, encodeErr := json.Marshal(status)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		for _, forbidden := range []string{"fixture-secret", "business-payload", "embedding", "SELECT ", "/Users/", `C:\\Users\\`} {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("runtime fixture leaked %q: %s", forbidden, encoded)
			}
		}
	}
	for _, phase := range []string{"ready", "degraded"} {
		if !seenPhases[phase] {
			t.Errorf("runtime fixture omitted phase %s", phase)
		}
	}
	for _, mode := range []string{"complete", "lightweight", "development"} {
		if !seenModes[mode] {
			t.Errorf("runtime fixture omitted package mode %s", mode)
		}
	}
	problemsBody, err := os.ReadFile(filepath.Join(root, "settings-validation-problems.json"))
	if err != nil {
		t.Fatal(err)
	}
	var problems []riskdto.Problem
	if err = json.Unmarshal(problemsBody, &problems); err != nil || len(problems) != 2 || problems[0].FieldPath == nil {
		t.Fatalf("settings problem fixtures=%#v err=%v", problems, err)
	}
}

func TestOpenAPIContainsAllHandlerOperations(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract := string(raw)
	for _, id := range []string{"selectProjectDirectory", "openProject", "listRecentProjects", "openRecentProject", "getCurrentProject", "closeProject", "getEntitySchema", "listEntities", "createEntity", "getEntity", "patchEntity", "deleteEntity", "createValidationRun", "getValidationRun", "listRevisions", "createRevision", "getRevision", "getRevisionDiff", "ensureGraphSync", "getGraphStatus", "listReleasePolicies", "createReleasePolicy", "listReleases", "createRelease", "getRelease", "getJob", "streamJobEvents", "cancelJob", "createSimulationJob", "getSimulationRun", "createRiskReview", "getRiskReview", "createAIDesignJob", "getDraftPatch", "acceptDraftPatch", "discardDraftPatch", "getSettings", "patchSettings", "putProviderCredential", "deleteProviderCredential", "getRuntimeStatus", "getRuntimeCapabilities"} {
		if !strings.Contains(contract, "operationId: "+id) {
			t.Errorf("OpenAPI missing handler operation %s", id)
		}
	}
}

func TestOpenAPIContainsStrictAIDesignContracts(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract := string(raw)
	for _, schema := range []string{"CreateAIDesignJobRequest", "AIDesignJobAccepted", "DraftPatchResource", "AcceptDraftPatchRequest", "AcceptDraftPatchResult", "DiscardDraftPatchRequest", "DiscardDraftPatchResult", "AIAttemptProjection", "AIPreviewProjection", "AIFreshnessProjection", "AIEvidenceRef", "PutProviderCredentialRequest", "ProviderCredentialStatus", "CredentialSource", "AICapabilityReason"} {
		if !strings.Contains(contract, "    "+schema+":") {
			t.Errorf("OpenAPI missing AI schema %s", schema)
		}
	}
	for _, code := range []string{"AI_IDEMPOTENCY_REQUIRED", "AI_PROVIDER_UNCONFIGURED", "AI_INPUT_INVALID", "AI_SNAPSHOT_IDENTITY_MISMATCH", "AI_SNAPSHOT_INDEX_NOT_READY", "AI_PROVIDER_TIMEOUT", "AI_OUTPUT_INVALID", "AI_TOOL_POLICY_VIOLATION", "AI_BUDGET_EXCEEDED", "AI_PATCH_STALE", "AI_DECISION_CONFLICT", "AI_INTERRUPTED", "AI_CREDENTIAL_INVALID", "AI_CREDENTIAL_STORE_FAILED"} {
		if !strings.Contains(contract, code) {
			t.Errorf("OpenAPI missing AI problem code %s", code)
		}
	}
	for _, strictSchema := range []string{"CreateAIDesignJobRequest:", "AcceptDraftPatchRequest:", "DiscardDraftPatchRequest:", "DraftPatchResource:"} {
		start := strings.Index(contract, "    "+strictSchema)
		if start < 0 || !strings.Contains(contract[start:start+min(600, len(contract)-start)], "additionalProperties: false") {
			t.Errorf("AI schema %s is not strict", strictSchema)
		}
	}
}

func TestCredentialGeneratedDTOFixtureIsWriteOnly(t *testing.T) {
	raw, err := os.ReadFile("../../api/fixtures/ai-credential-put-request.json")
	if err != nil {
		t.Fatal(err)
	}
	var request riskdto.PutProviderCredentialRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	if request.Credential == nil || *request.Credential != "fixture-only-not-a-real-secret" {
		t.Fatalf("generated credential request lost write-only value: %#v", request)
	}
	encoded, err := json.Marshal(riskdto.ProviderCredentialStatus{
		Provider:          riskdto.ProviderCredentialStatusProviderOpenaiCompatible,
		CredentialPresent: true,
	})
	if err != nil || strings.Contains(string(encoded), *request.Credential) {
		t.Fatalf("credential status leaked request value: %s err=%v", encoded, err)
	}
}

func TestAIDesignGeneratedDTOFixtures(t *testing.T) {
	request, err := os.ReadFile("../../api/fixtures/ai-design-job-request.json")
	if err != nil {
		t.Fatal(err)
	}
	var input riskdto.CreateAIDesignJobRequest
	if err := json.Unmarshal(request, &input); err != nil {
		t.Fatal(err)
	}
	if len(input.Goals) != 1 || len(input.AllowedTargets) != 1 || input.RequestedBudget == nil || input.RequestedBudget.MaxFormatRepairs != 3 {
		t.Fatalf("generated AI request DTO lost required fields: %#v", input)
	}
	accepted, err := os.ReadFile("../../api/fixtures/ai-design-job-accepted.json")
	if err != nil {
		t.Fatal(err)
	}
	var result riskdto.AIDesignJobAccepted
	if err := json.Unmarshal(accepted, &result); err != nil {
		t.Fatal(err)
	}
	if result.Location == "" || result.Job.Kind != "ai_design" || result.DraftPatchUrl != nil {
		t.Fatalf("generated AI accepted DTO lost Job links: %#v", result)
	}
}

func TestAIDesignProblemFixturesHaveStableStatusMappings(t *testing.T) {
	raw, err := os.ReadFile("../../api/fixtures/problems.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Code   string `json:"code"`
		Status int    `json:"status"`
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		"AI_IDEMPOTENCY_REQUIRED": 400, "AI_PROVIDER_UNCONFIGURED": 409, "AI_CAPABILITY_UNAVAILABLE": 503,
		"AI_INPUT_INVALID": 400, "AI_EVIDENCE_UNAVAILABLE": 422, "AI_SNAPSHOT_IDENTITY_MISMATCH": 422,
		"AI_SNAPSHOT_INDEX_NOT_READY": 409, "AI_RETRIEVAL_UNAVAILABLE": 503, "AI_PROVIDER_FAILED": 502,
		"AI_PROVIDER_TIMEOUT": 504, "AI_OUTPUT_INVALID": 422, "AI_REPAIR_EXHAUSTED": 422,
		"AI_TOOL_POLICY_VIOLATION": 422, "AI_BUDGET_EXCEEDED": 422, "AI_PREVIEW_BLOCKED": 422,
		"AI_PATCH_NOT_FOUND": 404, "AI_PATCH_NOT_ACCEPTABLE": 409, "AI_PATCH_STALE": 409,
		"AI_DECISION_CONFLICT": 409, "AI_CANCELED": 409, "AI_INTERRUPTED": 409,
		"AI_CREDENTIAL_INVALID": 400, "AI_CREDENTIAL_STORE_FAILED": 503,
	}
	got := map[string]int{}
	for _, fixture := range fixtures {
		got[fixture.Code] = fixture.Status
	}
	for code, status := range want {
		if got[code] != status {
			t.Errorf("%s status=%d want %d", code, got[code], status)
		}
	}
}

func TestOpenAPIContainsGraphStatusAndProblemContracts(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract := string(raw)
	for _, schema := range []string{
		"GraphSyncRequest", "GraphSyncJobAccepted", "GraphStatus", "GraphProjectionSummary",
		"GraphProviderObservation", "GraphComponent", "GraphEvidence", "GraphSafeError",
	} {
		if !strings.Contains(contract, "    "+schema+":") {
			t.Errorf("OpenAPI missing graph schema %s", schema)
		}
	}
	for _, code := range []string{
		"GRAPH_VALIDATION_REQUIRED", "GRAPH_VALIDATION_BLOCKED", "PROJECTOR_VERSION_UNAVAILABLE",
		"GRAPH_CAPABILITY_UNAVAILABLE", "CONTENT_HASH_MISMATCH", "CONTENT_HASH_CONFLICT",
		"PROVIDER_TASK_FAILED", "GRAPH_RETRY_EXHAUSTED",
	} {
		if !strings.Contains(contract, code) {
			t.Errorf("OpenAPI missing graph problem code %s", code)
		}
	}
}

func TestOpenAPIContainsSimulationProblemContracts(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract := string(raw)
	for _, code := range []string{
		"SIMULATION_IDEMPOTENCY_REQUIRED", "SIMULATION_INPUT_INVALID", "SIMULATION_SEED_INVALID",
		"SIMULATION_VALIDATION_REQUIRED", "SIMULATION_SOURCE_INVALID", "SIMULATION_SCENE_INVALID",
		"SIMULATION_PARAMETER_INVALID", "SIMULATION_METRIC_INVALID", "SIMULATION_SAMPLE_INVALID",
		"SIMULATION_BUDGET_INVALID", "SIMULATION_IMPLEMENTATION_UNAVAILABLE",
		"SIMULATION_VERIFICATION_TARGET_INVALID", "SIMULATION_CAPABILITY_UNAVAILABLE",
		"SIMULATION_RUN_NOT_FOUND", "SIMULATION_RUN_UNAVAILABLE", "BUDGET_EXCEEDED", "TIMEOUT",
		"RECOVERY_MISMATCH", "RECOVERY_UNAVAILABLE",
	} {
		if !strings.Contains(contract, code) {
			t.Errorf("OpenAPI missing simulation problem code %s", code)
		}
	}
}

func TestOpenAPISharedJobContractIncludesRegisteredKindsAndCancellation(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract := string(raw)
	for _, fragment := range []string{"Registered durable Job kind", "cancel_generation:", "cancel_requested_at:", "Last-Event-ID"} {
		if !strings.Contains(contract, fragment) {
			t.Errorf("OpenAPI missing shared Job contract fragment %q", fragment)
		}
	}
}

func TestVersioningDTOsAreGeneratedRatherThanHandwritten(t *testing.T) {
	duplicate := regexp.MustCompile(`(?m)^(?:export\s+)?(?:interface|type)\s+(?:Revision|Release|Job|Gate|VersionManifest)\w*`)
	for _, root := range []string{".", "../../web/src/features/versions"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || path == "contract_test.go" || strings.Contains(path, "riskdto/risk.gen.go") {
				return nil
			}
			contents, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			if duplicate.Match(contents) {
				t.Errorf("handwritten versioning DTO in %s; use generated client DTOs", path)
			}
			return nil
		})
		if root == "../../web/src/features/versions" && os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestSimulationClientUsesGeneratedDTOsRatherThanHandwrittenDuplicates(t *testing.T) {
	path := "../../web/src/api/simulation.ts"
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	if !strings.Contains(text, "import type { components } from './generated'") {
		t.Fatal("simulation client must use OpenAPI-generated component schemas")
	}
	duplicate := regexp.MustCompile(`(?m)^(?:export\s+)?(?:interface\s+Simulation\w*|type\s+Simulation\w*\s*=\s*\{)`)
	if duplicate.MatchString(text) {
		t.Fatal("simulation client declares a handwritten DTO; use generated component schemas")
	}
}

func TestVersioningProblemFixturesHaveStableStatusMappings(t *testing.T) {
	raw, err := os.ReadFile("../../api/fixtures/problems.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Code   string `json:"code"`
		Status int    `json:"status"`
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		"REVISION_NOT_FOUND": 404, "REVISION_IMMUTABLE": 409, "REVISION_CONFLICT": 409,
		"DIFF_BASE_INVALID": 400, "RELEASE_POLICY_INVALID": 400, "RELEASE_CAPABILITY_DISABLED": 409,
		"RELEASE_PREFLIGHT_FAILED": 422, "RELEASE_BASE_CONFLICT": 409, "IDEMPOTENCY_CONFLICT": 409,
		"MANDATORY_BACKUP_FAILED": 502, "MIGRATION_BACKUP_REQUIRED": 409, "INVALID_CONFIRMATION": 400,
		"HISTORY_NOT_FOUND": 404, "STORAGE_FAILURE": 500, "EXTERNAL_SERVICE_FAILURE": 503,
		"GRAPH_VALIDATION_REQUIRED": 409, "GRAPH_VALIDATION_BLOCKED": 409,
		"PROJECTOR_VERSION_UNAVAILABLE": 409, "GRAPH_CAPABILITY_UNAVAILABLE": 503,
		"BASE_SNAPSHOT_NOT_FOUND": 409, "BASE_SNAPSHOT_NOT_READY": 409,
		"CONTENT_HASH_MISMATCH": 422, "CONTENT_HASH_CONFLICT": 409,
		"PROVIDER_TASK_FAILED": 422, "GRAPH_RETRY_EXHAUSTED": 503,
		"GRAPH_RETRY_NOT_SAFE": 409, "GRAPH_STATUS_UNAVAILABLE": 503,
		"SIMULATION_IDEMPOTENCY_REQUIRED": 400, "SIMULATION_INPUT_INVALID": 400, "SIMULATION_SEED_INVALID": 400,
		"SIMULATION_VALIDATION_REQUIRED": 409, "SIMULATION_SOURCE_INVALID": 400,
		"SIMULATION_SCENE_INVALID": 400, "SIMULATION_PARAMETER_INVALID": 400,
		"SIMULATION_METRIC_INVALID": 400, "SIMULATION_SAMPLE_INVALID": 400,
		"SIMULATION_BUDGET_INVALID": 400, "SIMULATION_IMPLEMENTATION_UNAVAILABLE": 409,
		"SIMULATION_VERIFICATION_TARGET_INVALID": 400, "SIMULATION_CAPABILITY_UNAVAILABLE": 503,
		"SIMULATION_RUN_NOT_FOUND": 404, "SIMULATION_RUN_UNAVAILABLE": 503,
		"BUDGET_EXCEEDED": 422, "TIMEOUT": 504, "RECOVERY_MISMATCH": 409, "RECOVERY_UNAVAILABLE": 409,
		"RISK_IDEMPOTENCY_REQUIRED": 400, "RISK_COMMAND_INVALID": 400,
		"RISK_REVISION_INVALID": 409, "RISK_BASELINE_INVALID": 409, "RISK_POLICY_INVALID": 409,
		"THRESHOLD_NOT_CONFIGURED": 409, "THRESHOLD_INVALID": 422, "RISK_VALIDATION_INVALID": 409,
		"RISK_SIMULATION_INVALID": 409, "RISK_COHORT_INVALID": 422, "RISK_REQUIRED_METRIC_UNAVAILABLE": 422,
		"RISK_STALE_IDENTITY": 409, "RISK_REGISTRY_INCOMPATIBLE": 409, "RISK_RULE_CONTRACT_INVALID": 409,
		"RISK_DECISION_INVALID": 400, "RISK_REVIEW_NOT_FOUND": 404, "RISK_CANCELED": 409, "RISK_INTERRUPTED": 409,
	}
	got := map[string]int{}
	for _, fixture := range fixtures {
		got[fixture.Code] = fixture.Status
	}
	for code, status := range want {
		if got[code] != status {
			t.Errorf("%s status=%d, want %d", code, got[code], status)
		}
	}
}

func TestOpenAPIContainsRiskReviewContractsAndFrozenErrors(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract := string(raw)
	for _, schema := range []string{
		"RiskReviewCommand", "EvaluateRiskReviewCommand", "RecordNumericDecisionCommand", "RiskJobAccepted",
		"RiskReview", "RiskReviewItem", "RiskMetricEvidence", "RiskStructuralEvidence", "RiskReadTimeProjection",
	} {
		if !strings.Contains(contract, "    "+schema+":") {
			t.Errorf("OpenAPI missing risk schema %s", schema)
		}
	}
	for _, fragment := range []string{
		"command: { type: string, const: evaluate }", "command: { type: string, const: record_numeric_decision }",
		"MATERIALIZED, COMPARING, STRUCTURE_CHECKING and SEALING", "additionalProperties: false",
		"Severity, Gate state and override outcomes are always server-derived",
	} {
		if !strings.Contains(contract, fragment) {
			t.Errorf("OpenAPI missing risk contract fragment %q", fragment)
		}
	}
}

func TestGeneratedRiskDTOsDecodeFrozenRequestAndResponseFixtures(t *testing.T) {
	for _, fixture := range []string{"risk-evaluate-request.json", "risk-decision-request.json"} {
		raw, err := os.ReadFile(filepath.Join("../../api/fixtures", fixture))
		if err != nil {
			t.Fatal(err)
		}
		var command riskdto.RiskReviewCommand
		if err = json.Unmarshal(raw, &command); err != nil {
			t.Fatalf("%s: %v", fixture, err)
		}
		if _, err = command.ValueByDiscriminator(); err != nil {
			t.Fatalf("%s discriminator: %v", fixture, err)
		}
	}
	raw, err := os.ReadFile("../../api/fixtures/risk-job-accepted.json")
	if err != nil {
		t.Fatal(err)
	}
	var accepted riskdto.RiskJobAccepted
	if err = json.Unmarshal(raw, &accepted); err != nil || accepted.Job.Kind != "risk" || accepted.Location == "" {
		t.Fatalf("accepted=%#v err=%v", accepted, err)
	}
}

func TestRiskHTTPDTOsAreGeneratedRatherThanHandwritten(t *testing.T) {
	duplicate := regexp.MustCompile(`(?m)^type\s+(?:RiskReview|EvaluateRisk|RecordNumericDecision|RiskMetricEvidence)\w*\s`)
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || strings.Contains(path, "riskdto/risk.gen.go") || path == "contract_test.go" {
			return nil
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if duplicate.Match(contents) {
			t.Errorf("handwritten risk HTTP DTO in %s; use generated bindings", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAPIContainsRevisionContractSchemas(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract := string(raw)
	for _, schema := range []string{
		"RevisionVersionManifest", "RevisionMetadata", "RevisionHistoryItem", "RevisionDetail",
		"RevisionPage", "RevisionTimeline", "CurrentWorkingPrecondition", "CreateRevisionRequest",
	} {
		if !strings.Contains(contract, "    "+schema+":") {
			t.Errorf("OpenAPI missing revision schema %s", schema)
		}
	}
}
