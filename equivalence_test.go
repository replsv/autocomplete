package autocomplete

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// linearSearch is Search without the word index: it scores every entry of the snapshot.
func linearSearch[T any](ix *Index[T], q string) []Result[T] {
	s := ix.snap.Load()
	nq := normalize(ix.trimQuery(q))
	if s.n == 0 || len(nq) < ix.cfg.minQueryLen {
		return nil
	}
	qr := newQuery(nq, ix.cfg.maxTokens)
	var all []scored
	for ci, c := range s.chunks {
		for j := range c.entries {
			if tier, sc := ix.scoreEntry(c, j, qr, TierAnyOrder); tier > 0 {
				e := &c.entries[j]
				all = append(all, scored{pos: ci*chunkSize + j, tier: tier, score: sc, base: e.base, id: e.id})
			}
		}
	}
	slices.SortFunc(all, func(a, b scored) int {
		if a.betterThan(&b) {
			return -1
		}
		return 1
	})
	results := make([]Result[T], 0, ix.cfg.limit)
	for _, sc := range all[:min(len(all), ix.cfg.limit)] {
		results = append(results, ix.result(s.chunks[sc.pos/chunkSize], sc.pos%chunkSize, qr, sc))
	}
	return results
}

func checkSame[T any](t *testing.T, ix *Index[T], q string) {
	t.Helper()
	got, want := ix.Search(q), linearSearch(ix, q)
	if len(got) == 0 && len(want) == 0 {
		return
	}
	eq(t, got, want, q)
}

// randomQueries draws queries from the words of the items: prefixes of one to four words, in order or not, with
// typos and words that match nothing.
func randomQueries(r *rand.Rand, texts []string, n int) []string {
	var words []string
	for _, t := range texts {
		words = append(words, strings.Fields(normalize(t))...)
	}
	qs := []string{"st", "s", "the", "the night", "a", "an st", "zzqx", "o lupta"}
	for range n {
		var b strings.Builder
		for w := range 1 + r.IntN(4) {
			if w > 0 {
				b.WriteByte(' ')
			}
			word := words[r.IntN(len(words))]
			switch r.IntN(8) {
			case 0:
				word += "q"
			case 1, 2:
			default:
				word = word[:1+r.IntN(len(word))]
			}
			b.WriteString(word)
		}
		qs = append(qs, b.String())
	}
	return qs
}

func benchTexts(movies []benchMovie) []string {
	var texts []string
	for _, m := range movies {
		texts = append(texts, m.Title)
		if m.AltTitle != nil {
			texts = append(texts, *m.AltTitle)
		}
	}
	return texts
}

func TestSearchMatchesLinearScan(t *testing.T) {
	movies := benchMovies(20000)
	r := rand.New(rand.NewPCG(5, 6))
	queries := randomQueries(r, benchTexts(movies), 600)
	for _, opts := range [][]Option{
		{WithScore(benchScore)},
		{WithScore(benchScore), WithLimit(1)},
		{WithScore(benchScore), WithLimit(50), WithMinQueryLen(1)},
		{WithScore(func(m benchMovie) float64 { return float64(m.Votes % 3) }), WithMaxTokens(2)},
		{WithMaxFieldBytes(9), WithArticles()},
		{},
	} {
		ix, err := New[benchMovie](opts...)
		if err != nil {
			t.Fatal(err)
		}
		if err := ix.Build(movies); err != nil {
			t.Fatal(err)
		}
		for _, q := range queries {
			checkSame(t, ix, q)
		}
	}
}

// TestSearchMatchesLinearScanAfterWrites checks the dirty positions and the reindexing.
func TestSearchMatchesLinearScanAfterWrites(t *testing.T) {
	movies := benchMovies(6000)
	r := rand.New(rand.NewPCG(8, 9))
	queries := randomQueries(r, benchTexts(movies), 60)
	ix, err := New[benchMovie](WithScore(benchScore), WithLimit(5))
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Build(movies[:3000]); err != nil {
		t.Fatal(err)
	}
	reindexed := 0
	for step := range 120 {
		words := ix.snap.Load().words
		switch op := r.IntN(4); op {
		case 0:
			var rm []string
			for range 1 + r.IntN(40) {
				rm = append(rm, movies[r.IntN(len(movies))].ID)
			}
			ix.Remove(rm...)
		default:
			var items []benchMovie
			for range 1 + r.IntN(60) {
				m := movies[r.IntN(len(movies))]
				m.Votes = r.IntN(500000)
				if r.IntN(3) == 0 {
					m.Title = movies[r.IntN(len(movies))].Title
				}
				items = append(items, m)
			}
			if err := ix.Upsert(items...); err != nil {
				t.Fatal(err)
			}
		}
		if ix.snap.Load().words != words {
			reindexed++
		}
		for _, q := range queries {
			checkSame(t, ix, q)
		}
		if t.Failed() {
			t.Fatalf("step %d", step)
		}
	}
	if reindexed == 0 {
		t.Error("never reindexed")
	}
}

func TestSearchExactBeatsScore(t *testing.T) {
	var items []movie
	for i := range 500 {
		items = append(items, movie{ID: fmt.Sprintf("%04d", i), Title: fmt.Sprintf("Star %d", i), Votes: 1e6})
	}
	items = append(items,
		movie{ID: "exact", Title: "Star"},
		movie{ID: "article", Title: "The Star Ship"},
		movie{ID: "aligned", Title: "Stars Shipped"},
		movie{ID: "deep", Title: "Big Star Ship", Votes: 1e9},
	)
	ix := testIndex(t, items, WithLimit(3))
	eq(t, ids(ix.Search("star")), []string{"exact", "0000", "0001"})
	eq(t, ids(ix.Search("star ship")), []string{"article", "aligned", "deep"})
	eq(t, ids(ix.Search("the star ship")), []string{"article"})
	for _, q := range []string{"star", "star ship", "st sh", "ship star", "the", "big"} {
		checkSame(t, ix, q)
	}
}
