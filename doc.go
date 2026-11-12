// Package autocomplete is an in-memory search-as-you-type index over the string fields of your own struct type.
//
// Tag the fields to search with searchindex:"true", optionally with a boost, and the id with searchindex:"id"
// (or pass WithID):
//
//	type Book struct {
//		ISBN   string `searchindex:"id"`
//		Title  string `searchindex:"true,boost=10"`
//		Author string `searchindex:"true"`
//	}
//
// Tagged fields are string or *string. New reads the tags once, and the search scan never uses reflection.
//
// # Matching
//
// Queries and fields are lowercased and stripped of diacritics, and every query word must prefix a word of the
// field. A match is ranked by its [Tier] first (exact, prefix, aligned, in order, any order), then by the field
// boost plus the item score from [WithScore], minus a penalty for extra words, then by id.
//
// # Writes
//
// [Index.Build] replaces the whole index, [Index.Upsert] inserts or replaces items by id and [Index.Remove]
// deletes them. Their method values are [BuildFunc], [UpsertFunc] and [RemoveFunc], so the code that feeds the
// index can get a plain func instead of the index.
//
// # Concurrency and memory
//
// Searches are lock-free: writers serialize on a mutex, build a new snapshot and swap it in atomically, so a
// search sees the index before or after a write, never a mix. Snapshots are made of chunks of 256 items, and a
// write copies only the chunks it touches. [WithMaxItems] and [WithMaxFieldBytes] bound what the index can hold.
package autocomplete
