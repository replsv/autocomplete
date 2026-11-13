package autocomplete

import "testing"

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"romanian diacritics", "Țara Șoimilor", "tara soimilor"},
		{"cedilla variants", "Ştefan", "stefan"},
		{"sharp s", "Straße", "strasse"},
		{"ligature", "Æon Flux", "aeon flux"},
		{"stroke l", "Łódź", "lodz"},
		{"punctuation", "Spider-Man: No Way Home", "spider man no way home"},
		{"accent", "Amélie", "amelie"},
		{"decomposed accent", "Amélie", "amelie"},
		{"latin extended additional", "Ấn Độ", "an do"},
		{"greek is not latin", "Ωmega", "mega"},
		{"digits", "2001: A Space Odyssey", "2001 a space odyssey"},
		{"whitespace only", " \t\n ", ""},
		{"symbols only", "---", ""},
		{"non latin", "映画", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eq(t, Normalize(tt.in), tt.want)
		})
	}
}

func TestNormalizeMapped(t *testing.T) {
	var src []int
	eq(t, string(appendNormalized(nil, "Straße", 0, &src)), "strasse")
	eq(t, src, []int{0, 1, 2, 3, 4, 4, 5})

	src = nil
	n := appendNormalized(nil, "  O luptă", 0, &src)
	eq(t, string(n), "o lupta")
	eq(t, src[:7], []int{2, 4, 4, 5, 6, 7, 8})
	eq(t, len(src), len(n))
}

func TestNormalizeLimit(t *testing.T) {
	tests := []struct {
		limit int
		want  string
	}{
		{0, "spider man no way home"},
		{10, "spider man"},
		{11, "spider man"},
		{12, "spider man n"},
		{1, "s"},
	}
	for _, tt := range tests {
		var src []int
		got := string(appendNormalized(nil, "Spider-Man: No Way Home", tt.limit, &src))
		eq(t, got, tt.want, tt.limit)
		eq(t, len(src), len(got), tt.limit)
	}

	eq(t, string(appendNormalized([]byte("x"), "Straße", 5, nil)), "xstras", "limit is relative to dst")
}
