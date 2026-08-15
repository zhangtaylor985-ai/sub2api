package sessiondelivery

// Go's encoding/json escapes '<', '>' and '&' inside string values by default,
// so every delivery record produced by this pipeline carried \u003c, \u003e and
// \u0026 where the client had sent the literal character. The escaping is
// semantically transparent — a parser recovers the same string either way — but
// it is a uniform, whole-corpus divergence from the client's own output.
//
// MEASURED: real Claude Code transcripts contain 609 literal '<' and zero
// \u003c; the archive before this pass contained 67,530 \u003c and zero literal
// '<'. Claude Code writes its JSON from a JavaScript runtime, whose serializer
// does not escape these characters.
//
// unescapeJSONHTML rewrites those three escapes back to the literal character.
// It is applied to a finished record, so it cannot be expressed as an encoder
// option: the escaped bytes were introduced by earlier stages and are already
// baked into the json.RawMessage values this package passes around.

// unescapeJSONHTML converts \u003c, \u003e and \u0026 escapes into '<', '>' and
// '&'. Only escape sequences inside string literals are touched, and only when
// the backslash that introduces them is itself unescaped, so a string whose
// literal text is "\u003c" — a backslash followed by u003c, encoded as
// \\u003c — is left alone. The result decodes to exactly the same value.
func unescapeJSONHTML(document []byte) []byte {
	if len(document) == 0 {
		return document
	}

	var out []byte
	inString := false
	index := 0
	for index < len(document) {
		current := document[index]
		if !inString {
			if current == '"' {
				inString = true
			}
			if out != nil {
				out = append(out, current)
			}
			index++
			continue
		}
		switch current {
		case '"':
			inString = false
			if out != nil {
				out = append(out, current)
			}
			index++
		case '\\':
			if replacement, width, ok := htmlEscapeReplacement(document[index:]); ok {
				if out == nil {
					out = make([]byte, 0, len(document))
					out = append(out, document[:index]...)
				}
				out = append(out, replacement)
				index += width
				continue
			}
			// Any other escape, including an escaped backslash, is copied whole
			// so its second byte can never be read as the start of an escape.
			width := 2
			if index+1 >= len(document) {
				width = 1
			}
			if out != nil {
				out = append(out, document[index:index+width]...)
			}
			index += width
		default:
			if out != nil {
				out = append(out, current)
			}
			index++
		}
	}
	if out == nil {
		return document
	}
	return out
}

func htmlEscapeReplacement(tail []byte) (byte, int, bool) {
	if len(tail) < 6 || tail[0] != '\\' || (tail[1] != 'u' && tail[1] != 'U') {
		return 0, 0, false
	}
	if tail[2] != '0' || tail[3] != '0' {
		return 0, 0, false
	}
	switch {
	case tail[4] == '3' && (tail[5] == 'c' || tail[5] == 'C'):
		return '<', 6, true
	case tail[4] == '3' && (tail[5] == 'e' || tail[5] == 'E'):
		return '>', 6, true
	case tail[4] == '2' && tail[5] == '6':
		return '&', 6, true
	}
	return 0, 0, false
}
