package autocomplete

import (
	"errors"
	"fmt"
	"math"
)

// Option defaults and bounds.
const (
	DefaultLimit         = 8
	DefaultMaxQueryRunes = 60
	DefaultMinQueryLen   = 2
	DefaultMaxTokens     = 6
	DefaultMaxFieldBytes = 255
	MaxFieldBytesLimit   = 65535
)

// DefaultArticles are the leading articles skipped when matching a field from its start.
var DefaultArticles = []string{"the", "a", "an"}

// ErrInvalidOption is wrapped by the errors New returns for an out of range or mistyped option.
var ErrInvalidOption = errors.New("autocomplete: invalid option")

// Option configures an Index. Options are validated by New.
type Option func(*settings)

type settings struct {
	limit         int
	maxQueryRunes int
	minQueryLen   int
	maxTokens     int
	maxItems      int
	capacity      int
	maxFieldBytes int
	articles      []string
	id            any // func(T) string
	score         any // func(T) float64
	err           error
}

func defaultSettings() settings {
	return settings{
		limit:         DefaultLimit,
		maxQueryRunes: DefaultMaxQueryRunes,
		minQueryLen:   DefaultMinQueryLen,
		maxTokens:     DefaultMaxTokens,
		maxFieldBytes: DefaultMaxFieldBytes,
		articles:      DefaultArticles,
	}
}

func (s *settings) fail(format string, args ...any) {
	if s.err == nil {
		s.err = fmt.Errorf("%w: "+format, append([]any{ErrInvalidOption}, args...)...)
	}
}

// WithLimit sets the maximum number of results of a search (default 8).
func WithLimit(n int) Option {
	return func(s *settings) {
		if n < 1 {
			s.fail("limit %d, want >= 1", n)
			return
		}
		s.limit = n
	}
}

// WithMaxQueryRunes sets how many runes of a query are used; the rest is ignored (default 60).
func WithMaxQueryRunes(n int) Option {
	return func(s *settings) {
		if n < 1 {
			s.fail("max query runes %d, want >= 1", n)
			return
		}
		s.maxQueryRunes = n
	}
}

// WithMinQueryLen sets the minimum length in bytes of a normalized query; shorter queries match nothing
// (default 2).
func WithMinQueryLen(n int) Option {
	return func(s *settings) {
		if n < 1 {
			s.fail("min query length %d, want >= 1", n)
			return
		}
		s.minQueryLen = n
	}
}

// WithMaxTokens sets how many words of a query are matched against words of a field (default 6, at most 64).
func WithMaxTokens(n int) Option {
	return func(s *settings) {
		if n < 1 || n > 64 {
			s.fail("max tokens %d, want 1..64", n)
			return
		}
		s.maxTokens = n
	}
}

// WithMaxItems bounds the number of indexed items; 0, the default, means no bound. Build and Upsert return
// ErrTooManyItems instead of growing past it, and the current index keeps serving.
func WithMaxItems(n int) Option {
	return func(s *settings) {
		if n < 0 || n > math.MaxInt32 {
			s.fail("max items %d, want 0..%d", n, math.MaxInt32)
			return
		}
		s.maxItems = n
	}
}

// WithCapacity presizes the index for about n items, so Build does not grow its id map while loading.
func WithCapacity(n int) Option {
	return func(s *settings) {
		if n < 0 || n > math.MaxInt32 {
			s.fail("capacity %d, want 0..%d", n, math.MaxInt32)
			return
		}
		s.capacity = n
	}
}

// WithMaxFieldBytes bounds the normalized length of each indexed field (default 255, at most 65535); the rest of
// a longer field is not searchable. It bounds the memory of an item no matter what its fields hold.
func WithMaxFieldBytes(n int) Option {
	return func(s *settings) {
		if n < 1 || n > MaxFieldBytesLimit {
			s.fail("max field bytes %d, want 1..%d", n, MaxFieldBytesLimit)
			return
		}
		s.maxFieldBytes = n
	}
}

// WithArticles replaces the leading articles skipped when matching a field from its start (default "the", "a",
// "an"). With no arguments no article is skipped.
func WithArticles(articles ...string) Option {
	return func(s *settings) {
		for _, a := range articles {
			if n := Normalize(a); n == "" || len(n) > 254 {
				s.fail("article %q", a)
				return
			}
		}
		s.articles = articles
	}
}

// WithID sets the function that returns the unique id of an item. It overrides a searchindex:"id" field.
func WithID[T any](f func(T) string) Option {
	return func(s *settings) {
		if f == nil {
			s.fail("nil id func")
			return
		}
		s.id = f
	}
}

// WithScore sets the popularity or recency bonus of an item, added to the score of its matching fields. It is
// computed once per item when the item is indexed. Matches are ranked by tier first, so the bonus only orders
// items that match equally well.
func WithScore[T any](f func(T) float64) Option {
	return func(s *settings) {
		if f == nil {
			s.fail("nil score func")
			return
		}
		s.score = f
	}
}
