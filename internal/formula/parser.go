package formula

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type SyntaxError struct {
	Span    Span
	Message string
}
type ParseResult struct {
	AST    AST
	Errors []SyntaxError
}
type token struct {
	kind, text string
	span       Span
}

func Parse(source string, registry *Registry) ParseResult {
	tokens, errs := lex(source)
	p := &parser{tokens: tokens, registry: registry, errors: errs}
	root := p.expression()
	if p.peek().kind != "eof" {
		p.error(p.peek().span, "unexpected token")
	}
	return ParseResult{AST: NewAST(root), Errors: p.errors}
}

func lex(source string) ([]token, []SyntaxError) {
	tokens, errs := []token{}, []SyntaxError{}
	for offset := 0; offset < len(source); {
		start := offset
		r, width := utf8.DecodeRuneInString(source[offset:])
		if unicode.IsSpace(r) {
			offset += width
			continue
		}
		if strings.ContainsRune("()+-*/!,[]", r) {
			tokens = append(tokens, token{string(r), string(r), Span{start, start + width}})
			offset += width
			continue
		}
		if strings.ContainsRune("<>=&|", r) {
			offset += width
			if offset < len(source) && source[offset] == '=' {
				offset++
			} else if (r == '&' || r == '|') && offset < len(source) && source[offset] == byte(r) {
				offset++
			}
			tokens = append(tokens, token{"op", source[start:offset], Span{start, offset}})
			continue
		}
		if r == '$' && strings.HasPrefix(source[offset:], "${") {
			end := strings.IndexByte(source[offset+2:], '}')
			if end < 0 {
				errs = append(errs, SyntaxError{Span{start, len(source)}, "unterminated selector"})
				tokens = append(tokens, token{"selector", source[start:], Span{start, len(source)}})
				break
			}
			offset += 2 + end + 1
			tokens = append(tokens, token{"selector", source[start:offset], Span{start, offset}})
			continue
		}
		if unicode.IsDigit(r) || r == '.' {
			offset += width
			for offset < len(source) && source[offset] >= '0' && source[offset] <= '9' {
				offset++
			}
			if offset < len(source) && source[offset] == '.' {
				offset++
				for offset < len(source) && source[offset] >= '0' && source[offset] <= '9' {
					offset++
				}
			}
			if offset < len(source) && (source[offset] == 'e' || source[offset] == 'E') {
				offset++
				if offset < len(source) && (source[offset] == '+' || source[offset] == '-') {
					offset++
				}
				for offset < len(source) && source[offset] >= '0' && source[offset] <= '9' {
					offset++
				}
			}
			tokens = append(tokens, token{"number", source[start:offset], Span{start, offset}})
			continue
		}
		if unicode.IsLetter(r) || r == '_' {
			offset += width
			for offset < len(source) {
				ch, w := utf8.DecodeRuneInString(source[offset:])
				if unicode.IsLetter(ch) || unicode.IsDigit(ch) || ch == '_' {
					offset += w
				} else {
					break
				}
			}
			tokens = append(tokens, token{"identifier", source[start:offset], Span{start, offset}})
			continue
		}
		errs = append(errs, SyntaxError{Span{start, start + width}, "unsupported character"})
		offset += width
	}
	tokens = append(tokens, token{"eof", "", Span{len(source), len(source)}})
	return tokens, errs
}

type parser struct {
	tokens   []token
	at       int
	registry *Registry
	errors   []SyntaxError
}

