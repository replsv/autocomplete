# autocomplete

An in-memory search-as-you-type index for Go, over the string fields of your own struct type.

- Generic: `Index[T]` stores and returns your items; you pick the fields with a struct tag.
- Word-prefix matching, insensitive to case and diacritics (`"pădu inim"` finds `"Pădurea inimă"`).
- Ranked by match tier, then a per-field boost plus your own per-item score, with a deterministic tie break.
- Highlight ranges per field, as rune offsets into the original text.
- Lock-free searches; rebuilds and updates swap an immutable snapshot.
- Bounded memory: item count and field length limits, copy-on-write updates in 256-item chunks.
- One dependency: `golang.org/x/text`.

## Install

```sh
go get github.com/replsv/autocomplete
```

Go 1.27 or later.

## Quick start

```go
type Book struct {
	ISBN   string `searchindex:"id"`
	Title  string `searchindex:"true,boost=10"`
	Author string `searchindex:"true"`
	Year   int
}

ix, err := autocomplete.New[Book]()
if err != nil {
	log.Fatal(err)
}
if err := ix.Build(books); err != nil {
	log.Fatal(err)
}

for _, r := range ix.Search("great gat") {
	fmt.Println(r.ID, r.Item.Title, r.Tier, r.Score, r.Ranges("Title"))
}
// 9780743273565 The Great Gatsby 4 2 [{4 13}]
```

`Result[T]` holds the item, its id, the tier and score of its best field, and `Matches`, the merged half-open
rune ranges of every field that matched. `r.Ranges("Title")` picks one field by its Go name. `Range` has JSON
tags (`{"start":4,"end":13}`).

## Struct tags

| Tag | Meaning |
| --- | --- |
| `searchindex:"true"` | index this field; it must be a `string` or a `*string` (named string types work too) |
| `searchindex:"true,boost=25"` | index it and add 25 to the score when this field is the best match |
| `searchindex:"id"` | the item id; a `string` field, at most one |
| `searchindex:"-"` or `"false"` | not indexed (same as no tag) |

The tags are read once, by `New`. Any other tagged kind, an unknown option, a bad boost or a second id field is
an error wrapping `ErrInvalidTag`; no tagged field is `ErrNoFields`; no id is `ErrNoID`. Fields promoted from
embedded structs are supported, fields promoted through an embedded pointer are not. `T` may be a struct or a
pointer to a struct; a nil `*string` (or a nil item) indexes as an empty field.

Indexing reads fields through offsets computed at construction, and searching never touches reflection.

## Matching and ranking

Queries and fields are normalized the same way: lowercase ASCII words, diacritics removed (`ß` → `ss`, `æ` →
`ae`, `ł` → `l`, ...), anything that is not a letter or digit separates words. Every query word must prefix a
word of the field. The tiers, best first:

| Tier | Example: field `The Dark Knight` |
| --- | --- |
| `TierExact` | `the dark knight`, `dark knight` (a leading article is skipped) |
| `TierPrefix` | `dark kn` |
| `TierAligned` | `da kni` (query word i prefixes field word i) |
| `TierInOrder` | `the knight` |
| `TierAnyOrder` | `knight dark` |

A 64-bit bloom filter on the first letters and letter pairs of each field's words rejects most items before any
string is compared.

The tier always wins. Within a tier, the score is `boost + Score(item) - min(80, 8 × extra words)`, then the
item score, then the id, so results are deterministic. `Result.Score` reports that score without the tier.

## Options

| Option | Default | Bounds |
| --- | --- | --- |
| `WithLimit(n)` | 8 results | ≥ 1 |
| `WithMinQueryLen(n)` | 2 bytes of normalized query | ≥ 1 |
| `WithMaxQueryRunes(n)` | 60 runes, the rest is ignored | ≥ 1 |
| `WithMaxTokens(n)` | 6 query words | 1..64 |
| `WithArticles(a...)` | `"the", "a", "an"` | none to disable |
| `WithID(func(T) string)` | the `searchindex:"id"` field | overrides the tag |
| `WithScore(func(T) float64)` | 0 | computed once per item, when it is indexed |
| `WithMaxItems(n)` | 0, no bound | 0..2³¹-1 |
| `WithMaxFieldBytes(n)` | 255 normalized bytes per field | 1..65535 |
| `WithCapacity(n)` | 0 | presizes the id map and chunk list for `Build` |

