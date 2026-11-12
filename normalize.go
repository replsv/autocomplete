package autocomplete

import (
	"unicode"

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
			f := folded(r)
			for i := range len(f) {
				emit(f[i], idx)
			}
			if f == "" {
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

// Latin letters folded ahead of time, since the NFD decomposition allocates.
var (
	latinFold    = foldRange(0x80, 0x250)    // Latin-1 Supplement, Latin Extended-A and B
	latinExtFold = foldRange(0x1e00, 0x1f00) // Latin Extended Additional
)

func foldRange(lo, hi rune) []string {
	t := make([]string, hi-lo)
	for r := lo; r < hi; r++ {
		t[r-lo] = fold(r)
	}
	return t
}

// folded is the ASCII spelling of a non-ASCII rune: its lowercase letters and digits without diacritics, or "".
func folded(r rune) string {
	switch {
	case r >= 0x80 && r < 0x250:
		return latinFold[r-0x80]
	case r >= 0x1e00 && r < 0x1f00:
		return latinExtFold[r-0x1e00]
	}
	return fold(r)
}

func fold(r rune) string {
	r = unicode.ToLower(r)
	if f, ok := foldedLetters[r]; ok {
		return f
	}
	var b []byte
	for _, c := range []byte(norm.NFD.String(string(r))) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			b = append(b, c)
		}
	}
	return string(b)
}
