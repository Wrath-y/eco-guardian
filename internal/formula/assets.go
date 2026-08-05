package formula

import _ "embed"

// DSLVersion identifies the persisted grammar and AST contract.
const DSLVersion = "dsl-v1"

// ASTSchemaVersion identifies the canonical AST wire representation.
const ASTSchemaVersion = "ast-v1"

//go:embed dsl-v1.ebnf
var DSLGrammar string
