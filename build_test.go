package autocomplete

import (
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"testing"
)

// checkIndex verifies the snapshot against the expected items: every chunk but the last is full, the id map
// points at the right entries, and the stored fields are the normalized item fields.
func checkIndex(t *testing.T, ix *Index[movie], want map[string]movie) {
	t.Helper()
	s := ix.snap.Load()
	if s.n != len(want) || len(ix.byID) != len(want) {
		t.Fatalf("n %d, byID %d, want %d", s.n, len(ix.byID), len(want))
	}
	nf := len(ix.fields)
	n := 0
	for ci, c := range s.chunks {
		if ci < len(s.chunks)-1 && len(c.entries) != chunkSize || len(c.entries) == 0 {
			t.Fatalf("chunk %d of %d has %d entries", ci, len(s.chunks), len(c.entries))
		}
		if len(c.spans) != len(c.entries)*nf || len(c.filters) != len(c.spans) {
			t.Fatalf("chunk %d: %d spans, %d filters for %d entries", ci, len(c.spans), len(c.filters), len(c.entries))
		}
		for j := range c.entries {
			e := &c.entries[j]
			if pos, ok := ix.byID[e.id]; !ok || pos != ci*chunkSize+j {
				t.Fatalf("entry %s at %d, byID says %d %v", e.id, ci*chunkSize+j, pos, ok)
			}
			if e.item != want[e.id] {
				t.Fatalf("entry %s: %+v, want %+v", e.id, e.item, want[e.id])
			}
			alt := ""
			if e.item.Alt != nil {
				alt = *e.item.Alt
			}
			for k, text := range []string{e.item.Title, alt} {
				norm := string(appendNormalized(nil, text, ix.cfg.maxFieldBytes, nil))
				if f := c.field(j*nf + k); f.norm != norm || c.filters[j*nf+k] != newFieldFilter(norm) {
					t.Fatalf("entry %s field %d: %q, want %q", e.id, k, f.norm, norm)
				}
			}
			n++
		}
	}
	if n != len(want) {
		t.Fatalf("%d entries, want %d", n, len(want))
	}
}

func randomMovie(r *rand.Rand, id int) movie {
	words := []string{"alpha", "beta", "gamma", "lupta", "după", "Șoimilor", "Straße", "the", "night"}
	m := movie{ID: fmt.Sprint(id), Year: 1990 + r.IntN(30), Votes: r.IntN(1000)}
	for range 1 + r.IntN(4) {
		m.Title += words[r.IntN(len(words))] + " "
	}
	if r.IntN(2) == 0 {
		m.Alt = ptr(words[r.IntN(len(words))])
	}
	return m
}

func TestWritesAgainstModel(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 7))
	ix := testIndex(t, nil, WithMaxFieldBytes(12))
	want := map[string]movie{}
	checkIndex(t, ix, want)

	for step := range 400 {
		switch op := r.IntN(10); {
		case op == 0:
			var items []movie
			for range r.IntN(1500) {
				items = append(items, randomMovie(r, r.IntN(2000)))
			}
			if err := ix.Build(items); err != nil {
				t.Fatal(err)
			}
			clear(want)
			for _, m := range items {
				want[m.ID] = m
			}
		case op < 6:
			var items []movie
			for range 1 + r.IntN(40) {
				items = append(items, randomMovie(r, r.IntN(2000)))
			}
			if err := ix.Upsert(items...); err != nil {
				t.Fatal(err)
			}
			for _, m := range items {
				want[m.ID] = m
			}
		default:
			var rm []string
			for range 1 + r.IntN(60) {
				rm = append(rm, fmt.Sprint(r.IntN(2000)))
			}
			expected := 0
			for _, id := range rm {
				if _, ok := want[id]; ok {
					expected++
					delete(want, id)
				}
			}
			if got := ix.Remove(rm...); got != expected {
				t.Fatalf("step %d: removed %d, want %d", step, got, expected)
			}
		}
		checkIndex(t, ix, want)
	}
}

func TestRemoveEverything(t *testing.T) {
	var items []movie
	want := map[string]movie{}
	for i := range chunkSize*2 + 3 {
		m := movie{ID: fmt.Sprint(i), Title: fmt.Sprint("Item ", i)}
		items = append(items, m)
		want[m.ID] = m
	}
	ix := testIndex(t, items)
	checkIndex(t, ix, want)

	all := slices.Collect(maps.Keys(want))
	eq(t, ix.Remove(all...), len(all))
	eq(t, ix.Remove(all...), 0)
	checkIndex(t, ix, map[string]movie{})
	eq(t, len(ix.snap.Load().chunks), 0)
	eq(t, len(ix.Search("item")), 0)

	if err := ix.Upsert(movie{ID: "x", Title: "Item again"}); err != nil {
		t.Fatal(err)
	}
	eq(t, ids(ix.Search("item")), []string{"x"})
}

