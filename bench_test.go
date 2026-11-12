package autocomplete

import (
	"fmt"
	"math"
	"math/rand/v2"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

type benchMovie struct {
	ID       string  `searchindex:"id"`
	Title    string  `searchindex:"true,boost=25"`
	AltTitle *string `searchindex:"true"`
	Year     int
	Votes    int
}

var benchWords = []string{
	"night", "dark", "star", "love", "war", "king", "last", "road", "city", "wars", "matrix", "dune",
	"river", "ghost", "summer", "winter", "secret", "garden", "empire", "shadow", "storm", "island", "stone",
	"lupta", "dupa", "alta", "casa", "omul", "noapte", "soimilor", "padurea", "inima", "vara",
	"luptă", "după", "Țara", "Șoimilor", "pădurea", "inimă", "vară", "fată", "băiat", "îngerul", "străin",
	"Amélie", "Straße", "Łódź", "château", "garçon", "mädchen", "über", "señor", "niño", "Æon", "café",
}

var (
	benchArticles  = []string{"The", "A", "An"}
	benchSyllables = []string{
		"ka", "ri", "to", "mo", "la", "ne", "vi", "sa", "do", "ru", "be", "ta", "go", "li", "fe", "zu",
		"pă", "ță", "șe", "câ", "în", "lé", "ço", "nü", "ñe", "ør", "ma", "st", "tr", "an", "el", "or",
	}
)

// benchVocab is the fixed words plus pseudo-words of two to four syllables, so the vocabulary is large like a
// real catalog and the prefilter sees realistic letter pairs.
func benchVocab(r *rand.Rand) []string {
	vocab := slices.Clone(benchWords)
	for range 6000 {
		var b strings.Builder
		for range 2 + r.IntN(3) {
			b.WriteString(benchSyllables[r.IntN(len(benchSyllables))])
		}
		vocab = append(vocab, b.String())
	}
	return vocab
}

// benchMovies generates n deterministic titles: one to five words drawn with a Zipf distribution, a quarter with
// a leading article, half with an alternative title, a mix of ASCII and diacritics.
func benchMovies(n int) []benchMovie {
	r := rand.New(rand.NewPCG(1, 2))
	vocab := benchVocab(r)
	zipf := rand.NewZipf(r, 1.1, 2, uint64(len(vocab)-1))
	title := func() string {
		var b strings.Builder
		if r.IntN(4) == 0 {
			b.WriteString(benchArticles[r.IntN(len(benchArticles))])
			b.WriteByte(' ')
		}
		for w := range 1 + r.IntN(5) {
			if w > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(vocab[zipf.Uint64()])
		}
		return b.String()
	}
	movies := make([]benchMovie, n)
	for i := range movies {
		m := benchMovie{
			ID:    "tt" + strconv.Itoa(1000000+i),
			Title: title(),
			Year:  1950 + r.IntN(76),
			Votes: r.IntN(500000),
		}
		if r.IntN(2) == 0 {
			alt := title()
			m.AltTitle = &alt
		}
		movies[i] = m
	}
	return movies
}

func benchScore(m benchMovie) float64 {
	return 60*math.Log10(1+float64(max(m.Votes, 0))) + 4*float64(min(10, max(0, 10-(2026-m.Year))))
}

func benchIndex(b *testing.B, movies []benchMovie) *Index[benchMovie] {
	b.Helper()
	ix, err := New[benchMovie](WithScore(benchScore), WithCapacity(len(movies)))
	if err != nil {
		b.Fatal(err)
	}
	if err := ix.Build(movies); err != nil {
		b.Fatal(err)
	}
	return ix
}

var benchSizes = []int{10_000, 60_000, 200_000}

func sizeName(n int) string {
	return fmt.Sprintf("n=%dk", n/1000)
}

func BenchmarkSearch(b *testing.B) {
	queries := []struct{ name, q string }{
		{"prefix", "st"},
		{"multiword", "dark night"},
		{"anyorder", "night dark star"},
		{"diacritics", "pădu inim"},
		{"nomatch", "zzqx"},
	}
	for _, n := range benchSizes {
		ix := benchIndex(b, benchMovies(n))
		for _, q := range queries {
			b.Run(sizeName(n)+"/"+q.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					ix.Search(q.q)
				}
			})
		}
	}
}

