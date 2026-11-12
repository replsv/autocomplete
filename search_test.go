package autocomplete

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestSearch(t *testing.T) {
	ix := testIndex(t, []movie{
		{ID: "0054331", Title: "O luptă după alta", Alt: ptr("One Battle After Another"), Year: 2025, Votes: 100},
		{ID: "0133093", Title: "The Matrix", Year: 1999, Votes: 2000000},
		{ID: "0234215", Title: "The Matrix Reloaded", Year: 2003, Votes: 600000},
		{ID: "0000100", Title: "Dune", Alt: ptr("Dune: Partea întâi"), Year: 2021, Votes: 5},
		{ID: "0000101", Title: "Dune Pt 2", Year: 2024, Votes: 5},
		{ID: "0000200", Title: "", Alt: ptr("Doar titlu românesc")},
	})

	t.Run("in order beats any order", func(t *testing.T) {
		res := ix.Search("lupta alta")
		eq(t, ids(res), []string{"0054331"})
		eq(t, res[0].Tier, TierInOrder)
		res = ix.Search("alta lupta")
		eq(t, ids(res), []string{"0054331"})
		eq(t, res[0].Tier, TierAnyOrder)
	})

	t.Run("exact via article skip ranks first", func(t *testing.T) {
		res := ix.Search("matrix")
		eq(t, ids(res), []string{"0133093", "0234215"})
		eq(t, res[0].Tier, TierExact)
		eq(t, res[1].Tier, TierPrefix)
	})

	t.Run("too short", func(t *testing.T) {
		for _, q := range []string{"x", "  ", "-!", ""} {
			eq(t, len(ix.Search(q)), 0, q)
		}
	})

	t.Run("alt title only", func(t *testing.T) {
		res := ix.Search("dune partea")
		eq(t, ids(res), []string{"0000100"})
		eq(t, res[0].Item.Title, "Dune")
		eq(t, res[0].Ranges("Title"), []Range(nil))
		eq(t, res[0].Ranges("Alt"), []Range{{0, 12}}, "a prefix match is one range")

		eq(t, ids(ix.Search("doar titlu")), []string{"0000200"})
	})

	t.Run("item is returned", func(t *testing.T) {
		res := ix.Search("dune pt 2")
		eq(t, ids(res), []string{"0000101"})
		eq(t, res[0].Item, movie{ID: "0000101", Title: "Dune Pt 2", Year: 2024, Votes: 5})
	})
}

func TestSearchTieBreak(t *testing.T) {
	t.Run("score then id", func(t *testing.T) {
		ix := testIndex(t, []movie{
			{ID: "3", Title: "Zebra Alpha", Year: 2000, Votes: 10},
			{ID: "1", Title: "Zebra Bravo", Year: 2000, Votes: 10},
			{ID: "2", Title: "Zebra Delta", Year: 2000, Votes: 10},
			{ID: "4", Title: "Zebra Gamma", Year: 2000, Votes: 11},
		})
		eq(t, ids(ix.Search("zebra")), []string{"4", "1", "2", "3"})
	})

	t.Run("tier beats score", func(t *testing.T) {
		ix := testIndex(t, []movie{
			{ID: "1", Title: "Night Star", Votes: 1e9},
			{ID: "2", Title: "Star Night", Votes: 0},
		})
		eq(t, ids(ix.Search("star")), []string{"2", "1"})
	})

	t.Run("field boost", func(t *testing.T) {
		ix := testIndex(t, []movie{
			{ID: "1", Title: "Other", Alt: ptr("Zebra")},
			{ID: "2", Title: "Zebra"},
		})
		res := ix.Search("zebra")
		eq(t, ids(res), []string{"2", "1"})
		eq(t, res[0].Score-res[1].Score, 25.0)
	})

	t.Run("extra words penalty", func(t *testing.T) {
		ix := testIndex(t, []movie{
			{ID: "1", Title: "Zebra one two three"},
			{ID: "2", Title: "Zebra one"},
		})
		eq(t, ids(ix.Search("zebra")), []string{"2", "1"})
	})
}

func TestSearchLimit(t *testing.T) {
	var many []movie
	for i := range 20 {
		many = append(many, movie{ID: fmt.Sprintf("%07d", i+1), Title: fmt.Sprintf("Film %d", i), Votes: i})
	}
	eq(t, len(testIndex(t, many).Search("film")), DefaultLimit)
	res := testIndex(t, many, WithLimit(3)).Search("film")
	eq(t, ids(res), []string{"0000020", "0000019", "0000018"})
}

func TestSearchTrimsLongQuery(t *testing.T) {
	long := strings.Repeat("a", DefaultMaxQueryRunes)
	ix := testIndex(t, []movie{{ID: "1", Title: long + "b"}}, WithMaxFieldBytes(200))

	eq(t, ids(ix.Search(long+"c")), []string{"1"})
	eq(t, ids(ix.Search(strings.Repeat("ă", DefaultMaxQueryRunes+1))), []string{"1"}, "runes, not bytes")
	eq(t, ids(ix.Search("  "+long+"  ")), []string{"1"})
}

