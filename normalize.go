package autocomplete

import (
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// letters with no NFD decomposition, folded to their usual ASCII spelling
var foldedLetters = map[rune]string{
	'ß': "ss", 'æ': "ae", 'ø': "o", 'đ': "d", 'ł': "l", 'œ': "oe", 'ı': "i", 'þ': "th",
}

// Normalize returns lowercase ASCII words separated by single spaces ("Țara Șoimilor" -> "tara soimilor").
// Letters are matched without diacritics; anything that is not a letter or a digit separates words.
func Normalize(s string) string {
	return string(appendNormalized(nil, s, 0, nil))
}

// appendNormalized appends the normalized form of s to dst. When limit > 0 it stops before the normalized text
// would exceed limit bytes; the result never ends in a space. When src is not nil, the index (in runes) of the
// character of s each appended byte came from is appended to it, so matches can be highlighted in the original
// text.
func appendNormalized(dst []byte, s string, limit int, src *[]int) []byte {
	start := len(dst)
	pendingSpace := false
	full := false

	emit := func(c byte, idx int) {
		n := len(dst) - start
		if pendingSpace && n > 0 {
			if limit > 0 && n+2 > limit {
				full = true
				return
			}
			dst = append(dst, ' ')
			if src != nil {
				*src = append(*src, idx)
			}
		} else if limit > 0 && n+1 > limit {
			full = true
			return
		}
		pendingSpace = false
		dst = append(dst, c)
		if src != nil {
			*src = append(*src, idx)
		}
	}

	var rbuf [utf8.UTFMax]byte
	var nbuf [4 * utf8.UTFMax]byte
	idx := 0
	for _, r := range s {
		switch {
		case r < unicode.MaxASCII:
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
				emit(byte(r), idx)
			case r >= 'A' && r <= 'Z':
				emit(byte(r+'a'-'A'), idx)
			default:
				pendingSpace = true
			}
		case unicode.Is(unicode.Mn, r):
			// combining mark of a decomposed letter: neither a letter nor a separator
		default:
			r = unicode.ToLower(r)
			var nfd []byte
			if folded, ok := foldedLetters[r]; ok {
				nfd = norm.NFD.AppendString(nbuf[:0], folded)
			} else {
				nfd = norm.NFD.Append(nbuf[:0], utf8.AppendRune(rbuf[:0], r)...)
			}
			wrote := false
			for _, c := range nfd {
				if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
					emit(c, idx)
					wrote = true
				}
			}
			if !wrote {
				pendingSpace = true
			}
		}
		if full {
			break
		}
		idx++
	}
	return dst
}
