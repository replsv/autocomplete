package autocomplete

import (
	"errors"
	"iter"
	"slices"
)

// ErrEmptyID is returned when an item has an empty id.
var ErrEmptyID = errors.New("autocomplete: empty item id")

// BuildFunc replaces the whole index with items. The method value of (*Index[T]).Build is one, so a rebuild can
// be handed to a scheduler or a handler as a plain func.
type BuildFunc[T any] func(items []T) error

// UpdateFunc inserts items, or replaces the indexed items with the same id. The method value of
// (*Index[T]).Upsert is one.
type UpdateFunc[T any] func(items ...T) error

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

	s := &snapshot[T]{}
	byID := map[string]int{}
	for item := range items {
		id := ix.id(&item)
		if id == "" {
			return ErrEmptyID
		}
		pos, ok := byID[id]
		if !ok {
			pos = len(s.entries)
			byID[id] = pos
		}
		ix.put(s, pos, item, id)
	}
	ix.byID = byID
	ix.snap.Store(s)
	return nil
}

// Upsert inserts items, or replaces the indexed items with the same id. A call copies the snapshot once,
// however many items it carries, so batch updates when you can. On error nothing is changed.
func (ix *Index[T]) Upsert(items ...T) error {
	if len(items) == 0 {
		return nil
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()

	ids := make([]string, len(items))
	for i := range items {
		if ids[i] = ix.id(&items[i]); ids[i] == "" {
			return ErrEmptyID
		}
	}

	cur := ix.snap.Load()
	s := &snapshot[T]{
		entries: slices.Clone(cur.entries),
		fields:  slices.Clone(cur.fields),
		filters: slices.Clone(cur.filters),
	}
	for i, item := range items {
		pos, ok := ix.byID[ids[i]]
		if !ok {
			pos = len(s.entries)
			ix.byID[ids[i]] = pos
		}
		ix.put(s, pos, item, ids[i])
	}
	ix.snap.Store(s)
	return nil
}

// Remove removes the items with the given ids and returns how many were indexed.
func (ix *Index[T]) Remove(ids ...string) int {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	cur := ix.snap.Load()
	var s *snapshot[T]
	nf := len(ix.fields)
	removed := 0
	for _, id := range ids {
		pos, ok := ix.byID[id]
		if !ok {
			continue
		}
		if s == nil {
			s = &snapshot[T]{
				entries: slices.Clone(cur.entries),
				fields:  slices.Clone(cur.fields),
				filters: slices.Clone(cur.filters),
			}
		}
		last := len(s.entries) - 1
		if pos != last {
			s.entries[pos] = s.entries[last]
			copy(s.fields[pos*nf:(pos+1)*nf], s.fields[last*nf:])
			copy(s.filters[pos*nf:(pos+1)*nf], s.filters[last*nf:])
			ix.byID[s.entries[pos].id] = pos
		}
		s.entries = s.entries[:last]
		s.fields = s.fields[:last*nf]
		s.filters = s.filters[:last*nf]
		delete(ix.byID, id)
		removed++
	}
	if s != nil {
		ix.snap.Store(s)
	}
	return removed
}