func TestSearchHighlight(t *testing.T) {
	ix := testIndex(t, []movie{
		{ID: "0054331", Title: "O luptă după alta", Alt: ptr("One Battle After Another"), Year: 2025, Votes: 1},
		{ID: "0000002", Title: "Straße", Year: 2000, Votes: 1},
		{ID: "0000003", Title: "Țara Șoimilor", Alt: ptr("Țara noastră"), Year: 2000, Votes: 1},
	})

	tests := []struct {
		name      string
		q         string
		wantTitle []Range
		wantAlt   []Range
	}{
		{"words in order", "lupt dup", []Range{{2, 6}, {8, 11}}, nil},
		{"alt title", "battle another", nil, []Range{{4, 10}, {17, 24}}},
		{"prefix merges into one range", "o lupta d", []Range{{0, 9}}, nil},
		{"sharp s", "strass", []Range{{0, 5}}, nil},
		{"both fields", "tara", []Range{{0, 4}}, []Range{{0, 4}}},
		{"any order", "soim tar", []Range{{0, 3}, {5, 9}}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := ix.Search(tt.q)
			if len(res) != 1 {
				t.Fatalf("got %d results", len(res))
			}
			eq(t, res[0].Ranges("Title"), tt.wantTitle)
			eq(t, res[0].Ranges("Alt"), tt.wantAlt)
		})
	}
}

func TestSearchMaxTokens(t *testing.T) {
	ix := testIndex(t, []movie{{ID: "1", Title: "alpha beta"}}, WithMaxTokens(1))
	eq(t, ids(ix.Search("alpha zzz")), []string{"1"}, "only the first token has to match a word")
	eq(t, len(testIndex(t, []movie{{ID: "1", Title: "alpha beta"}}).Search("alpha zzz")), 0)
}

func TestSearchMinQueryLen(t *testing.T) {
	ix := testIndex(t, []movie{{ID: "1", Title: "X Files"}}, WithMinQueryLen(1))
	eq(t, ids(ix.Search("x")), []string{"1"})
}

func TestSearchArticles(t *testing.T) {
	ms := []movie{{ID: "1", Title: "The Matrix"}}
	ix, err := New[movie]()
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Build(ms); err != nil {
		t.Fatal(err)
	}
	eq(t, ix.Search("matrix")[0].Tier, TierExact, "default articles")

	ix, err = New[movie](WithArticles())
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Build(ms); err != nil {
		t.Fatal(err)
	}
	eq(t, ix.Search("matrix")[0].Tier, TierInOrder, "no articles")
}

func TestSearchMaxFieldBytes(t *testing.T) {
	ix := testIndex(t, []movie{{ID: "1", Title: "Spider-Man: No Way Home"}}, WithMaxFieldBytes(12))
	eq(t, ids(ix.Search("spider man n")), []string{"1"})
	eq(t, ix.Search("spider man n")[0].Ranges("Title"), []Range{{0, 13}})
	eq(t, len(ix.Search("home")), 0, "past the bound")
}

func TestSearchEmptyIndex(t *testing.T) {
	ix, err := New[movie]()
	if err != nil {
		t.Fatal(err)
	}
	eq(t, len(ix.Search("anything")), 0)
	eq(t, ix.Len(), 0)
}

func TestSearchPointerItems(t *testing.T) {
	ix, err := New[*movie](WithID(func(m *movie) string { return m.ID }))
	if err != nil {
		t.Fatal(err)
	}
	m := &movie{ID: "1", Title: "Pointer Movie"}
	if err := ix.Build([]*movie{m}); err != nil {
		t.Fatal(err)
	}
	res := ix.Search("point")
	eq(t, ids(res), []string{"1"})
	if res[0].Item != m {
		t.Error("the result does not hold the indexed pointer")
	}
}

func TestSearchNilPointerItem(t *testing.T) {
	ix, err := New[*movie](WithID(func(m *movie) string {
		if m == nil {
			return "nil"
		}
		return m.ID
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Build([]*movie{nil, {ID: "1", Title: "Real"}}); err != nil {
		t.Fatal(err)
	}
	eq(t, ix.Len(), 2)
	eq(t, ids(ix.Search("real")), []string{"1"})
}

func TestHighlightAfterItemMutated(t *testing.T) {
	alt := ptr("Mutable Title")
	ix := testIndex(t, []movie{{ID: "1", Title: "Other", Alt: alt}})
	*alt = "Changed"
	res := ix.Search("mutable")
	eq(t, ids(res), []string{"1"}, "the index keeps what was indexed")
	eq(t, res[0].Ranges("Alt"), []Range(nil), "no ranges for text that changed")
}

// TestSearchMatchesBruteForce checks the pruned scan against scoring every entry.
func TestSearchMatchesBruteForce(t *testing.T) {
	movies := benchMovies(5000)
	ix, err := New[benchMovie](WithScore(benchScore), WithLimit(5))
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Build(movies); err != nil {
		t.Fatal(err)
	}
	queries := []string{"st", "da", "dark night", "night dark", "the night", "pădu inim", "ka ri", "star", "an",
		"love war", "ri", "zzqx", "mo la", "night dark star road"}
	for _, q := range queries {
		qr := newQuery(Normalize(q), DefaultMaxTokens)
		var all []scored
		for ci, c := range ix.snap.Load().chunks {
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
		var want []string
		for _, c := range all[:min(len(all), 5)] {
			want = append(want, c.id)
		}
		got := ids(ix.Search(q))
		if len(want) == 0 {
			want = []string{}
		}
		eq(t, got, want, q)
	}
}
