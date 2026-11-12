package autocomplete

import (
	"cmp"
	"hash/maphash"
	"math/bits"
	"slices"
	"sort"
	"strings"
	"sync"
)

// wordIndex is an inverted index over the first n positions of a snapshot, immutable once built. It numbers the
// entries by rank, in descending item score, so a walk over a posting list meets the best scores first.
type wordIndex struct {
	n       int
	order   []uint32 // position of each rank
	words   string   // the distinct words, sorted, back to back
	wordOff []uint32 // word w is words[wordOff[w]:wordOff[w+1]]
	any     postings // ranks of the entries with word w in a field
	lead    postings // ranks of the entries with word w first in a field, or first after its article
	whole   []uint64 // hash of a field from its start or after its article <<32 | rank, sorted
	seed    maphash.Seed
}

// postings holds a list of ranks per word, in ascending order, back to back.
type postings struct {
	ranks []uint32
	off   []uint32 // the ranks of word w are ranks[off[w]:off[w+1]]
}

func (p *postings) count(lo, hi int) int {
	return int(p.off[hi] - p.off[lo])
}

func (wi *wordIndex) word(w int) string {
	return wi.words[wi.wordOff[w]:wi.wordOff[w+1]]
}

// prefixRange returns the range of words that start with t.
func (wi *wordIndex) prefixRange(t string) (int, int) {
	nw := len(wi.wordOff) - 1
	lo := sort.Search(nw, func(i int) bool { return wi.word(i) >= t })
	hi := lo + sort.Search(nw-lo, func(i int) bool { return !strings.HasPrefix(wi.word(lo+i), t) })
	return lo, hi
}

func (wi *wordIndex) hash(s string) uint64 {
	return maphash.String(wi.seed, s) >> 32
}

// wholeRanks returns the ranks of the entries with a field equal to s, from its start or after its article, plus
// hash collisions.
func (wi *wordIndex) wholeRanks(s string) []uint64 {
	h := wi.hash(s)
	i, _ := slices.BinarySearch(wi.whole, h<<32)
	j := i
	for j < len(wi.whole) && wi.whole[j]>>32 == h {
		j++
	}
	return wi.whole[i:j]
}

func (ix *Index[T]) newWordIndex(s *snapshot[T]) *wordIndex {
	n := s.n
	nf := len(ix.fields)
	wi := &wordIndex{n: n, order: make([]uint32, n), seed: ix.seed}
	for i := range wi.order {
		wi.order[i] = uint32(i)
	}
	if ix.score != nil {
		base := make([]float64, n)
		for i := range base {
			base[i] = s.chunks[i/chunkSize].entries[i%chunkSize].base
		}
		slices.SortFunc(wi.order, func(a, b uint32) int { return cmp.Compare(base[b], base[a]) })
	}

	// word<<32 | rank, in rank order, each word once per entry
	vocab := map[string]uint32{}
	var anyOcc, leadOcc, ws, ls []uint64
	for r, pos := range wi.order {
		c, j := s.chunks[pos/chunkSize], int(pos%chunkSize)
		ws, ls = ws[:0], ls[:0]
		for k := range nf {
			f := c.field(j*nf + k)
			if f.norm == "" {
				continue
			}
			wi.whole = append(wi.whole, wi.hash(f.norm)<<32|uint64(r))
			if f.skip > 0 {
				wi.whole = append(wi.whole, wi.hash(f.norm[f.skip:])<<32|uint64(r))
			}
			for i := 0; i < len(f.norm); {
				start := i
				var w string
				w, i = nextWord(f.norm, i)
				id, ok := vocab[w]
				if !ok {
					id = uint32(len(vocab))
					vocab[w] = id
				}
				ws = append(ws, uint64(id)<<32|uint64(r))
				if start == 0 || start == f.skip {
					ls = append(ls, uint64(id)<<32|uint64(r))
				}
			}
		}
		slices.Sort(ws)
		slices.Sort(ls)
		anyOcc = append(anyOcc, slices.Compact(ws)...)
		leadOcc = append(leadOcc, slices.Compact(ls)...)
	}
	slices.Sort(wi.whole)
	wi.whole = slices.Clip(slices.Compact(wi.whole))

	sorted := make([]string, len(vocab))
	for w, id := range vocab {
		sorted[id] = w
	}
	byWord := make([]uint32, len(sorted)) // word id -> place in sorted order
	for i := range byWord {
		byWord[i] = uint32(i)
	}
	slices.SortFunc(byWord, func(a, b uint32) int { return strings.Compare(sorted[a], sorted[b]) })
	size := 0
	for _, w := range sorted {
		size += len(w)
	}
	var b strings.Builder
	b.Grow(size)
	wi.wordOff = make([]uint32, len(sorted)+1)
	place := make([]uint32, len(sorted))
	for i, id := range byWord {
		place[id] = uint32(i)
		b.WriteString(sorted[id])
		wi.wordOff[i+1] = uint32(b.Len())
	}
	wi.words = b.String()
	wi.any = newPostings(anyOcc, place)
	wi.lead = newPostings(leadOcc, place)
	return wi
}

// newPostings groups occurrences, word id<<32 | rank in rank order, by the place of their word.
func newPostings(occ []uint64, place []uint32) postings {
	p := postings{ranks: make([]uint32, len(occ)), off: make([]uint32, len(place)+1)}
	for _, o := range occ {
		p.off[place[o>>32]+1]++
	}
	for i := 1; i < len(p.off); i++ {
		p.off[i] += p.off[i-1]
	}
	next := slices.Clone(p.off[:len(place)])
	for _, o := range occ {
		w := place[o>>32]
		p.ranks[next[w]] = uint32(o)
		next[w]++
	}
	return p
}

var bitsetPool = sync.Pool{New: func() any { return new([]uint64) }}

// cursor walks a set of ranks in ascending order: a posting list, or a bitset when it is the union of several.
type cursor struct {
	ranks []uint32
	bits  *[]uint64
	i     int
	w     uint64
}

// open returns a cursor over the ranks of words [lo, hi) of p.
func (wi *wordIndex) open(p *postings, lo, hi int) cursor {
	if hi-lo == 1 {
		return cursor{ranks: p.ranks[p.off[lo]:p.off[hi]]}
	}
	return cursor{bits: wi.union(p, lo, hi), i: -1}
}

// union returns a pooled bitset of the ranks of words [lo, hi) of p.
func (wi *wordIndex) union(p *postings, lo, hi int) *[]uint64 {
	bp := bitsetPool.Get().(*[]uint64)
	nw := (wi.n + 63) / 64
	if cap(*bp) < nw {
		*bp = make([]uint64, nw)
	}
	set := (*bp)[:nw]
	clear(set)
	for _, r := range p.ranks[p.off[lo]:p.off[hi]] {
		set[r/64] |= 1 << (r % 64)
	}
	*bp = set
	return bp
}

func (c *cursor) next() (uint32, bool) {
	if c.bits == nil {
		if c.i == len(c.ranks) {
			return 0, false
		}
		c.i++
		return c.ranks[c.i-1], true
	}
	set := *c.bits
	for c.w == 0 {
		c.i++
		if c.i >= len(set) {
			return 0, false
		}
		c.w = set[c.i]
	}
	t := bits.TrailingZeros64(c.w)
	c.w &= c.w - 1
	return uint32(c.i*64 + t), true
}

func (c *cursor) close() {
	if c.bits != nil {
		bitsetPool.Put(c.bits)
		c.bits = nil
	}
}
