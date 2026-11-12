package autocomplete

import (
	"cmp"
	"slices"
	"strings"
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
	if len(s.entries) == 0 {
		return nil
	}
	nq := Normalize(ix.trimQuery(q))
	if len(nq) < ix.cfg.minQueryLen {
		return nil
	}
	qr := newQuery(nq, ix.cfg.maxTokens)
	nf := len(ix.fields)

	top := make([]scored, 0, ix.cfg.limit)
	for i := range s.entries {
		e := &s.entries[i]
		tier, sc := ix.scoreEntry(e.base, s.fields[i*nf:(i+1)*nf], s.filters[i*nf:(i+1)*nf], qr)
		if tier == 0 {
			continue
		}
		top = insertTop(top, scored{pos: i, tier: tier, score: sc, base: e.base, id: e.id})
	}

	results := make([]Result[T], len(top))
	for i, c := range top {
		results[i] = ix.result(&s.entries[c.pos], s.fields[c.pos*nf:(c.pos+1)*nf], qr, c)
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

// scoreEntry returns the tier and score of the best matching field (tier 0: no match). The tier dominates, then
// the field boost plus the item score, minus a penalty for extra words.
func (ix *Index[T]) scoreEntry(base float64, fields []field, filters []fieldFilter, q *query) (Tier, float64) {
	var bestTier Tier
	var best float64
	for k := range fields {
		if !q.filter.matches(filters[k]) {
			continue
		}
		tier, _ := matchField(fields[k], q, false)
		if tier == 0 {
			continue
		}
		penalty := float64(min(80, 8*max(0, wordCount(fields[k].norm)-len(q.toks))))
		sc := ix.fields[k].boost + base - penalty
		if tier > bestTier || tier == bestTier && sc > best {
			bestTier, best = tier, sc
		}
	}
	return bestTier, best
}

// result builds the result row, with the highlight ranges of every matching field.
func (ix *Index[T]) result(e *entry[T], fields []field, q *query, c scored) Result[T] {
	r := Result[T]{Item: e.item, ID: e.id, Tier: c.tier, Score: c.score}
	base := ix.base(&e.item)
	for k := range ix.fields {
		if ranges := ix.highlight(fields[k], ix.fields[k].get(base), q); ranges != nil {
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
	tier, hits := matchField(f, q, true)
	if tier == 0 {
		return nil
	}
	var src []int
	norm := appendNormalized(make([]byte, 0, len(f.norm)), text, 0, &src)
	if string(norm) != f.norm {
		// the item was changed after it was indexed
		return nil
	}

	ranges := make([]Range, 0, len(hits))
	for _, h := range hits {
		ranges = append(ranges, Range{src[h.off], src[h.off+h.n-1] + 1})
	}
	slices.SortFunc(ranges, func(a, b Range) int { return cmp.Compare(a.Start, b.Start) })

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
