package formula

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type Span struct {
	StartByte int `json:"start_byte"`
	EndByte   int `json:"end_byte"`
}

func (s Span) Valid() bool { return s.StartByte >= 0 && s.EndByte >= s.StartByte }

type NodeKind string

const (
	NodeInvalid  NodeKind = "invalid"
	NodeDecimal  NodeKind = "decimal"
	NodeBoolean  NodeKind = "boolean"
	NodeQuantity NodeKind = "quantity"
	NodeSelector NodeKind = "selector"
	NodeCall     NodeKind = "call"
	NodeUnary    NodeKind = "unary"
	NodeBinary   NodeKind = "binary"
)

// Node is a versioned, data-only AST. No executable code or dynamic target is
// represented by this wire format.
type Node struct {
	Kind     NodeKind `json:"kind"`
	Span     Span     `json:"span"`
	Text     string   `json:"text,omitempty"`
	Unit     string   `json:"unit,omitempty"`
	Scope    string   `json:"scope,omitempty"`
	Symbol   string   `json:"symbol,omitempty"`
	Operator string   `json:"operator,omitempty"`
	Args     []Node   `json:"args,omitempty"`
}
type AST struct {
	Version string `json:"version"`
	Root    Node   `json:"root"`
}

func NewAST(root Node) AST                    { return AST{Version: ASTSchemaVersion, Root: root} }
func (a AST) CanonicalBytes() ([]byte, error) { return json.Marshal(a) }
func (a AST) Hash() (string, error) {
	bytes, err := a.CanonicalBytes()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:]), nil
}
