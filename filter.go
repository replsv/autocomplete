package autocomplete

// A bloom-style prefilter: 64-bit sets of the first letters and first letter pairs of a field's words. A field
// can only match when every query token's bit is set, which rejects most of the index without touching the
// strings.

type fieldFilter struct {
	first, pair uint64
}

func firstBit(c byte) uint64 {
	return 1 << (c & 63)
}

func pairBit(c0, c1 byte) uint64 {
	return 1 << ((uint(c0)*31 + uint(c1)) & 63)
}

func newFieldFilter(norm string) fieldFilter {
	var ff fieldFilter
	for i := 0; i < len(norm); {
		var w string
		w, i = nextWord(norm, i)
		ff.first |= firstBit(w[0])
		if len(w) > 1 {
			ff.pair |= pairBit(w[0], w[1])
		}
	}
	return ff
}

// tokenFilter holds the bits every matching field must have.
type tokenFilter struct {
	first, pair uint64
}

func newTokenFilter(toks []string) tokenFilter {
	var tf tokenFilter
	for _, t := range toks {
		if len(t) > 1 {
			tf.pair |= pairBit(t[0], t[1])
		} else {
			tf.first |= firstBit(t[0])
		}
	}
	return tf
}

func (tf tokenFilter) matches(ff fieldFilter) bool {
	return ff.first&tf.first == tf.first && ff.pair&tf.pair == tf.pair
}
