package autocomplete

import (
	"errors"
	"fmt"
	"iter"
	"slices"
)

var (
	// ErrEmptyID is returned when an item has an empty id.
	ErrEmptyID = errors.New("autocomplete: empty item id")
	// ErrTooManyItems is returned when a write would grow the index past WithMaxItems.
	ErrTooManyItems = errors.New("autocomplete: too many items")
)

// BuildFunc replaces the whole index with items. The method value of (*Index[T]).Build is one, so a rebuild can
// be handed to a scheduler or a handler as a plain func.
type BuildFunc[T any] func(items []T) error

// UpsertFunc inserts items, or replaces the indexed items with the same id. The method value of
// (*Index[T]).Upsert is one.
type UpsertFunc[T any] func(items ...T) error

// RemoveFunc removes items by id and returns how many were indexed. The method value of (*Index[T]).Remove is
// one.
type RemoveFunc func(ids ...string) int

// Build replaces the whole index with items. When several items have the same id, the last one wins. On error
// the current index keeps serving.
func (ix *Index[T]) Build(items []T) error {
	return ix.BuildSeq(slices.Values(items))
}

// BuildSeq is Build over a sequence, so items can be streamed from their source without holding them twice.
func (ix *Index[T]) BuildSeq(items iter.Seq[T]) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	e := &editor[T]{
		ix:     ix,
		chunks: make([]*chunk[T], 0, (ix.cfg.capacity+chunkSize-1)/chunkSize),
		drafts: map[int]*draft[T]{},
		byID:   make(map[string]int, ix.cfg.capacity),
	}
	i := 0
	for item := range items {
		id := ix.id(&item)
		if id == "" {
			return fmt.Errorf("%w: item %d", ErrEmptyID, i)
		}
		if _, ok := e.byID[id]; !ok && ix.full(e.n, 1) {
			return ix.errTooMany()
		}
		i++
		if pos := e.put(ix.newEntry(item, id)); pos%chunkSize == chunkSize-1 {
			e.seal(pos / chunkSize)
		}
	}
	ix.byID = e.byID
	ix.snap.Store(e.snapshot())
	return nil
}

func (ix *Index[T]) full(n, more int) bool {
	return ix.cfg.maxItems > 0 && n+more > ix.cfg.maxItems
}

func (ix *Index[T]) errTooMany() error {
	return fmt.Errorf("%w: limit %d", ErrTooManyItems, ix.cfg.maxItems)
}

// Upsert inserts items, or replaces the indexed items with the same id. A call copies the chunks of 256 entries
// it touches once, however many items it carries, so batch updates when you can. On error nothing is changed.
func (ix *Index[T]) Upsert(items ...T) error {
	if len(items) == 0 {
		return nil
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()

	// user funcs run before the id map is touched, so a panic in one leaves the index consistent
	ents := make([]entry[T], len(items))
	var added map[string]struct{}
	for i := range items {
		id := ix.id(&items[i])
		if id == "" {
			return fmt.Errorf("%w: item %d", ErrEmptyID, i)
		}
		ents[i] = ix.newEntry(items[i], id)
		if _, ok := ix.byID[id]; !ok && ix.cfg.maxItems > 0 {
			if added == nil {
				added = map[string]struct{}{}
			}
			added[id] = struct{}{}
		}
	}
	cur := ix.snap.Load()
	if ix.full(cur.n, len(added)) {
		return ix.errTooMany()
	}

	e := ix.edit(cur, ix.byID)
	for _, ent := range ents {
		e.put(ent)
	}
	ix.snap.Store(e.snapshot())
	return nil
}

// Remove removes the items with the given ids and returns how many were indexed.
func (ix *Index[T]) Remove(ids ...string) int {
	if len(ids) == 0 {
		return 0
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()

	e := ix.edit(ix.snap.Load(), ix.byID)
	removed := 0
	for _, id := range ids {
		if e.remove(id) {
			removed++
		}
	}
	if removed > 0 {
		ix.snap.Store(e.snapshot())
	}
	return removed
}
