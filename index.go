// Package autocomplete is an in-memory search-as-you-type index over the string fields of your own struct type.
//
// Fields tagged searchindex:"true" are matched word by word, by prefix, without diacritics and case. Results are
// ranked by match tier (exact, prefix, aligned, in order, any order), then by a per-field boost plus an optional
// per-item score. Searches are lock-free and run concurrently with Build, Upsert and Remove.
package autocomplete

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"
)

// Tier is how well a field matches a query; higher is better.
type Tier int

// Match tiers, worst first.
const (
	TierAnyOrder Tier = iota + 1 // every query word prefixes a distinct word of the field
	TierInOrder                  // query words prefix words of the field in increasing order
	TierAligned                  // query word i prefixes field word i
	TierPrefix                   // the field starts with the query
	TierExact                    // the field is the query
)

// Range is a half-open range of rune offsets in the original field text.
type Range struct {
	Start, End int
}

// FieldMatch holds the matched ranges of one tagged field, merged and sorted.
type FieldMatch struct {
	Field  string
	Ranges []Range
}

// Result is one match: the indexed item, its id, the tier and score of its best field, and the highlight ranges
// of every matching field.
type Result[T any] struct {
	Item    T
	ID      string
	Tier    Tier
	Score   float64
	Matches []FieldMatch
}

// Ranges returns the matched ranges of the named field, or nil when it did not match.
func (r *Result[T]) Ranges(field string) []Range {
	for _, m := range r.Matches {
		if m.Field == field {
			return m.Ranges
		}
	}
	return nil
}

// field is a normalized field value.
type field struct {
	norm string
	skip int // byte offset after a leading article
}

// Index is an in-memory search over items of type T. Search is lock-free: writers build a new snapshot and swap
// it in as a whole.
type Index[T any] struct {
	snap atomic.Pointer[snapshot[T]]
	mu   sync.Mutex // serializes writers

	fields   []fieldSpec
	idField  *fieldSpec
	idFunc   func(T) string
	score    func(T) float64
	ptr      bool
	articles []string // normalized, each followed by a space
	cfg      settings
}

// New returns an empty index for items of type T, a struct or a pointer to a struct. The struct tags are read
// once, here; a malformed tag, a tagged field that is not a string or *string, or a bad option is an error.
func New[T any](opts ...Option) (*Index[T], error) {
	cfg := defaultSettings()
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.err != nil {
		return nil, cfg.err
	}

	typ := reflect.TypeFor[T]()
	l, err := parseLayout(typ)
	if err != nil {
		return nil, err
	}
	ix := &Index[T]{fields: l.fields, idField: l.id, ptr: l.ptr, cfg: cfg}

	if cfg.id != nil {
		f, ok := cfg.id.(func(T) string)
		if !ok {
			return nil, fmt.Errorf("%w: WithID takes func(%v) string, got %T", ErrInvalidOption, typ, cfg.id)
		}
		ix.idFunc = f
	} else if l.id == nil {
		return nil, ErrNoID
	}
	if cfg.score != nil {
		f, ok := cfg.score.(func(T) float64)
		if !ok {
			return nil, fmt.Errorf("%w: WithScore takes func(%v) float64, got %T", ErrInvalidOption, typ, cfg.score)
		}
		ix.score = f
	}
	for _, a := range cfg.articles {
		ix.articles = append(ix.articles, Normalize(a)+" ")
	}

	ix.snap.Store(&snapshot[T]{})
	return ix, nil
}

// Fields returns the names of the tagged fields, in struct order.
func (ix *Index[T]) Fields() []string {
	names := make([]string, len(ix.fields))
	for i, f := range ix.fields {
		names[i] = f.name
	}
	return names
}

// base is the address of the struct behind an item, nil for a nil pointer item.
func (ix *Index[T]) base(item *T) unsafe.Pointer {
	if ix.ptr {
		return *(*unsafe.Pointer)(unsafe.Pointer(item))
	}
	return unsafe.Pointer(item)
}

func (ix *Index[T]) id(item *T) string {
	if ix.idFunc != nil {
		return ix.idFunc(*item)
	}
	return ix.idField.get(ix.base(item))
}

// newField normalizes text and finds its leading article.
func (ix *Index[T]) newField(text string) field {
	f := field{norm: Normalize(text)}
	for _, a := range ix.articles {
		if strings.HasPrefix(f.norm, a) && len(f.norm) > len(a) {
			f.skip = len(a)
			break
		}
	}
	return f
}

type snapshot[T any] struct {
	entries []entry[T]
	fields  []field       // len(fields) per entry
	filters []fieldFilter // parallel to fields
}

type entry[T any] struct {
	item T
	id   string
	base float64 // item score, the same for every field
}

// add indexes item at the end of s.
func (ix *Index[T]) add(s *snapshot[T], item T, id string) {
	e := entry[T]{item: item, id: id}
	if ix.score != nil {
		e.base = ix.score(item)
		if math.IsNaN(e.base) {
			e.base = 0
		}
	}
	s.entries = append(s.entries, e)
	base := ix.base(&item)
	for k := range ix.fields {
		f := ix.newField(ix.fields[k].get(base))
		s.fields = append(s.fields, f)
		s.filters = append(s.filters, newFieldFilter(f.norm))
	}
}

// Len returns the number of indexed items.
func (ix *Index[T]) Len() int {
	return len(ix.snap.Load().entries)
}
