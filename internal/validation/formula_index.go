package validation

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
)

type FormulaIndexRecord struct {
	SourceID          domain.ID
	FieldPath         string
	OutputAttributeID domain.ID
	FormulaHash       string
	ASTHash           string
	AST               []byte
	ASTVersion        string
	DSLVersion        string
	RegistryVersion   string
	Reads             []FormulaRead
}
type FormulaRead struct {
	Scope  string
	Symbol string
	Span   formula.Span
}
type FormulaDiagnostic struct {
	SourceID  domain.ID
	FieldPath string
	Code      string
	Span      formula.Span
}

func CompileFormulaBindings(bindings []FormulaTuple, registry *formula.Registry, symbols formula.SymbolResolver) ([]FormulaIndexRecord, []FormulaDiagnostic) {
	records := []FormulaIndexRecord{}
	diagnostics := []FormulaDiagnostic{}
	for _, binding := range bindings {
		parsed := formula.Parse(binding.Expression, registry)
		for _, syntax := range parsed.Errors {
			diagnostics = append(diagnostics, FormulaDiagnostic{binding.SourceID, binding.FieldPath, "FORMULA_SYNTAX_INVALID", formula.Span{StartByte: syntax.Span.StartByte, EndByte: syntax.Span.EndByte}})
		}
		_, inference := formula.Infer(parsed.AST.Root, registry, symbols)
		for _, issue := range inference {
			diagnostics = append(diagnostics, FormulaDiagnostic{binding.SourceID, binding.FieldPath, issue.Code, issue.Span})
		}
		if len(parsed.Errors) > 0 || len(inference) > 0 {
			continue
		}
		hash, err := parsed.AST.Hash()
		if err != nil {
			diagnostics = append(diagnostics, FormulaDiagnostic{binding.SourceID, binding.FieldPath, "FORMULA_SYNTAX_INVALID", binding.Span})
			continue
		}
		encoded, err := parsed.AST.CanonicalBytes()
		if err != nil {
			diagnostics = append(diagnostics, FormulaDiagnostic{binding.SourceID, binding.FieldPath, "FORMULA_SYNTAX_INVALID", binding.Span})
			continue
		}
		formulaSum := sha256.Sum256([]byte(binding.Expression))
		reads := formulaReads(parsed.AST.Root)
		records = append(records, FormulaIndexRecord{binding.SourceID, binding.FieldPath, binding.OutputAttributeID, hex.EncodeToString(formulaSum[:]), hash, encoded, formula.ASTSchemaVersion, formula.DSLVersion, registry.ManifestHash(), reads})
	}
	sort.Slice(records, func(i, j int) bool {
		return string(records[i].SourceID)+records[i].FieldPath < string(records[j].SourceID)+records[j].FieldPath
	})
	sort.Slice(diagnostics, func(i, j int) bool {
		return string(diagnostics[i].SourceID)+diagnostics[i].FieldPath+diagnostics[i].Code < string(diagnostics[j].SourceID)+diagnostics[j].FieldPath+diagnostics[j].Code
	})
	return records, diagnostics
}
func formulaReads(node formula.Node) []FormulaRead {
	out := []FormulaRead{}
	var visit func(formula.Node)
	visit = func(current formula.Node) {
		if current.Kind == formula.NodeSelector {
			out = append(out, FormulaRead{current.Scope, current.Symbol, current.Span})
		}
		for _, child := range current.Args {
			visit(child)
		}
	}
	visit(node)
	sort.Slice(out, func(i, j int) bool { return out[i].Scope+out[i].Symbol < out[j].Scope+out[j].Symbol })
	return out
}
