package autocomplete_test

import (
	"fmt"
	"log"

	"github.com/replsv/autocomplete"
)

type Book struct {
	ISBN   string `searchindex:"id"`
	Title  string `searchindex:"true,boost=10"`
	Author string `searchindex:"true"`
	Sales  int
}

var books = []Book{
	{ISBN: "9780141439518", Title: "Pride and Prejudice", Author: "Jane Austen", Sales: 20},
	{ISBN: "9780743273565", Title: "The Great Gatsby", Author: "F. Scott Fitzgerald", Sales: 30},
	{ISBN: "9780060883287", Title: "One Hundred Years of Solitude", Author: "Gabriel García Márquez", Sales: 25},
	{ISBN: "9782070360024", Title: "L'Étranger", Author: "Albert Camus", Sales: 15},
}

func ExampleNew() {
	ix, err := autocomplete.New[Book](
		autocomplete.WithScore(func(b Book) float64 { return float64(b.Sales) }),
		autocomplete.WithLimit(5),
		autocomplete.WithMaxItems(100_000),
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(ix.Len())
	// Output: 0
}

func ExampleIndex_Build() {
	ix, err := autocomplete.New[Book]()
	if err != nil {
		log.Fatal(err)
	}
	if err := ix.Build(books); err != nil {
		log.Fatal(err)
	}
	fmt.Println(ix.Len())
	// Output: 4
}

func ExampleIndex_Search() {
	ix, err := autocomplete.New[Book]()
	if err != nil {
		log.Fatal(err)
	}
	if err := ix.Build(books); err != nil {
		log.Fatal(err)
	}
	for _, q := range []string{"great gat", "garcia", "etran", "solitude years"} {
		for _, r := range ix.Search(q) {
			fmt.Println(q, "->", r.Item.Title, r.Tier, r.Matches)
		}
	}
	// Output:
	// great gat -> The Great Gatsby 4 [{Title [{4 13}]}]
	// garcia -> One Hundred Years of Solitude 2 [{Author [{8 14}]}]
	// etran -> L'Étranger 2 [{Title [{2 7}]}]
	// solitude years -> One Hundred Years of Solitude 1 [{Title [{12 17} {21 29}]}]
}

func ExampleIndex_Upsert() {
	ix, err := autocomplete.New[Book]()
	if err != nil {
		log.Fatal(err)
	}
	if err := ix.Build(books); err != nil {
		log.Fatal(err)
	}
	if err := ix.Upsert(
		Book{ISBN: "9780141439518", Title: "Pride and Prejudice (Annotated)", Author: "Jane Austen"},
		Book{ISBN: "9780451524935", Title: "Nineteen Eighty-Four", Author: "George Orwell"},
	); err != nil {
		log.Fatal(err)
	}
	fmt.Println(ix.Len(), ix.Search("pride")[0].Item.Title, ix.Search("orwell")[0].ID)

	fmt.Println(ix.Remove("9780451524935", "unknown"), ix.Len())
	// Output:
	// 5 Pride and Prejudice (Annotated) 9780451524935
	// 1 4
}

func ExampleUpsertFunc() {
	ix, err := autocomplete.New[Book]()
	if err != nil {
		log.Fatal(err)
	}
	var (
		build  autocomplete.BuildFunc[Book]  = ix.Build
		upsert autocomplete.UpsertFunc[Book] = ix.Upsert
		remove autocomplete.RemoveFunc       = ix.Remove
	)
	if err := build(books); err != nil {
		log.Fatal(err)
	}
	if err := upsert(Book{ISBN: "1", Title: "Dune", Author: "Frank Herbert"}); err != nil {
		log.Fatal(err)
	}
	fmt.Println(ix.Len(), remove("1"), ix.Len())
	// Output: 5 1 4
}

func ExampleNormalize() {
	fmt.Println(autocomplete.Normalize("Țara Șoimilor"))
	// Output: tara soimilor
}
