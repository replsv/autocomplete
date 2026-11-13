package autocomplete

import (
	"hash/maphash"
	"math"
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
	wi := &wordIndex{n: n, seed: ix.seed}
	words, starts := 0, 0
	for _, c := range s.chunks {
		for i := range c.spans {
			if f := c.field(i); f.norm != "" {
				words += wordCount(f.norm)
				starts++
				if f.skip > 0 {
					starts++
				}
			}
		}
	}
	wi.order = rankOrder(s, ix.score != nil)

	// the words of each entry, once, in rank order; the entry of rank r ends at anyEnd[r]
	vocab := make(map[string]uint32, len(s.words.wordOff))
	anyOcc, leadOcc := make([]uint32, 0, words), make([]uint32, 0, starts)
	anyEnd, leadEnd := make([]uint32, n), make([]uint32, n)
	wi.whole = make([]uint64, 0, starts)
	var anySeen, leadSeen []uint32 // 1 + the last rank each word id was added for
	for r, pos := range wi.order {
		c, j := s.chunks[pos/chunkSize], int(pos%chunkSize)
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
					anySeen, leadSeen = append(anySeen, 0), append(leadSeen, 0)
				}
				if anySeen[id] != uint32(r)+1 {
					anySeen[id] = uint32(r) + 1
					anyOcc = append(anyOcc, id)
				}
				if (start == 0 || start == f.skip) && leadSeen[id] != uint32(r)+1 {
					leadSeen[id] = uint32(r) + 1
					leadOcc = append(leadOcc, id)
				}
			}
		}
		anyEnd[r], leadEnd[r] = uint32(len(anyOcc)), uint32(len(leadOcc))
	}
	// stable, so the ranks of a hash stay in order and duplicates next to each other
	radixSort(wi.whole, []uint64(nil), 4)
	wi.whole = slices.Clip(slices.Compact(wi.whole))

	sorted := make([]string, len(vocab))
	size := 0
	for w, id := range vocab {
		sorted[id] = w
		size += len(w)
	}
	byWord := make([]uint32, len(sorted))
	for i := range byWord {
		byWord[i] = uint32(i)
	}
	slices.SortFunc(byWord, func(a, b uint32) int { return strings.Compare(sorted[a], sorted[b]) })
	var b strings.Builder
	b.Grow(size)
	wi.wordOff = make([]uint32, len(sorted)+1)
	place := make([]uint32, len(sorted)) // word id -> place in sorted order
	for i, id := range byWord {
		place[id] = uint32(i)
		b.WriteString(sorted[id])
		wi.wordOff[i+1] = uint32(b.Len())
	}
	wi.words = b.String()
	wi.any = newPostings(anyOcc, anyEnd, place)
	wi.lead = newPostings(leadOcc, leadEnd, place)
	return wi
}

// rankOrder returns the positions of s in descending item score.
func rankOrder[T any](s *snapshot[T], scored bool) []uint32 {
	order := make([]uint32, s.n)
	for i := range order {
		order[i] = uint32(i)
	}
	if !scored {
		return order
	}
	keys := make([]uint64, s.n)
	for i := range keys {
		// flipped so that the unsigned order of the keys is the descending order of the scores
		b := math.Float64bits(s.chunks[i/chunkSize].entries[i%chunkSize].base)
		if b>>63 == 0 {
			b |= 1 << 63
		} else {
			b = ^b
		}
		keys[i] = ^b
	}
	radixSort(keys, order, 0)
	return order
}

// radixSort sorts keys by their bytes from byte from up, stably, and vals along with them unless vals is nil.
func radixSort[V any](keys []uint64, vals []V, from int) {
	tk := make([]uint64, len(keys))
	var tv []V
	if vals != nil {
		tv = make([]V, len(vals))
	}
	src, dst, srcV, dstV := keys, tk, vals, tv
	swapped := false
	for shift := uint(8 * from); shift < 64; shift += 8 {
		var count [256]int
		for _, k := range src {
			count[byte(k>>shift)]++
		}
		if len(src) < 2 || count[byte(src[0]>>shift)] == len(src) {
			continue
		}
		next := 0
		for i, c := range count {
			count[i] = next
			next += c
		}
		for i, k := range src {
			d := &count[byte(k>>shift)]
			dst[*d] = k
			if vals != nil {
				dstV[*d] = srcV[i]
			}
			*d++
		}
		src, dst, srcV, dstV = dst, src, dstV, srcV
		swapped = !swapped
	}
	if swapped {
		copy(keys, src)
		copy(vals, srcV)
	}
}

// newPostings groups the word ids occ, the ones of rank r ending at end[r], by the place of their word.
func newPostings(occ, end []uint32, place []uint32) postings {
	p := postings{ranks: make([]uint32, len(occ)), off: make([]uint32, len(place)+1)}
	for _, w := range occ {
		p.off[place[w]+1]++
	}
	for i := 1; i < len(p.off); i++ {
		p.off[i] += p.off[i-1]
	}
	next := slices.Clone(p.off[:len(place)])
	i := 0
	for r, e := range end {
		for ; i < int(e); i++ {
			w := place[occ[i]]
			p.ranks[next[w]] = uint32(r)
			next[w]++
		}
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
