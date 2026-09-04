package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
	"github.com/zouyi/eco-guardian/internal/httpapi/riskdto"
	"github.com/zouyi/eco-guardian/internal/validation"
)

const maxFormulaExpressionBytes = 10000

type FormulaHandler struct{ store StoreProvider }

func NewFormulaHandler(store StoreProvider) *FormulaHandler { return &FormulaHandler{store: store} }

func (h *FormulaHandler) Register(engine *gin.Engine) {
	engine.POST("/api/v1/formula-validations", h.validate)
}

func (h *FormulaHandler) validate(c *gin.Context) {
	store := h.store()
	if store == nil {
		problem(c, http.StatusNotFound, "PROJECT_NOT_OPEN", "No active project")
		return
	}
	var request riskdto.FormulaValidationRequest
	if err := c.ShouldBindJSON(&request); err != nil || strings.TrimSpace(request.Expression) == "" || len(request.Expression) > maxFormulaExpressionBytes {
		problem(c, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid formula validation request")
		return
	}
	registry, err := formula.V1Registry()
	if err != nil {
		problem(c, http.StatusServiceUnavailable, "VALIDATION_STORAGE_FAILURE", "Formula registry unavailable")
		return
	}
	attributes, err := listFormulaAttributes(c, store)
	if err != nil {
		problem(c, http.StatusInternalServerError, "VALIDATION_STORAGE_FAILURE", "Formula attributes unavailable")
		return
	}
	symbols := validation.AttributeSymbols(attributes, registry, nil)
	parsed := formula.Parse(request.Expression, registry)
	diagnostics := make([]riskdto.FormulaDiagnostic, 0, len(parsed.Errors)+1)
	for _, issue := range parsed.Errors {
		code := riskdto.FORMULASYNTAXINVALID
		if issue.Message == "unknown unit" {
			code = riskdto.FORMULAUNITMISMATCH
		}
		diagnostics = append(diagnostics, formulaDiagnostic(code, issue.Message, issue.Span))
	}
	if len(parsed.Errors) == 0 {
		var inference []formula.Diagnostic
		if request.OutputAttributeId != nil {
			expected, found := validation.OutputAttributeFormulaType(attributes, domain.ID(request.OutputAttributeId.String()), registry)
			if !found {
				problem(c, http.StatusBadRequest, "VALIDATION_FAILED", "Output attribute is unavailable")
				return
			}
			inference = formula.InferOutput(parsed.AST.Root, expected, registry, symbols)
		} else {
			_, inference = formula.Infer(parsed.AST.Root, registry, symbols)
		}
		for _, issue := range inference {
			diagnostics = append(diagnostics, formulaDiagnostic(riskdto.FormulaDiagnosticCode(issue.Code), issue.Message, issue.Span))
		}
		if len(inference) == 0 && !formulaContainsSelector(parsed.AST.Root) {
			_, evaluation := formula.Evaluate(parsed.AST.Root, registry)
			for _, issue := range evaluation {
				diagnostics = append(diagnostics, formulaDiagnostic(riskdto.FormulaDiagnosticCode(issue.Code), issue.Message, issue.Span))
			}
		}
	}
	c.JSON(http.StatusOK, riskdto.FormulaValidationResult{Valid: len(diagnostics) == 0, Diagnostics: diagnostics})
}

func listFormulaAttributes(c *gin.Context, store EntityStore) ([]domain.Entity, error) {
	attributes := []domain.Entity{}
	cursor := ""
	for {
		page, err := store.List(c.Request.Context(), domain.KindAttribute, "", cursor, 200)
		if err != nil {
			return nil, err
		}
		attributes = append(attributes, page.Items...)
		if page.NextCursor == "" {
			return attributes, nil
		}
		cursor = page.NextCursor
	}
}

func formulaContainsSelector(node formula.Node) bool {
	if node.Kind == formula.NodeSelector {
		return true
	}
	for _, child := range node.Args {
		if formulaContainsSelector(child) {
			return true
		}
	}
	return false
}

func formulaDiagnostic(code riskdto.FormulaDiagnosticCode, message string, span formula.Span) riskdto.FormulaDiagnostic {
	return riskdto.FormulaDiagnostic{Code: code, Message: message, StartByte: span.StartByte, EndByte: span.EndByte}
}
