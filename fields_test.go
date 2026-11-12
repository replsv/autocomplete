package autocomplete

import (
	"errors"
	"testing"
)

type Inner struct {
	Name string `searchindex:"true"`
}

type withEmbedded struct {
	ID string `searchindex:"id"`
	Inner
	Title string `searchindex:"true,boost=10"`
}

type withEmbeddedPtr struct {
	ID string `searchindex:"id"`
	*Inner
}

func TestNewTags(t *testing.T) {
	check := func(name string, err, want error) {
		t.Helper()
		if !errors.Is(err, want) {
			t.Errorf("%s: got %v, want %v", name, err, want)
		}
	}

	_, err := New[struct {
		ID    string `searchindex:"id"`
		Title string `searchindex:"true"`
		Note  string
		Skip  string `searchindex:"-"`
		Off   string `searchindex:"false"`
	}]()
	check("valid", err, nil)

	_, err = New[struct {
		ID   string `searchindex:"id"`
		Note string
	}]()
	check("no fields", err, ErrNoFields)

	_, err = New[struct {
		Title string `searchindex:"true"`
	}]()
	check("no id", err, ErrNoID)

	tagErrors := map[string]error{}
	_, tagErrors["int field"] = New[struct {
		ID   string `searchindex:"id"`
		Year int    `searchindex:"true"`
	}]()
	_, tagErrors["*int field"] = New[struct {
		ID   string `searchindex:"id"`
		Year *int   `searchindex:"true"`
	}]()
	_, tagErrors["[]string field"] = New[struct {
		ID   string   `searchindex:"id"`
		Tags []string `searchindex:"true"`
	}]()
	_, tagErrors["bad boost"] = New[struct {
		ID    string `searchindex:"id"`
		Title string `searchindex:"true,boost=lots"`
	}]()
	_, tagErrors["infinite boost"] = New[struct {
		ID    string `searchindex:"id"`
		Title string `searchindex:"true,boost=+Inf"`
	}]()
	_, tagErrors["unknown option"] = New[struct {
		ID    string `searchindex:"id"`
		Title string `searchindex:"true,weight=2"`
	}]()
	_, tagErrors["unknown value"] = New[struct {
		ID    string `searchindex:"id"`
		Title string `searchindex:"yes"`
	}]()
	_, tagErrors["duplicate id"] = New[struct {
		ID    string `searchindex:"id"`
		ID2   string `searchindex:"id"`
		Title string `searchindex:"true"`
	}]()
	_, tagErrors["id not a string"] = New[struct {
		ID    int    `searchindex:"id"`
		Title string `searchindex:"true"`
	}]()
	_, tagErrors["id with options"] = New[struct {
		ID    string `searchindex:"id,boost=1"`
		Title string `searchindex:"true"`
	}]()
	_, tagErrors["embedded pointer"] = New[withEmbeddedPtr]()
	for name, err := range tagErrors {
		check(name, err, ErrInvalidTag)
	}

	if _, err := New[int](); err == nil {
		t.Error("New[int]: no error")
	}
	if _, err := New[*int](); err == nil {
		t.Error("New[*int]: no error")
	}
}

func TestNewTagBoostAndOrder(t *testing.T) {
	ix, err := New[withEmbedded]()
	if err != nil {
		t.Fatal(err)
	}
	eq(t, ix.Fields(), []string{"Name", "Title"})
	eq(t, ix.fields[0].boost, 0.0)
	eq(t, ix.fields[1].boost, 10.0)

	if err := ix.Build([]withEmbedded{{ID: "1", Inner: Inner{Name: "Promoted Field"}, Title: "Own"}}); err != nil {
		t.Fatal(err)
	}
	res := ix.Search("promoted")
	eq(t, ids(res), []string{"1"})
	eq(t, res[0].Ranges("Name"), []Range{{0, 8}})
}

type title string

type namedStrings struct {
	ID  string `searchindex:"id"`
	T   title  `searchindex:"true"`
	Alt *title `searchindex:"true"`
}

func TestNamedStringTypes(t *testing.T) {
	ix, err := New[namedStrings]()
	if err != nil {
		t.Fatal(err)
	}
	alt := title("Second Name")
	if err := ix.Build([]namedStrings{{ID: "1", T: "First Name", Alt: &alt}}); err != nil {
		t.Fatal(err)
	}
	eq(t, ids(ix.Search("second")), []string{"1"})
}

func TestWithID(t *testing.T) {
	type noTag struct {
		Key   int
		Title string `searchindex:"true"`
	}
	ix, err := New[noTag](WithID(func(n noTag) string { return string(rune('a' + n.Key)) }))
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Build([]noTag{{0, "Same"}, {1, "Same"}}); err != nil {
		t.Fatal(err)
	}
	eq(t, ids(ix.Search("same")), []string{"a", "b"})

	ix2, err := New[movie](WithID(func(m movie) string { return "x" + m.ID }))
	if err != nil {
		t.Fatal(err)
	}
	if err := ix2.Build([]movie{{ID: "1", Title: "Override"}}); err != nil {
		t.Fatal(err)
	}
	eq(t, ids(ix2.Search("override")), []string{"x1"}, "WithID overrides the tag")

	_, err = New[noTag](WithID(func(m movie) string { return m.ID }))
	if !errors.Is(err, ErrInvalidOption) {
		t.Errorf("mistyped WithID: got %v", err)
	}
	_, err = New[movie](WithScore(func(n noTag) float64 { return 0 }))
	if !errors.Is(err, ErrInvalidOption) {
		t.Errorf("mistyped WithScore: got %v", err)
	}
}
