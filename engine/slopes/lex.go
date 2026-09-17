package slopes

import "strings"

// A tiny scanner, adapted from engine/mapinfo's lex.go: bare words
// (identifiers, sector indices, and numbers — including a leading '-' or
// '.', which mapinfo's number-shaped tokens never needed), the punctuation
// `{ }`, and `//` / `/* */` comments. Whitespace is dropped; line numbers
// are kept for diagnostics. Never fails outright — an unterminated comment
// just runs to end of input.

type tokKind int

const (
	tWord tokKind = iota
	tLBrace
	tRBrace
	tEOF
)

type token struct {
	kind tokKind
	text string
	line int
}

func lex(src []byte) []token {
	toks := make([]token, 0, len(src)/4+8)
	line := 1
	n := len(src)
	emit := func(k tokKind, s string) { toks = append(toks, token{k, s, line}) }

	for i := 0; i < n; {
		c := src[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r' || c == '\f' || c == '\v':
			i++
		case c == '/' && i+1 < n && src[i+1] == '/':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			i += 2
			for i+1 < n && !(src[i] == '*' && src[i+1] == '/') {
				if src[i] == '\n' {
					line++
				}
				i++
			}
			i += 2
		case c == '{':
			emit(tLBrace, "{")
			i++
		case c == '}':
			emit(tRBrace, "}")
			i++
		default:
			start := i
			for i < n {
				d := src[i]
				if d == ' ' || d == '\t' || d == '\r' || d == '\n' || d == '\f' || d == '\v' || d == '{' || d == '}' {
					break
				}
				if d == '/' && i+1 < n && (src[i+1] == '/' || src[i+1] == '*') {
					break
				}
				i++
			}
			emit(tWord, string(src[start:i]))
		}
	}
	emit(tEOF, "")
	return toks
}

// parser is a cursor over the token slice.
type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() token { return p.toks[p.pos] }

func (p *parser) next() token {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}

func (p *parser) at(k tokKind) bool { return p.toks[p.pos].kind == k }

func (p *parser) accept(k tokKind) bool {
	if p.at(k) {
		p.next()
		return true
	}
	return false
}

func (p *parser) acceptWordFold(s string) bool {
	if p.at(tWord) && strings.EqualFold(p.peek().text, s) {
		p.next()
		return true
	}
	return false
}
