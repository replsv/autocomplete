package autocomplete

import (
	"slices"
	"unsafe"
)

// chunkSize is the number of entries per chunk. An update copies the chunks it touches plus the chunk list, so
// it bounds the cost of a small update; every chunk but the last is full.
const chunkSize = 256

type snapshot[T any] struct {
	chunks []*chunk[T]
	n      int
}

// chunk is an immutable block of entries, shared by every snapshot that did not change it.
type chunk[T any] struct {
	filters []fieldFilter // one per field of each entry, scanned first because it is small and contiguous
	spans   []span        // parallel to filters
	text    string        // the normalized fields, back to back
	entries []entry[T]
}

type entry[T any] struct {
	item T
	id   string
	base float64 // item score, the same for every field
}

// span locates a normalized field in chunk.text.
type span struct {
	off  uint32
	n    uint16
	skip uint8 // byte offset after a leading article
}

func (c *chunk[T]) field(i int) field {
	sp := c.spans[i]
	return field{norm: c.text[sp.off : sp.off+uint32(sp.n)], skip: int(sp.skip)}
}

// draft is a chunk being written. Replaced fields leave dead bytes in text until freeze compacts it.
type draft[T any] struct {
	spans   []dspan
	text    []byte
	entries []entry[T]
}

type dspan struct {
	off, n int
}

func thaw[T any](c *chunk[T]) *draft[T] {
	d := &draft[T]{
		spans:   make([]dspan, len(c.spans)),
		text:    []byte(c.text),
		entries: slices.Clone(c.entries),
	}
	for i, sp := range c.spans {
		d.spans[i] = dspan{int(sp.off), int(sp.n)}
	}
	return d
}

func (ix *Index[T]) freeze(d *draft[T]) *chunk[T] {
	total := 0
	for _, sp := range d.spans {
		total += sp.n
	}
	buf := make([]byte, 0, total)
	c := &chunk[T]{
		filters: make([]fieldFilter, len(d.spans)),
		spans:   make([]span, len(d.spans)),
		entries: d.entries,
	}
	for i, sp := range d.spans {
		c.spans[i] = span{off: uint32(len(buf)), n: uint16(sp.n)}
		buf = append(buf, d.text[sp.off:sp.off+sp.n]...)
	}
	// buf is never written again, so the string can share it
	c.text = unsafe.String(unsafe.SliceData(buf), len(buf))
	for i := range c.spans {
		f := c.field(i)
		c.spans[i].skip = uint8(ix.articleSkip(f.norm))
		c.filters[i] = newFieldFilter(f.norm)
	}
	return c
}

// set indexes item at slot, which is either taken or len(d.entries).
func (ix *Index[T]) set(d *draft[T], slot int, e entry[T]) {
	nf := len(ix.fields)
	if slot == len(d.entries) {
		d.entries = append(d.entries, entry[T]{})
		d.spans = append(d.spans, make([]dspan, nf)...)
	}
	d.entries[slot] = e
	v := ix.structOf(&d.entries[slot].item)
	for k := range ix.fields {
		off := len(d.text)
		d.text = appendNormalized(d.text, ix.fields[k].get(v), ix.cfg.maxFieldBytes, nil)
		d.spans[slot*nf+k] = dspan{off, len(d.text) - off}
	}
}

// move copies entry slot of src to slot dst of d.
func (ix *Index[T]) move(d *draft[T], dst int, src *draft[T], slot int) {
	nf := len(ix.fields)
	d.entries[dst] = src.entries[slot]
	for k := range nf {
		sp := src.spans[slot*nf+k]
		off := len(d.text)
		d.text = append(d.text, src.text[sp.off:sp.off+sp.n]...)
		d.spans[dst*nf+k] = dspan{off, sp.n}
	}
}

func (ix *Index[T]) truncate(d *draft[T], n int) {
	clear(d.entries[n:])
	d.entries = d.entries[:n]
	d.spans = d.spans[:n*len(ix.fields)]
}

// editor applies writes to a copy of a snapshot, copying each touched chunk once.
type editor[T any] struct {
	ix     *Index[T]
	chunks []*chunk[T]
	drafts map[int]*draft[T]
	byID   map[string]int
	n      int
	spare  *draft[T] // a sealed draft whose spans and text the next new chunk reuses
}

func (ix *Index[T]) edit(s *snapshot[T], byID map[string]int) *editor[T] {
	return &editor[T]{ix: ix, chunks: slices.Clone(s.chunks), drafts: map[int]*draft[T]{}, byID: byID, n: s.n}
}

func (e *editor[T]) draft(ci int) *draft[T] {
	if d, ok := e.drafts[ci]; ok {
		return d
	}
	var d *draft[T]
	if ci == len(e.chunks) {
		d = &draft[T]{entries: make([]entry[T], 0, chunkSize)}
		if e.spare != nil {
			d.spans, d.text = e.spare.spans[:0], e.spare.text[:0]
			e.spare = nil
		}
		e.chunks = append(e.chunks, nil)
	} else {
		d = thaw(e.chunks[ci])
	}
	e.drafts[ci] = d
	return d
}

// put inserts or replaces the entry with id e.id and returns its position.
func (e *editor[T]) put(ent entry[T]) int {
	pos, ok := e.byID[ent.id]
	if !ok {
		pos = e.n
		e.byID[ent.id] = pos
		e.n++
	}
	e.ix.set(e.draft(pos/chunkSize), pos%chunkSize, ent)
	return pos
}

// remove swaps the last entry into the place of id, so every chunk but the last stays full.
func (e *editor[T]) remove(id string) bool {
	pos, ok := e.byID[id]
	if !ok {
		return false
	}
	delete(e.byID, id)
	last := e.n - 1
	ld := e.draft(last / chunkSize)
	if pos != last {
		d := e.draft(pos / chunkSize)
		e.ix.move(d, pos%chunkSize, ld, last%chunkSize)
		e.byID[d.entries[pos%chunkSize].id] = pos
	}
	e.ix.truncate(ld, last%chunkSize)
	if last%chunkSize == 0 {
		delete(e.drafts, last/chunkSize)
		e.chunks = e.chunks[:len(e.chunks)-1]
	}
	e.n--
	return true
}

// seal freezes the draft of chunk ci, if any.
func (e *editor[T]) seal(ci int) {
	if d, ok := e.drafts[ci]; ok {
		e.chunks[ci] = e.ix.freeze(d)
		delete(e.drafts, ci)
		e.spare = d
	}
}

func (e *editor[T]) snapshot() *snapshot[T] {
	for ci := range e.drafts {
		e.seal(ci)
	}
	return &snapshot[T]{chunks: e.chunks, n: e.n}
}
