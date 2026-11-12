package autocomplete

import (
	"cmp"
	"slices"
	"strings"
)

// query is a normalized search query.
type query struct {
	nq     string
	toks   []string // space-separated words of nq, at most MaxTokens
	byLen  []string // toks, longest first
	filter tokenFilter
}

func newQuery(nq string, maxTokens int) *query {
	toks := strings.Split(nq, " ")
	if len(toks) > maxTokens {
		toks = toks[:maxTokens]
	}
	byLen := slices.Clone(toks)
	slices.SortStableFunc(byLen, func(a, b string) int { return cmp.Compare(len(b), len(a)) })
	return &query{nq: nq, toks: toks, byLen: byLen, filter: newTokenFilter(toks)}
}

// hit is a matched byte range of field.norm.
type hit struct {
	off, n int
}

// nextWord returns the word of norm starting at byte i and the start of the word after it.
func nextWord(norm string, i int) (string, int) {
	j := strings.IndexByte(norm[i:], ' ')
	if j < 0 {
		return norm[i:], len(norm) + 1
	}
	return norm[i : i+j], i + j + 1
}

func wordCount(norm string) int {
	return strings.Count(norm, " ") + 1
}

// matchField returns the best tier at which f matches q (0: no match), plus the matched ranges when collect is
// set.
func matchField(f field, q *query, collect bool) (Tier, []hit) {
	if f.norm == "" || !anyWordHasPrefix(f.norm, q.toks[0]) {
		return 0, nil
	}

	for _, from := range [2]int{0, f.skip} {
		rest := f.norm[from:]
		if rest == q.nq {
			return TierExact, wholeHit(collect, from, len(q.nq))
		}
		if strings.HasPrefix(rest, q.nq) {
			return TierPrefix, wholeHit(collect, from, len(q.nq))
		}
		if f.skip == 0 {
			break
		}
	}

	// token i prefixes word i, from the first word or from the word after a leading article
	var hits []hit
	for _, from := range [2]int{0, f.skip} {
		if hits, ok := aligned(f.norm, from, q.toks, collect); ok {
			return TierAligned, hits
		}
		if f.skip == 0 {
			break
		}
	}

	// tokens in increasing word order (greedy)
	i, ok := 0, true
	for _, t := range q.toks {
		var w string
		start := i
		for i < len(f.norm) {
			start = i
			w, i = nextWord(f.norm, i)
			if strings.HasPrefix(w, t) {
				break
			}
			w = ""
		}
		if w == "" {
			ok = false
			break
		}
		if collect {
			hits = append(hits, hit{start, len(t)})
		}
	}
	if ok {
		return TierInOrder, hits
	}

	// any order, each word used once; longest tokens first so the greedy assignment is the optimal one
	hits = hits[:0]
	var used uint64
	for _, t := range q.byLen {
		found := false
		for i, wi := 0, 0; i < len(f.norm) && wi < 64; wi++ {
			start := i
			var w string
			w, i = nextWord(f.norm, i)
			if used&(1<<wi) == 0 && strings.HasPrefix(w, t) {
				used |= 1 << wi
				if collect {
					hits = append(hits, hit{start, len(t)})
				}
				found = true
				break
			}
		}
		if !found {
			return 0, nil
		}
	}
	return TierAnyOrder, hits
}

// aligned reports whether token i prefixes the i-th word of norm counted from byte from.
func aligned(norm string, from int, toks []string, collect bool) ([]hit, bool) {
	var hits []hit
	i := from
	for _, t := range toks {
		if i >= len(norm) {
			return nil, false
		}
		start := i
		var w string
		w, i = nextWord(norm, i)
		if !strings.HasPrefix(w, t) {
			return nil, false
		}
		if collect {
			hits = append(hits, hit{start, len(t)})
		}
	}
	return hits, true
}

func wholeHit(collect bool, off, n int) []hit {
	if !collect {
		return nil
	}
	return []hit{{off, n}}
}

func anyWordHasPrefix(norm, prefix string) bool {
	for i := 0; i < len(norm); {
		var w string
		w, i = nextWord(norm, i)
		if strings.HasPrefix(w, prefix) {
			return true
		}
	}
	return false
}