func TestBuildDuplicatesLastWins(t *testing.T) {
	var items []movie
	for i := range chunkSize + 10 {
		items = append(items, movie{ID: fmt.Sprint(i), Title: "First"})
	}
	items = append(items, movie{ID: "0", Title: "Second"}, movie{ID: "1", Title: "Second"})
	ix := testIndex(t, items)
	eq(t, ix.Len(), chunkSize+10)
	eq(t, ids(ix.Search("second")), []string{"0", "1"})
}

func TestBuildEmptyIDKeepsIndex(t *testing.T) {
	ix := testIndex(t, []movie{{ID: "1", Title: "Kept"}})
	if err := ix.Build([]movie{{ID: "2", Title: "New"}, {Title: "No id"}}); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("got %v", err)
	}
	eq(t, ids(ix.Search("kept")), []string{"1"})
	if err := ix.Upsert(movie{ID: "3", Title: "Fine"}, movie{Title: "No id"}); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("got %v", err)
	}
	eq(t, ix.Len(), 1)
	checkIndex(t, ix, map[string]movie{"1": {ID: "1", Title: "Kept"}})
}

func TestBuildSeq(t *testing.T) {
	ix := testIndex(t, nil)
	seq := func(yield func(movie) bool) {
		for i := range 1000 {
			if !yield(movie{ID: fmt.Sprint(i), Title: fmt.Sprint("Streamed ", i)}) {
				return
			}
		}
	}
	if err := ix.BuildSeq(seq); err != nil {
		t.Fatal(err)
	}
	eq(t, ix.Len(), 1000)
	eq(t, ids(ix.Search("streamed 999")), []string{"999"})
}

func TestUpsertCopiesOnlyTouchedChunks(t *testing.T) {
	var items []movie
	for i := range chunkSize * 4 {
		items = append(items, movie{ID: fmt.Sprint(i), Title: "Old"})
	}
	ix := testIndex(t, items)
	before := ix.snap.Load()

	if err := ix.Upsert(movie{ID: fmt.Sprint(chunkSize + 1), Title: "New"}); err != nil {
		t.Fatal(err)
	}
	after := ix.snap.Load()
	for ci := range before.chunks {
		if shared := before.chunks[ci] == after.chunks[ci]; shared != (ci != 1) {
			t.Errorf("chunk %d shared %v", ci, shared)
		}
	}
	eq(t, before.chunks[1].entries[1].item.Title, "Old", "the old snapshot is unchanged")
	eq(t, ids(ix.Search("new")), []string{fmt.Sprint(chunkSize + 1)})
}

func TestFuncValues(t *testing.T) {
	ix := testIndex(t, nil)
	var (
		build  BuildFunc[movie]  = ix.Build
		update UpdateFunc[movie] = ix.Upsert
		remove RemoveFunc        = ix.Remove
	)
	if err := build([]movie{{ID: "1", Title: "Built"}}); err != nil {
		t.Fatal(err)
	}
	if err := update(movie{ID: "2", Title: "Built too"}); err != nil {
		t.Fatal(err)
	}
	eq(t, ids(ix.Search("built")), []string{"1", "2"})
	eq(t, remove("1", "nope"), 1)
	eq(t, ids(ix.Search("built")), []string{"2"})
	if err := update(); err != nil {
		t.Fatal(err)
	}
}

func TestUpsertPanicLeavesIndexConsistent(t *testing.T) {
	ix, err := New[movie](WithScore(func(m movie) float64 {
		if m.ID == "boom" {
			panic("score")
		}
		return 0
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Build([]movie{{ID: "1", Title: "One"}}); err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() { _ = recover() }()
		_ = ix.Upsert(movie{ID: "2", Title: "Two"}, movie{ID: "boom", Title: "Boom"})
	}()
	if err := ix.Upsert(movie{ID: "3", Title: "Three"}); err != nil {
		t.Fatal(err)
	}
	checkIndex(t, ix, map[string]movie{"1": {ID: "1", Title: "One"}, "3": {ID: "3", Title: "Three"}})
}