`WithID` and `WithScore` are generic, so the item type is inferred from the func; a func of the wrong type is an
`ErrInvalidOption` from `New`, like any value out of bounds.

### Ranking by votes and recency

Popular and recent titles first, with a +25 bonus for the primary title over the alternative one:

```go
type Movie struct {
	IMDbID   string  `searchindex:"id"`
	Title    string  `searchindex:"true,boost=25"`
	AltTitle *string `searchindex:"true"`
	Year     int
	Votes    int
}

now := time.Now().Year()
ix, err := autocomplete.New[Movie](
	autocomplete.WithScore(func(m Movie) float64 {
		return 60*math.Log10(1+float64(max(m.Votes, 0))) + 4*float64(min(10, max(0, 10-(now-m.Year))))
	}),
	autocomplete.WithArticles("the", "a", "an", "o", "un"),
)
```

Ties within a tier and score are broken on the item score, then the id. See [examples/movies](examples/movies).

## Create and update funcs

| Method | Func type | Does |
| --- | --- | --- |
| `Build(items []T) error` | `BuildFunc[T]` | replaces the whole index; for duplicate ids the last item wins |
| `BuildSeq(items iter.Seq[T]) error` | | the same, streaming from the source without holding all items twice |
| `Upsert(items ...T) error` | `UpsertFunc[T]` | inserts, or replaces the items with the same id |
| `Remove(ids ...string) int` | `RemoveFunc` | removes by id, returns how many were indexed |

Method values are plain funcs, so the index never has to be shared with the code that feeds it:

```go
var (
	build  autocomplete.BuildFunc[Product]  = ix.Build
	update autocomplete.UpsertFunc[Product] = ix.Upsert
	remove autocomplete.RemoveFunc          = ix.Remove
)

go rebuildEvery(ctx, 5*time.Minute, loadFromDB, build) // cron
sub.OnMessage(func(p Product) { update(p) })          // pub/sub
mux.Handle("DELETE /items/{id}", removeHandler(remove)) // admin route
```

Writes are all-or-nothing: an empty id (`ErrEmptyID`) or a write past `WithMaxItems` (`ErrTooManyItems`)
leaves the current index serving, unchanged. [examples/httpapi](examples/httpapi) wires all three into a
`net/http` server with a periodic rebuild.

## Concurrency

`Search` and `Len` load the current snapshot with an `atomic.Pointer` and never block. `Build`, `BuildSeq`,
`Upsert` and `Remove` serialize on a mutex, build a new snapshot on the side and swap it in; a search sees either
the old snapshot or the new one, never a mix. The tests run 4 readers against 3 writers under `-race`.

Items are stored by value. Don't mutate what a `*string` field (or a pointer item) points to after indexing: the
index keeps matching the indexed text, and drops the highlight ranges of a field whose text changed.

## Memory model

A snapshot is a list of chunks of 256 entries. Each chunk holds:

