package texturedefs

// Tokenizer for the ZDoom TEXTURES lump: bare words / numbers, "..." quoted
// strings, the punctuation { } , and `//` `/* */` `#`-to-EOL comments.
// Whitespace (including newlines) is insignificant. Never fails.

type tokKind int

const (
	tWord tokKind = iota
	tString
	tLBrace
	tRBrace
	tComma
	tEOF
)

type token struct {
	kind  tokKind
	text  string
	lower string
	line  int
}

func lex(src []byte) []token {
	var toks []token
	line := 1
	n := len(src)
	push := func(k tokKind, s string) {
		toks = append(toks, token{kind: k, text: s, lower: lowerASCII(s), line: line})
	}

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
		case c == '{':
			push(tLBrace, "{")
			i++
		case c == '}':
			push(tRBrace, "}")
			i++
		case c == ',':
			push(tComma, ",")
			i++
		case c == '"':
			i++
			start := i
			for i < n && src[i] != '"' && src[i] != '\n' {
				i++
			}
			push(tString, string(src[start:i]))
			if i < n && src[i] == '"' {
				i++
			}
		default:
			start := i
			for i < n {
				b := src[i]
				if b <= ' ' || b == '{' || b == '}' || b == ',' || b == '"' || b == '#' {
					break
				}
				if b == '/' && i+1 < n && (src[i+1] == '/' || src[i+1] == '*') {
					break
				}
				i++
			}
			if i > start {
				push(tWord, string(src[start:i]))
			} else {
				i++ // defensive: never stall
			}
		}
	}
	toks = append(toks, token{kind: tEOF, line: line})
	return toks
}

func lowerASCII(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
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
