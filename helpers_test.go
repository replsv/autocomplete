package autocomplete

import (
	"math"
	"reflect"
	"testing"
)

type movie struct {
	ID    string  `searchindex:"id"`
	Title string  `searchindex:"true,boost=25"`
	Alt   *string `searchindex:"true"`
	Year  int
	Votes int
}

var roArticles = []string{"the", "a", "an", "o", "un"}

func ptr(s string) *string { return &s }

func movieScore(m movie) float64 {
	return 60*math.Log10(1+float64(max(m.Votes, 0))) + 4*float64(min(10, max(0, 10-(2026-m.Year))))
}

func testIndex(t testing.TB, movies []movie, opts ...Option) *Index[movie] {
	t.Helper()
	opts = append([]Option{WithScore(movieScore), WithArticles(roArticles...)}, opts...)
	ix, err := New[movie](opts...)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Build(movies); err != nil {
		t.Fatal(err)
	}
	return ix
}

func ids[T any](results []Result[T]) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, r.ID)
	}
	return out
}

func eq[V any](t testing.TB, got, want V, msg ...any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v %v", got, want, msg)
	}
}