func (p *parser) peek() token { return p.tokens[p.at] }
func (p *parser) take() token {
	t := p.peek()
	if p.at < len(p.tokens)-1 {
		p.at++
	}
	return t
}
func (p *parser) error(span Span, message string) {
	p.errors = append(p.errors, SyntaxError{span, message})
}
func (p *parser) match(kinds ...string) (token, bool) {
	for _, kind := range kinds {
		if p.peek().kind == kind || p.peek().text == kind {
			return p.take(), true
		}
	}
	return token{}, false
}
func (p *parser) expression() Node { return p.binary(p.primary(), 0) }
func precedence(op string) int {
	switch op {
	case "||":
		return 1
	case "&&":
		return 2
	case "<", "<=", ">", ">=", "==", "!=":
		return 3
	case "+", "-":
		return 4
	case "*", "/":
		return 5
	}
	return -1
}
func (p *parser) binary(left Node, min int) Node {
	for {
		t := p.peek()
		op := t.text
		level := precedence(op)
		if level < min {
			break
		}
		p.take()
		right := p.binary(p.primary(), level+1)
		left = Node{Kind: NodeBinary, Span: Span{left.Span.StartByte, right.Span.EndByte}, Operator: op, Args: []Node{left, right}}
	}
	return left
}
func (p *parser) primary() Node {
	if t, ok := p.match("-", "!"); ok {
		child := p.primary()
		return Node{Kind: NodeUnary, Span: Span{t.span.StartByte, child.Span.EndByte}, Operator: t.text, Args: []Node{child}}
	}
	if _, ok := p.match("("); ok {
		n := p.expression()
		if _, ok := p.match(")"); !ok {
			p.error(p.peek().span, "expected )")
		}
		return n
	}
	if t, ok := p.match("number"); ok {
		canonical, err := ParseDecimal(t.text)
		if err != nil {
			p.error(t.span, "invalid decimal literal")
			return Node{Kind: NodeInvalid, Span: t.span}
		}
		if _, ok := p.match("["); ok {
			unit := p.take()
			if unit.kind != "identifier" {
				p.error(unit.span, "expected unit")
			}
			if p.registry != nil {
				if _, found := p.registry.Unit(unit.text); !found {
					p.error(unit.span, "unknown unit")
				}
			}
			end := unit.span.EndByte
			if close, ok := p.match("]"); ok {
				end = close.span.EndByte
			} else {
				p.error(p.peek().span, "expected ]")
			}
			return Node{Kind: NodeQuantity, Span: Span{t.span.StartByte, end}, Text: canonical.String(), Unit: unit.text}
		}
		return Node{Kind: NodeDecimal, Span: t.span, Text: canonical.String()}
	}
	if t, ok := p.match("selector"); ok {
		raw := strings.TrimSuffix(strings.TrimPrefix(t.text, "${"), "}")
		fields := strings.SplitN(raw, ":", 2)
		if len(fields) != 2 || fields[1] == "" {
			p.error(t.span, "invalid selector")
			return Node{Kind: NodeInvalid, Span: t.span}
		}
		switch fields[0] {
		case "self", "source", "target", "scenario":
		default:
			p.error(t.span, "unknown selector scope")
		}
		return Node{Kind: NodeSelector, Span: t.span, Scope: fields[0], Symbol: fields[1]}
	}
	if t, ok := p.match("identifier"); ok {
		if t.text == "true" || t.text == "false" {
			return Node{Kind: NodeBoolean, Span: t.span, Text: t.text}
		}
		if _, ok := p.match("("); !ok {
			p.error(t.span, "bare identifiers are not valid expressions")
			return Node{Kind: NodeInvalid, Span: t.span}
		}
		args := []Node{}
		if p.peek().kind != ")" {
			for {
				args = append(args, p.expression())
				if _, ok := p.match(","); !ok {
					break
				}
			}
		}
		close, ok := p.match(")")
		end := t.span.EndByte
		if ok {
			end = close.span.EndByte
		} else {
			p.error(p.peek().span, "expected )")
		}
		if p.registry != nil {
			if _, ok := p.registry.Function(t.text); !ok {
				p.error(t.span, fmt.Sprintf("unknown function %s", t.text))
			}
		}
		return Node{Kind: NodeCall, Span: Span{t.span.StartByte, end}, Text: t.text, Args: args}
	}
	t := p.take()
	p.error(t.span, "expected expression")
	return Node{Kind: NodeInvalid, Span: t.span}
}
