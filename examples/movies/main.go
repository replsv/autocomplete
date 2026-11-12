// Command movies ranks titles by votes and recency: the primary title gets a +25 boost over the alternative
// title, and the item score adds popularity (60·log10(1+votes)) and recency (up to +40 for titles less than ten
// years old).
package main

import (
	"fmt"
	"log"
	"math"
	"time"

	"github.com/replsv/autocomplete"
)

type Movie struct {
	IMDbID   string  `searchindex:"id"`
	Title    string  `searchindex:"true,boost=25"`
	AltTitle *string `searchindex:"true"`
	Year     int
	Votes    int
	Serial   bool
}

func score(nowYear int) func(Movie) float64 {
	return func(m Movie) float64 {
		popularity := 60 * math.Log10(1+float64(max(m.Votes, 0)))
		recency := 4 * float64(min(10, max(0, 10-(nowYear-m.Year))))
		return popularity + recency
	}
}

func main() {
	ix, err := autocomplete.New[Movie](
		autocomplete.WithScore(score(time.Now().Year())),
		autocomplete.WithArticles("the", "a", "an", "o", "un"), // English and Romanian
	)
	if err != nil {
		log.Fatal(err)
	}

	alt := func(s string) *string { return &s }
	movies := []Movie{
		{IMDbID: "tt30144839", Title: "O luptă după alta", AltTitle: alt("One Battle After Another"), Year: 2025, Votes: 100000},
		{IMDbID: "tt0133093", Title: "The Matrix", Year: 1999, Votes: 2000000},
		{IMDbID: "tt0234215", Title: "The Matrix Reloaded", Year: 2003, Votes: 600000},
		{IMDbID: "tt1160419", Title: "Dune", AltTitle: alt("Dune: Partea întâi"), Year: 2021, Votes: 900000},
		{IMDbID: "tt15239678", Title: "Dune: Partea a doua", AltTitle: alt("Dune: Part Two"), Year: 2024, Votes: 600000},
		{IMDbID: "tt0944947", Title: "Urzeala tronurilor", AltTitle: alt("Game of Thrones"), Year: 2011, Votes: 2400000, Serial: true},
		{IMDbID: "tt0056172", Title: "Lawrence al Arabiei", AltTitle: alt("Lawrence of Arabia"), Year: 1962, Votes: 330000},
	}
	if err := ix.Build(movies); err != nil {
		log.Fatal(err)
	}

	for _, q := range []string{"matrix", "dune", "dune part", "lupta alta", "battle another", "game thr", "thrones game"} {
		fmt.Printf("%q\n", q)
		for _, r := range ix.Search(q) {
			kind := "film"
			if r.Item.Serial {
				kind = "serial"
			}
			fmt.Printf("  %-10s %-24s %4d %-6s tier %d score %6.1f matches %v\n",
				r.ID, r.Item.Title, r.Item.Year, kind, r.Tier, r.Score, r.Matches)
		}
	}
}
