package autocomplete

import (
	"errors"
	"math"
	"testing"
)

func TestOptionBounds(t *testing.T) {
	bad := map[string]Option{
		"limit 0":             WithLimit(0),
		"max query runes 0":   WithMaxQueryRunes(0),
		"min query len 0":     WithMinQueryLen(0),
		"max tokens 0":        WithMaxTokens(0),
		"max tokens 65":       WithMaxTokens(65),
		"max items -1":        WithMaxItems(-1),
		"max items too big":   WithMaxItems(math.MaxInt32 + 1),
		"capacity -1":         WithCapacity(-1),
		"max field bytes 0":   WithMaxFieldBytes(0),
		"max field bytes big": WithMaxFieldBytes(MaxFieldBytesLimit + 1),
		"article symbols":     WithArticles("--"),
		"nil id":              WithID[movie](nil),
		"nil score":           WithScore[movie](nil),
	}
	for name, o := range bad {
		if _, err := New[movie](o); !errors.Is(err, ErrInvalidOption) {
			t.Errorf("%s: got %v, want ErrInvalidOption", name, err)
		}
	}

	good := []Option{
		WithLimit(1), WithMaxQueryRunes(1), WithMinQueryLen(1), WithMaxTokens(64), WithMaxItems(0),
		WithCapacity(0), WithMaxFieldBytes(MaxFieldBytesLimit), WithArticles(), WithArticles("Thé"),
	}
	if _, err := New[movie](good...); err != nil {
		t.Errorf("valid options: %v", err)
	}
}

func TestMaxItems(t *testing.T) {
	ix := testIndex(t, []movie{{ID: "1", Title: "One"}, {ID: "2", Title: "Two"}}, WithMaxItems(2))

	err := ix.Build([]movie{{ID: "1", Title: "One"}, {ID: "2", Title: "Two"}, {ID: "3", Title: "Three"}})
	if !errors.Is(err, ErrTooManyItems) {
		t.Fatalf("Build: got %v", err)
	}
	eq(t, ix.Len(), 2, "the old index keeps serving")
	eq(t, ids(ix.Search("one")), []string{"1"})

	if err := ix.Build([]movie{{ID: "1", Title: "One"}, {ID: "1", Title: "Uno"}, {ID: "2", Title: "Two"}}); err != nil {
		t.Errorf("duplicates do not count: %v", err)
	}

	if err := ix.Upsert(movie{ID: "3", Title: "Three"}); !errors.Is(err, ErrTooManyItems) {
		t.Errorf("Upsert past the bound: got %v", err)
	}
	if err := ix.Upsert(movie{ID: "1", Title: "Replaced"}, movie{ID: "3", Title: "Three"}); !errors.Is(err, ErrTooManyItems) {
		t.Errorf("batch Upsert past the bound: got %v", err)
	}
	eq(t, len(ix.Search("replaced")), 0, "a failed batch changes nothing")
	if err := ix.Upsert(movie{ID: "2", Title: "Replaced"}); err != nil {
		t.Errorf("replace at the bound: %v", err)
	}
	ix.Remove("1")
	if err := ix.Upsert(movie{ID: "3", Title: "Three"}, movie{ID: "3", Title: "Three"}); err != nil {
		t.Errorf("insert after remove: %v", err)
	}
	eq(t, ix.Len(), 2)
}
