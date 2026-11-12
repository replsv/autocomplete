package autocomplete

import (
	"fmt"
	"hash/maphash"
	"math"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
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
	Start int `json:"start"`
	End   int `json:"end"`
}

// FieldMatch holds the matched ranges of one tagged field, merged and sorted.
type FieldMatch struct {
	Field  string  `json:"field"`
	Ranges []Range `json:"ranges"`
}

// Result is one match: the indexed item, its id, the tier and score of its best field, and the highlight ranges
// of every matching field.
type Result[T any] struct {
	Item    T            `json:"item"`
	ID      string       `json:"id"`
	Tier    Tier         `json:"tier"`
	Score   float64      `json:"score"`
	Matches []FieldMatch `json:"matches,omitempty"`
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

type field struct {
	norm string
	skip int // byte offset after a leading article
}

// Index is an in-memory search over items of type T. Search is lock-free: writers build a new snapshot and swap
// it in as a whole.
type Index[T any] struct {
	snap atomic.Pointer[snapshot[T]]
	mu   sync.Mutex     // serializes writers
	byID map[string]int // position of every id in the current snapshot, owned by the writers

	fields   []fieldSpec
	idField  *fieldSpec
	idFunc   func(T) string
	score    func(T) float64
	ptr      bool
	articles []string // normalized, each followed by a space
	maxBoost float64
	seed     maphash.Seed
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
	ix := &Index[T]{fields: l.fields, idField: l.id, ptr: l.ptr, cfg: cfg, maxBoost: l.fields[0].boost}
	for _, f := range l.fields {
		ix.maxBoost = max(ix.maxBoost, f.boost)
	}

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
		ix.articles = append(ix.articles, normalize(a)+" ")
	}

	ix.byID = make(map[string]int, cfg.capacity)
	ix.seed = maphash.MakeSeed()
	ix.snap.Store(&snapshot[T]{words: &wordIndex{}})
	return ix, nil
}

// structOf returns the struct behind item, or the zero Value for a nil pointer item.
func (ix *Index[T]) structOf(item *T) reflect.Value {
	v := reflect.ValueOf(item).Elem()
	if ix.ptr {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}
	return v
}

func (ix *Index[T]) id(item *T) string {
	if ix.idFunc != nil {
		return ix.idFunc(*item)
	}
	return ix.idField.get(ix.structOf(item))
}

// articleSkip is the byte offset after the leading article of norm, 0 when there is none.
func (ix *Index[T]) articleSkip(norm string) int {
	for _, a := range ix.articles {
		if strings.HasPrefix(norm, a) && len(norm) > len(a) {
			return len(a)
		}
	}
	return 0
}

func (ix *Index[T]) newEntry(item T, id string) entry[T] {
	e := entry[T]{item: item, id: id}
	if ix.score != nil {
		e.base = ix.score(item)
		if math.IsNaN(e.base) {
			e.base = 0
		}
	}
	return e
}

// Len returns the number of indexed items.
func (ix *Index[T]) Len() int {
	return ix.snap.Load().n
}
