package autocomplete

import "testing"

func testField(t *testing.T, text string) field {
	t.Helper()
	ix, err := New[movie](WithArticles(roArticles...))
	if err != nil {
		t.Fatal(err)
	}
	n := normalize(text)
	return field{norm: n, skip: ix.articleSkip(n)}
}

func TestMatchFieldTiers(t *testing.T) {
	f := testField(t, "O luptă după alta")
	tests := []struct {
		name string
		q    string
		want Tier
	}{
		{"exact", "o lupta dupa alta", TierExact},
		{"exact without article", "lupta dupa alta", TierExact},
		{"prefix", "lupta dup", TierPrefix},
		{"aligned", "o lup du al", TierAligned},
		{"aligned after article", "lup du al", TierAligned},
		{"in order", "lupta alta", TierInOrder},
		{"any order", "alta lupta", TierAnyOrder},
		{"first token misses", "xyz lupta", 0},
		{"second token misses", "lupta xyz", 0},
		{"one word two tokens", "alta alt", 0},
		{"short token before long one", "dup du", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tier, _ := matchField(f, newQuery(tt.q, defaultMaxTokens), TierAnyOrder, false)
			eq(t, tier, tt.want)
		})
	}
}

func TestMatchFieldEmpty(t *testing.T) {
	tier, hits := matchField(field{}, newQuery("ab", defaultMaxTokens), TierAnyOrder, true)
	eq(t, tier, Tier(0))
	eq(t, len(hits), 0)
}

func TestMatchFieldAnyOrderLongestFirst(t *testing.T) {
	tier, hits := matchField(testField(t, "Matrix Man"), newQuery("ma mat", defaultMaxTokens), TierAnyOrder, true)
	eq(t, tier, TierAnyOrder)
	eq(t, len(hits), 2)
}

func TestMatchFieldAlignedAfterArticle(t *testing.T) {
	tier, hits := matchField(testField(t, "The Dark Knight"), newQuery("dar kni", defaultMaxTokens), TierAnyOrder, true)
	eq(t, tier, TierAligned)
	eq(t, hits, []hit{{4, 3}, {9, 3}})

	ix := testIndex(t, []movie{
		{ID: "2", Title: "The Dark Knight Rises", Votes: 10},
		{ID: "1", Title: "The Dark Knight", Votes: 10},
	})
	eq(t, ids(ix.Search("dar kni")), []string{"1", "2"})
}

func TestMatchFieldMultiWordArticle(t *testing.T) {
	ix, err := New[movie](WithArticles("die da"))
	if err != nil {
		t.Fatal(err)
	}
	n := normalize("Die da Oben")
	f := field{norm: n, skip: ix.articleSkip(n)}
	tier, hits := matchField(f, newQuery("ob", defaultMaxTokens), TierAnyOrder, true)
	eq(t, tier, TierPrefix)
	eq(t, hits, []hit{{7, 2}})
}

func TestFilterRejectsOnlyNonMatches(t *testing.T) {
	titles := []string{"O luptă după alta", "The Dark Knight", "Matrix Man", "Straße", "a b c d"}
	queries := []string{"lu", "dar kni", "ma mat", "strass", "b d", "x", "da", "alta o"}
	for _, title := range titles {
		f := testField(t, title)
		ff := newFieldFilter(f.norm)
		for _, q := range queries {
			qr := newQuery(normalize(q), defaultMaxTokens)
			if tier, _ := matchField(f, qr, TierAnyOrder, false); tier > 0 && !qr.filter.matches(ff) {
				t.Errorf("filter rejects %q for %q, which matches at tier %d", title, q, tier)
			}
		}
	}
}