func BenchmarkBuild(b *testing.B) {
	for _, n := range benchSizes {
		movies := benchMovies(n)
		b.Run(sizeName(n), func(b *testing.B) {
			b.ReportAllocs()
			ix, err := New[benchMovie](WithScore(benchScore), WithCapacity(n))
			if err != nil {
				b.Fatal(err)
			}
			for b.Loop() {
				if err := ix.Build(movies); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(retainedPerItem(b, movies), "heap-B/item")
		})
	}
}

// retainedPerItem is the live heap a built index adds, per item, on top of the items themselves.
func retainedPerItem(b *testing.B, movies []benchMovie) float64 {
	b.Helper()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	ix := benchIndex(b, movies)
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(ix)
	return float64(after.HeapAlloc-before.HeapAlloc) / float64(len(movies))
}

func BenchmarkUpsert(b *testing.B) {
	const n = 60_000
	movies := benchMovies(n)
	ix := benchIndex(b, movies)

	b.Run(sizeName(n)+"/single", func(b *testing.B) {
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			m := movies[(i*7919)%n]
			m.Votes++
			if err := ix.Upsert(m); err != nil {
				b.Fatal(err)
			}
			i++
		}
	})
	b.Run(sizeName(n)+"/batch100", func(b *testing.B) {
		b.ReportAllocs()
		batch := make([]benchMovie, 100)
		i := 0
		for b.Loop() {
			for j := range batch {
				batch[j] = movies[(i*7919)%n]
				i++
			}
			if err := ix.Upsert(batch...); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run(sizeName(n)+"/insert-remove", func(b *testing.B) {
		b.ReportAllocs()
		m := benchMovie{ID: "new", Title: "Brand New Title"}
		for b.Loop() {
			if err := ix.Upsert(m); err != nil {
				b.Fatal(err)
			}
			ix.Remove(m.ID)
		}
	})
}

// BenchmarkReindex is the merge of the written positions into a new word index, which Upsert and Remove run
// once they exceed max(minMerge, n/mergeRatio).
func BenchmarkReindex(b *testing.B) {
	for _, n := range benchSizes {
		ix := benchIndex(b, benchMovies(n))
		b.Run(sizeName(n), func(b *testing.B) {
			b.ReportAllocs()
			s := *ix.snap.Load()
			for b.Loop() {
				ix.reindex(&s)
			}
		})
	}
}

// BenchmarkSearchDirty searches with 500 upserts pending, scanned linearly next to the word index.
func BenchmarkSearchDirty(b *testing.B) {
	const n, pending = 60_000, 500
	movies := benchMovies(n)
	ix := benchIndex(b, movies)
	for i := range pending {
		m := movies[(i*7919)%n]
		m.Votes++
		if err := ix.Upsert(m); err != nil {
			b.Fatal(err)
		}
	}
	if s := ix.snap.Load(); len(s.moved) != pending {
		b.Fatalf("%d dirty positions, want %d", len(s.moved), pending)
	}
	for _, q := range []struct{ name, q string }{{"prefix", "st"}, {"multiword", "dark night"}, {"nomatch", "zzqx"}} {
		b.Run(sizeName(n)+"/"+q.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				ix.Search(q.q)
			}
		})
	}
}

// BenchmarkGC is a forced collection with a 200k index live: the cost of the pointers the index holds.
func BenchmarkGC(b *testing.B) {
	const n = 200_000
	ix := benchIndex(b, benchMovies(n))
	for b.Loop() {
		runtime.GC()
	}
	runtime.KeepAlive(ix)
}
