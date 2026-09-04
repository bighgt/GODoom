package mapinfo

import "strings"

// A tiny scanner for UMAPINFO: identifiers / numbers as bare words, "..."
// strings (escapes: \" and \\), the punctuation `= , { }`, and `//`,
// `/* */` and `;`-to-EOL comments. Whitespace is dropped; line numbers are
// kept for diagnostics. It never fails — an unterminated string or comment
// just runs to end of input.

type tokKind int

const (
	tWord tokKind = iota // identifier or number (unquoted)
	tString              // contents of a "..." literal
	tEq
	tComma
	tLBrace
	tRBrace
	tEOF
)

type token struct {
	kind tokKind
	text string
	line int
}

func lexMapInfo(src []byte) []token {
	toks := make([]token, 0, len(src)/6+8)
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
		case c == ';': // some lumps use ; comments
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
		case c == '=':
			emit(tEq, "=")
			i++
		case c == ',':
			emit(tComma, ",")
			i++
		case c == '"':
			i++
			var b strings.Builder
			for i < n && src[i] != '"' {
				if src[i] == '\\' && i+1 < n {
					i++
				} else if src[i] == '\n' {
					line++
				}
				b.WriteByte(src[i])
				i++
			}
			i++ // closing quote (or EOF)
			emit(tString, b.String())
		default:
			start := i
			for i < n {
				d := src[i]
				if d == ' ' || d == '\t' || d == '\r' || d == '\n' || d == '\f' || d == '\v' ||
					d == '{' || d == '}' || d == '=' || d == ',' || d == '"' || d == ';' {
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
