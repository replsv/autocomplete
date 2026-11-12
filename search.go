package autocomplete

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
)

type scored struct {
	pos   int
	tier  Tier
	score float64
	base  float64
	id    string
}

func (a *scored) betterThan(b *scored) bool {
	if a.tier != b.tier {
		return a.tier > b.tier
	}
	if a.score != b.score {
		return a.score > b.score
	}
	if a.base != b.base {
		return a.base > b.base
	}
	return a.id < b.id
}

// Search returns up to Limit items matching q, best first. Ties are broken by item score, then by id, so the
// order is deterministic.
func (ix *Index[T]) Search(q string) []Result[T] {
	s := ix.snap.Load()
	if s.n == 0 {
		return nil
	}
	nq := normalize(ix.trimQuery(q))
	if len(nq) < ix.cfg.minQueryLen {
		return nil
	}
	qr := newQuery(nq, ix.cfg.maxTokens)
	nf := len(ix.fields)

	tf := qr.filter
	top := make([]scored, 0, ix.cfg.limit)
	for ci, c := range s.chunks {
		fs := c.filters
		for k := 0; k < len(fs); k++ {
			if fs[k].first&tf.first != tf.first || fs[k].pair&tf.pair != tf.pair {
				continue
			}
			j := k / nf
			k = j*nf + nf - 1
			e := &c.entries[j]
			minTier := TierAnyOrder
			if len(top) == cap(top) {
				// only a better tier can beat the last result when even the best field score cannot
				worst := &top[len(top)-1]
				if minTier = worst.tier; e.base+ix.maxBoost < worst.score {
					minTier++
				}
				if minTier > TierExact {
					continue
				}
			}
			tier, sc := ix.scoreEntry(c, j, qr, minTier)
			if tier == 0 {
				continue
			}
			top = insertTop(top, scored{pos: ci*chunkSize + j, tier: tier, score: sc, base: e.base, id: e.id})
		}
	}

	results := make([]Result[T], len(top))
	for i, sc := range top {
		results[i] = ix.result(s.chunks[sc.pos/chunkSize], sc.pos%chunkSize, qr, sc)
	}
	return results
}

// trimQuery drops surrounding space and everything after MaxQueryRunes runes.
func (ix *Index[T]) trimQuery(q string) string {
	q = strings.TrimSpace(q)
	n := 0
	for i := range q {
		if n == ix.cfg.maxQueryRunes {
			return q[:i]
		}
		n++
	}
	return q
}

// insertTop inserts c into top, sorted best first, keeping at most cap(top) entries.
func insertTop(top []scored, c scored) []scored {
	n := len(top)
	if n == cap(top) && !c.betterThan(&top[n-1]) {
		return top
	}
	pos := n
	if n < cap(top) {
		top = append(top, c)
	} else {
		pos = n - 1
	}
	for pos > 0 && c.betterThan(&top[pos-1]) {
		top[pos] = top[pos-1]
		pos--
	}
	top[pos] = c
	return top
}

// scoreEntry returns the tier and score of the best field of entry j that matches at minTier or better (tier 0:
// none). The tier dominates, then the field boost plus the item score, minus a penalty for extra words.
func (ix *Index[T]) scoreEntry(c *chunk[T], j int, q *query, minTier Tier) (Tier, float64) {
	var bestTier Tier
	var best float64
	nf := len(ix.fields)
	for k := range ix.fields {
		if !q.filter.matches(c.filters[j*nf+k]) {
			continue
		}
		f := c.field(j*nf + k)
		tier, _ := matchField(f, q, max(minTier, bestTier), false)
		if tier == 0 {
			continue
		}
		penalty := float64(min(80, 8*max(0, wordCount(f.norm)-len(q.toks))))
		sc := ix.fields[k].boost + c.entries[j].base - penalty
		if tier > bestTier || tier == bestTier && sc > best {
			bestTier, best = tier, sc
		}
	}
	return bestTier, best
}

// result builds the result row of entry j, with the highlight ranges of every matching field.
func (ix *Index[T]) result(c *chunk[T], j int, q *query, sc scored) Result[T] {
	e := &c.entries[j]
	r := Result[T]{Item: e.item, ID: e.id, Tier: sc.tier, Score: sc.score}
	v := ix.structOf(&e.item)
	nf := len(ix.fields)
	for k := range ix.fields {
		if ranges := ix.highlight(c.field(j*nf+k), ix.fields[k].get(v), q); ranges != nil {
			r.Matches = append(r.Matches, FieldMatch{Field: ix.fields[k].name, Ranges: ranges})
		}
	}
	return r
}

// highlight maps the matched ranges of f back onto the original text, as merged rune ranges.
func (ix *Index[T]) highlight(f field, text string, q *query) []Range {
	if text == "" {
		return nil
	}
	tier, hits := matchField(f, q, TierAnyOrder, true)
	if tier == 0 {
		return nil
	}
	var src []int
	norm := appendNormalized(make([]byte, 0, len(f.norm)), text, ix.cfg.maxFieldBytes, &src)
	if string(norm) != f.norm {
		// the item was changed after it was indexed
		return nil
	}

	ranges := make([]Range, 0, len(hits))
	for _, h := range hits {
		ranges = append(ranges, Range{src[h.off], src[h.off+h.n-1] + 1})
	}
	slices.SortFunc(ranges, func(a, b Range) int { return cmp.Compare(a.Start, b.Start) })
	extendOverMarks(text, ranges)

	merged := ranges[:0]
	for _, r := range ranges {
		if last := len(merged) - 1; last >= 0 && r.Start <= merged[last].End {
			merged[last].End = max(merged[last].End, r.End)
			continue
		}
		merged = append(merged, r)
	}
	return merged
}

// extendOverMarks moves the end of each sorted range past the combining marks of its last letter, so a
// decomposed "é" is highlighted whole.
func extendOverMarks(text string, ranges []Range) {
	k, i := 0, 0
	for _, r := range text {
		for k < len(ranges) && ranges[k].End < i {
			k++
		}
		if k == len(ranges) {
			return
		}
		if ranges[k].End == i && unicode.Is(unicode.Mn, r) {
			ranges[k].End++
		}
		i++
	}
}