- the items (`T` by value), their ids (sharing the item's bytes when the id is a tagged field) and item scores;
- one 16-byte bloom filter per field, contiguous so the scan stays in cache;
- the normalized fields, packed back to back in one string per chunk, with an 8-byte span per field.

Word lists and offsets are not stored, words are found by scanning the normalized text. The original text is
not copied: highlights re-normalize it from the item. An id → position map is kept for the writers only.

With the benchmark item (two indexed fields, 56-byte struct) that is about **200-225 bytes per item** on top
of the item's own strings (`heap-B/item` below).

**Updates** copy the chunk list (8 bytes per 256 items) and each chunk they touch, once per call. A single
upsert at 60k items allocates about 60 KiB instead of copying the whole index. A batch of 100 random ids touches up to 100 chunks (5.6 MiB at 60k); ids that are close together
share chunks. A removal moves the last item into the hole so every chunk but the last stays full.

**Builds** hold the old snapshot until the new one is swapped in, so the peak is about two snapshots plus your
items (`BuildSeq` avoids holding the items separately). `Build` allocates about as much as it retains (see B/op),
the draft buffers are reused from chunk to chunk.

Measured with the benchmark data, sampling `/memory/classes/heap/objects:bytes` during a second `Build`:

| Items | Items themselves | One snapshot | Peak during a rebuild (excluding the items) |
| --- | --- | --- | --- |
| 60k | 7.3 MiB | 12.8 MiB | 25.3 MiB |
| 200k | 24.0 MiB | 38.1 MiB | 75.9 MiB |

Set `GOMEMLIMIT` to leave room for two snapshots plus the rest of the process; at 60k items, 150MiB is plenty.
Use `WithMaxItems` and `WithMaxFieldBytes` to bound what a bad feed can do.

## Benchmarks

`go test -bench . -benchmem -count=5 -run '^$'`, medians from `benchstat`. Raw output:
[bench/results.txt](bench/results.txt).

- CPU: AMD Ryzen 7 PRO 7840U w/ Radeon 780M Graphics (a laptop shared with a desktop session)
- Go 1.27.3 linux/amd64, `GOMAXPROCS=4`, `GOMEMLIMIT=1GiB`, `GOFLAGS=-p=2`, in a 2 GiB `systemd-run` scope

The data is generated in `bench_test.go` with a seeded `math/rand/v2`: titles of one to five words drawn with a
Zipf distribution from 56 real words (ASCII and diacritics) plus 6000 pseudo-words, a quarter with a leading
article, half with an alternative title. Common words make `st` match about a quarter of the index, a worst
case for a two-letter prefix.

| Search | 10k | 60k | 200k | allocs/op |
| --- | --- | --- | --- | --- |
| `st` (short prefix) | 170 µs | 596 µs | 3.2 ms | ~70 |
| `dark night` (multi-word) | 52 µs | 222 µs | 793 µs | ~74 |
| `night dark star` (any order) | 46 µs | 205 µs | 609 µs | 74-94 |
| `pădu inim` (diacritics) | 24 µs | 143 µs | 486 µs | 37-87 |
| `zzqx` (no match) | 16 µs | 99 µs | 355 µs | 6 |

A search allocates 0.5-7 KiB: the normalized query and the results with their highlight ranges.

| Build | time | B/op | allocs/op | retained heap |
| --- | --- | --- | --- | --- |
| 10k | 5.0 ms | 2.1 MiB | 306 | 210 B/item |
| 60k | 29.4 ms | 12.8 MiB | 1700 | 223 B/item |
| 200k | 108 ms | 38.2 MiB | 5238 | 200 B/item |

| Update at 60k | time | B/op | allocs/op |
| --- | --- | --- | --- |
| `Upsert` one item | 41 µs | 59.4 KiB | 12 |
| `Upsert` 100 random items | 4.0 ms | 5.6 MiB | 816 |
| `Upsert` a new item, then `Remove` it | 26 µs | 52.8 KiB | 25 |

Search time grows linearly with the index (the scan is a full pass over the bloom filters), so a two-letter
prefix over 200k items costs a few milliseconds. Once the top results are full, an item whose best possible
score cannot beat the last one is only checked for a better tier, which keeps popular prefixes cheap.

## Examples

- [examples/basic](examples/basic): tags, build, search and printed highlights.
- [examples/movies](examples/movies): a votes and recency ranking over movie titles.
- [examples/httpapi](examples/httpapi): a JSON search endpoint, periodic rebuild through `BuildFunc`, upsert and
  remove routes through `UpsertFunc` and `RemoveFunc`.

```sh
go run ./examples/basic
GOMEMLIMIT=150MiB go run ./examples/httpapi -addr 127.0.0.1:8080
curl '127.0.0.1:8080/search?q=keyb'
```

## License

MIT, see [LICENSE](LICENSE).
