package animdefs

// A minimal tokenizer for the ZDoom ANIMDEFS lump. ANIMDEFS is a flat,
// line-oriented, whitespace-separated token stream: bare words and numbers,
// optional "..." quoted names, and `//` / `/* */` / `#`-to-EOL comments.
// It has no braces — a `flat`/`texture` header owns the `pic` / `range` /
// `oscillate` lines that follow until the next top-level keyword. So all we
// need is the token list (lower-cased copy kept alongside for keyword
// matching) plus line numbers for diagnostics.

type token struct {
	text  string // original case (lump names are case-insensitive but kept as written)
	lower string
	line  int
}

func lex(src []byte) []token {
	var toks []token
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
		case c == '#':
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
		case c == '"':
			i++
			start := i
			for i < n && src[i] != '"' && src[i] != '\n' {
				i++
			}
			s := string(src[start:i])
			toks = append(toks, token{s, lowerASCII(s), line})
			if i < n && src[i] == '"' {
				i++
			}
		default:
			start := i
			for i < n {
				b := src[i]
				if b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == '\f' || b == '\v' || b == '"' {
					break
				}
				if b == '/' && i+1 < n && (src[i+1] == '/' || src[i+1] == '*') {
					break
				}
				if b == '#' {
					break
				}
				i++
			}
			if i > start {
				s := string(src[start:i])
				toks = append(toks, token{s, lowerASCII(s), line})
			}
		}
	}
	return toks
}

func lowerASCII(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			if b == nil {
				b = []byte(s)
			}
			b[i] = c + ('a' - 'A')
		}
	}
	if b == nil {
		return s
	}
	return string(b)
}
