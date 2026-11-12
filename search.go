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

// Search returns up to WithLimit items matching q, best first. Ties are broken by item score, then by id, so the
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
	sr := searcher[T]{ix: ix, s: s, q: newQuery(nq, ix.cfg.maxTokens), top: make([]scored, 0, ix.cfg.limit)}
	for _, pos := range s.moved {
		if int(pos) < s.n {
			sr.try(int(pos))
		}
	}
	for pos := s.words.n; pos < s.n; pos++ {
		sr.try(pos)
	}
	sr.searchWords()

	results := make([]Result[T], len(sr.top))
	for i, sc := range sr.top {
		results[i] = ix.result(s.chunks[sc.pos/chunkSize], sc.pos%chunkSize, sr.q, sc)
	}
	return results
}

type searcher[T any] struct {
	ix  *Index[T]
	s   *snapshot[T]
	q   *query
	top []scored
}

func (sr *searcher[T]) try(pos int) {
	c, j := sr.s.chunks[pos/chunkSize], pos%chunkSize
	e := &c.entries[j]
	minTier := TierAnyOrder
	if len(sr.top) == cap(sr.top) {
		// only a better tier can beat the last result when even the best field score cannot
		worst := &sr.top[len(sr.top)-1]
		if minTier = worst.tier; e.base+sr.ix.maxBoost < worst.score {
			minTier++
		}
		if minTier > TierExact {
			return
		}
	}
	tier, sc := sr.ix.scoreEntry(c, j, sr.q, minTier)
	if tier == 0 {
		return
	}
	sr.top = insertTop(sr.top, scored{pos: pos, tier: tier, score: sc, base: e.base, id: e.id})
}

// searchWords tries the clean positions of the word index. Every query token has to prefix a word of a
// matching entry, and a field matches at TierAligned or better only when the first token prefixes its first
// word, or the first after its article. So it tries the fields equal to the query, then the entries led by the
// first token, then the entries holding the rarest token, each in descending item score, and stops a walk once
// the entries left can only beat the results by a tier that was fully tried.
func (sr *searcher[T]) searchWords() {
	wi := sr.s.words
	if wi.n == 0 {
		return
	}
	toks := sr.q.toks
	ranges := make([][2]int, len(toks))
	rarest, second := 0, -1
	for i, t := range toks {
		lo, hi := wi.prefixRange(t)
		if lo == hi {
			return
		}
		ranges[i] = [2]int{lo, hi}
		switch c := wi.any.count(lo, hi); {
		case i == 0:
		case c < wi.any.count(ranges[rarest][0], ranges[rarest][1]):
			rarest, second = i, rarest
		case second < 0 || c < wi.any.count(ranges[second][0], ranges[second][1]):
			second = i
		}
	}
	for _, h := range wi.wholeRanks(sr.q.nq) {
		if pos := int(wi.order[uint32(h)]); pos < sr.s.n && !sr.s.isDirty(pos) {
			sr.try(pos)
		}
	}

	// candidates also have to hold a second token, checked in a bitset before the fields
	var sets [2]*[]uint64
	defer func() {
		for _, set := range sets {
			if set != nil {
				bitsetPool.Put(set)
			}
		}
	}()
	filter := func(tok int) []uint64 {
		if tok < 0 {
			return nil
		}
		k := 0
		if tok == second {
			k = 1
		}
		if sets[k] == nil {
			sets[k] = wi.union(&wi.any, ranges[tok][0], ranges[tok][1])
		}
		return *sets[k]
	}
	leadFilter := rarest
	if rarest == 0 {
		leadFilter = second
	}
	if sr.walk(&wi.lead, ranges[0][0], ranges[0][1], TierExact, filter, leadFilter) {
		sr.walk(&wi.any, ranges[rarest][0], ranges[rarest][1], TierAligned, filter, second)
	}
}

// walk tries the clean entries of words [lo, hi) of p in rank order that are also in the filter set of token
// tok, every entry matching at covered or better having been tried already. It reports whether it went through
// them all.
func (sr *searcher[T]) walk(p *postings, lo, hi int, covered Tier, filter func(int) []uint64, tok int) bool {
	if sr.full() && sr.worst().tier >= covered {
		return false
	}
	wi := sr.s.words
	cur := wi.open(p, lo, hi)
	defer cur.close()
	var set []uint64
	for r, ok := cur.next(); ok; r, ok = cur.next() {
		if set != nil && set[r/64]&(1<<(r%64)) == 0 {
			continue
		}
		pos := int(wi.order[r])
		if pos >= sr.s.n || sr.s.isDirty(pos) {
			continue
		}
		if sr.full() {
			worst := sr.worst()
			base := sr.s.chunks[pos/chunkSize].entries[pos%chunkSize].base
			if base+sr.ix.maxBoost < worst.score && worst.tier+1 >= covered {
				return false
			}
		}
		if set == nil && tok >= 0 {
			// built once the walk gets past the cheap early stops
			if set = filter(tok); set[r/64]&(1<<(r%64)) == 0 {
				continue
			}
		}
		sr.try(pos)
	}
	return true
}

func (sr *searcher[T]) full() bool {
	return len(sr.top) == cap(sr.top)
}

func (sr *searcher[T]) worst() *scored {
	return &sr.top[len(sr.top)-1]
}

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

func insertTop(top []scored, c scored) []scored {
	n := len(top)
	if n == cap(top) && !c.betterThan(&top[n-1]) {
		return top
	}
	for i := range top {
		if top[i].pos == c.pos {
			return top
		}
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
