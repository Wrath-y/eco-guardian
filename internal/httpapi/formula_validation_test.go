package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/httpapi/riskdto"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

func TestFormulaHandlerValidatesUnitsOutputAndReadableAttributeKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	schemas, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	project, _, err := store.Create(context.Background(), t.TempDir(), schemas)
	if err != nil {
		t.Fatal(err)
	}
	defer project.Close()
	health, _, err := project.Create(context.Background(), domain.KindAttribute, domain.EntityDraft{
		Key: "health", Name: "Health", Payload: map[string]json.RawMessage{
			"value_type": json.RawMessage(`"decimal"`), "dimension": json.RawMessage(`"health"`),
			"base_unit": json.RawMessage(`"health_point"`), "default": json.RawMessage(`"100"`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	NewFormulaHandler(func() EntityStore { return project }).Register(engine)

	tests := []struct {
		name       string
		expression string
		valid      bool
		code       riskdto.FormulaDiagnosticCode
	}{
		{"matching quantity", `80[health_point]`, true, ""},
		{"readable key selector", `${self:health} + 10[health_point]`, true, ""},
		{"mixed dimensions", `80[health_point] + 10[damage_point]`, false, riskdto.FORMULAUNITMISMATCH},
		{"wrong output dimension", `10[damage_point]`, false, riskdto.FORMULATYPEMISMATCH},
		{"constant division by zero", `80[health_point] / 0[scalar]`, false, riskdto.FORMULADIVISIONBYZERO},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, _ := json.Marshal(riskdto.FormulaValidationRequest{Expression: test.expression, OutputAttributeId: riskUUIDPtr(health.ID)})
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/formula-validations", bytes.NewReader(body)))
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var result riskdto.FormulaValidationResult
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Valid != test.valid {
				t.Fatalf("valid=%v diagnostics=%#v", result.Valid, result.Diagnostics)
			}
			if !test.valid && (len(result.Diagnostics) == 0 || result.Diagnostics[0].Code != test.code) {
				t.Fatalf("diagnostics=%#v", result.Diagnostics)
			}
		})
	}
}

func TestFormulaHandlerRequiresActiveProject(t *testing.T) {
	engine := gin.New()
	NewFormulaHandler(func() EntityStore { return nil }).Register(engine)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/formula-validations", bytes.NewBufferString(`{"expression":"1"}`)))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
