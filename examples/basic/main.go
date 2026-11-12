// Command basic indexes a few books and prints the matches of some queries, with the matched text in brackets.
package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/replsv/autocomplete"
)

type Book struct {
	ISBN   string `searchindex:"id"`
	Title  string `searchindex:"true,boost=10"`
	Author string `searchindex:"true"`
	Year   int
}

func main() {
	ix, err := autocomplete.New[Book]()
	if err != nil {
		log.Fatal(err)
	}
	err = ix.Build([]Book{
		{ISBN: "9780141439518", Title: "Pride and Prejudice", Author: "Jane Austen", Year: 1813},
		{ISBN: "9780140449136", Title: "Crime and Punishment", Author: "Fyodor Dostoevsky", Year: 1866},
		{ISBN: "9782070360024", Title: "L'Étranger", Author: "Albert Camus", Year: 1942},
		{ISBN: "9780060883287", Title: "One Hundred Years of Solitude", Author: "Gabriel García Márquez", Year: 1967},
		{ISBN: "9780743273565", Title: "The Great Gatsby", Author: "F. Scott Fitzgerald", Year: 1925},
	})
	if err != nil {
		log.Fatal(err)
	}

	for _, q := range []string{"pride", "great gat", "garcia", "etran", "years solitude", "punishment crime"} {
		fmt.Printf("%q\n", q)
		for _, r := range ix.Search(q) {
			fmt.Printf("  %-14s %s / %s (tier %d, score %.0f)\n", r.ID,
				mark(r.Item.Title, r.Ranges("Title")), mark(r.Item.Author, r.Ranges("Author")), r.Tier, r.Score)
		}
	}
}

// mark puts the matched rune ranges of s in brackets.
func mark(s string, ranges []autocomplete.Range) string {
	var b strings.Builder
	i := 0
	for _, r := range s {
		if len(ranges) > 0 && i == ranges[0].Start {
			b.WriteByte('[')
		}
		b.WriteRune(r)
		i++
		if len(ranges) > 0 && i == ranges[0].End {
			b.WriteByte(']')
			ranges = ranges[1:]
		}
	}
	return b.String()
}
