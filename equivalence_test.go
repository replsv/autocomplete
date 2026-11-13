package autocomplete

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	qs := []string{"st", "s", "the", "the night", "a", "an st", "zzqx", "o lupta", "st ka", "lo ri ta", "night xq"}
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
		for _, q := range queries {
			checkSame(t, ix, q)
		}
		if step%3 == 0 {
			ix.waitRebuild()
			for _, q := range queries {
				checkSame(t, ix, q)
			}
		}
		if ix.snap.Load().words != words {
			reindexed++
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

func (ix *Index[T]) waitRebuild() {
	ix.rebuilds.Wait()
}

// holdRebuilds makes the rebuilds of ix wait between indexing and swapping until release is called; started
// receives once a rebuild is waiting.
func holdRebuilds[T any](ix *Index[T]) (started <-chan struct{}, release func()) {
	ch, gate := make(chan struct{}, 1), make(chan struct{})
	ix.rebuilt = func() {
		select {
		case ch <- struct{}{}:
		default:
		}
		<-gate
	}
	return ch, sync.OnceFunc(func() { close(gate) })
}

// stale upserts items with new votes until a rebuild starts.
func stale(t *testing.T, ix *Index[benchMovie], movies []benchMovie, r *rand.Rand, started <-chan struct{}) {
	t.Helper()
	for {
		select {
		case <-started:
			return
		default:
		}
		m := movies[r.IntN(len(movies))]
		m.Votes = r.IntN(500000)
		if err := ix.Upsert(m); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSearchMatchesLinearScanDuringRebuild(t *testing.T) {
	movies := benchMovies(4000)
	r := rand.New(rand.NewPCG(10, 11))
	queries := randomQueries(r, benchTexts(movies), 80)
	ix, err := New[benchMovie](WithScore(benchScore), WithLimit(5))
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Build(movies[:3000]); err != nil {
		t.Fatal(err)
	}
	started, release := holdRebuilds(ix)
	defer release()
	stale(t, ix, movies[:3000], r, started)
	before := ix.snap.Load().words

	// replaced, moved by removes, appended past the indexed positions, removed from the end
	for step := range 30 {
		switch step % 3 {
		case 0:
			var items []benchMovie
			for range 50 {
				m := movies[r.IntN(len(movies))]
				m.Title = movies[r.IntN(len(movies))].Title
				items = append(items, m)
			}
			if err := ix.Upsert(items...); err != nil {
				t.Fatal(err)
			}
		case 1:
			var rm []string
			for range 30 {
				rm = append(rm, movies[r.IntN(len(movies))].ID)
			}
			ix.Remove(rm...)
		default:
			for _, q := range queries {
				checkSame(t, ix, q)
			}
		}
	}
	if ix.snap.Load().words != before {
		t.Fatal("the word index changed while its rebuild was held")
	}
	release()
	ix.waitRebuild()
	if ix.snap.Load().words == before {
		t.Fatal("the rebuild was not installed")
	}
	for _, q := range queries {
		checkSame(t, ix, q)
	}
}

func TestBuildDuringRebuild(t *testing.T) {
	movies := benchMovies(4000)
	r := rand.New(rand.NewPCG(12, 13))
	ix, err := New[benchMovie](WithScore(benchScore))
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Build(movies[:3000]); err != nil {
		t.Fatal(err)
	}
	started, release := holdRebuilds(ix)
	defer release()
	stale(t, ix, movies[:3000], r, started)

	if err := ix.Build(movies[1000:]); err != nil {
		t.Fatal(err)
	}
	built := ix.snap.Load()
	release()
	ix.waitRebuild()
	if ix.snap.Load() != built {
		t.Fatal("a rebuild started before the Build replaced its result")
	}
	eq(t, ix.Len(), 3000)
	for _, q := range randomQueries(r, benchTexts(movies), 80) {
		checkSame(t, ix, q)
	}
}

func TestConcurrentWritesAcrossRebuilds(t *testing.T) {
	movies := benchMovies(3000)
	ix, err := New[benchMovie](WithScore(benchScore))
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Build(movies[:2000]); err != nil {
		t.Fatal(err)
	}
	var rebuilt atomic.Int32
	ix.rebuilt = func() { rebuilt.Add(1) }

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for g := range 3 {
		readers.Go(func() {
			queries := []string{"st", "dark night", "night dark star", "pădu inim", "lo ri ta"}
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				for _, res := range ix.Search(queries[(g+i)%len(queries)]) {
					if res.ID != res.Item.ID || res.Tier == 0 {
						t.Errorf("inconsistent result %+v", res)
						return
					}
				}
			}
		})
	}
	var writers sync.WaitGroup
	for w := range 2 {
		writers.Go(func() {
			r := rand.New(rand.NewPCG(uint64(w), 14))
			for i := range 3000 {
				m := movies[r.IntN(len(movies))]
				if i%4 == 3 {
					ix.Remove(m.ID)
					continue
				}
				m.Votes = r.IntN(500000)
				if err := ix.Upsert(m); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	writers.Wait()
	close(stop)
	readers.Wait()
	ix.waitRebuild()
	if rebuilt.Load() < 2 {
		t.Errorf("%d rebuilds, want several", rebuilt.Load())
	}
	for _, q := range randomQueries(rand.New(rand.NewPCG(15, 16)), benchTexts(movies), 60) {
		checkSame(t, ix, q)
	}
}
