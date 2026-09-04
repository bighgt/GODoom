package gldefs

// A tiny scanner for the GLDEFS text format. GLDEFS is whitespace-delimited
// words, brace-nested blocks, `//` and `/* */` comments and double-quoted
// strings — the same shape as most id/ZDoom text lumps. This is a
// clean-room reader written from the ZDoom wiki's format description; no
// GZDoom source is used.

type tokKind int

const (
	tWord tokKind = iota // a bare run of non-space, non-brace, non-quote chars
	tString              // the contents of a "..." literal (quotes stripped)
	tLBrace
	tRBrace
)

type token struct {
	kind tokKind
	text string
	line int
}

// lex turns GLDEFS source into a flat token slice. Comments and whitespace
// are dropped; line numbers are kept for diagnostics. It never fails — an
// unterminated string or block comment just runs to end of input.
func lex(src []byte) []token {
	toks := make([]token, 0, len(src)/6+8)
	line := 1
	n := len(src)
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
			for i < n {
				if src[i] == '*' && i+1 < n && src[i+1] == '/' {
					i += 2
					break
				}
				if src[i] == '\n' {
					line++
				}
				i++
			}
		case c == '{':
			toks = append(toks, token{tLBrace, "{", line})
			i++
		case c == '}':
			toks = append(toks, token{tRBrace, "}", line})
			i++
		case c == '"':
			i++
			start := i
			for i < n && src[i] != '"' {
				if src[i] == '\n' {
					line++
				}
				i++
			}
			toks = append(toks, token{tString, string(src[start:i]), line})
			if i < n {
				i++ // consume closing quote
			}
		default:
			start := i
			for i < n {
				b := src[i]
				if b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == '\f' || b == '\v' ||
					b == '{' || b == '}' || b == '"' {
					break
				}
				if b == '/' && i+1 < n && (src[i+1] == '/' || src[i+1] == '*') {
					break
				}
				i++
			}
			if i > start {
				toks = append(toks, token{tWord, string(src[start:i]), line})
			} else {
				i++ // defensive: never stall
			}
		}
	}
	return toks
}
