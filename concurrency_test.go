package autocomplete

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"
)

func TestConcurrentSearchDuringWrites(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	items := make([]movie, 2000)
	for i := range items {
		items[i] = randomMovie(r, i)
	}
	ix := testIndex(t, items)

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for g := range 4 {
		readers.Go(func() {
			queries := []string{"alpha", "lupta dupa", "night the", "soim", "beta gamma alpha"}
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				for _, res := range ix.Search(queries[(g+i)%len(queries)]) {
					if res.ID != res.Item.ID || res.Tier == 0 {
						t.Errorf("inconsistent result %+v", res)
						return
					}
				}
				_ = ix.Len()
			}
		})
	}

	var writers sync.WaitGroup
	for w := range 3 {
		writers.Go(func() {
			wr := rand.New(rand.NewPCG(uint64(w), 9))
			for i := range 60 {
				switch i % 3 {
				case 0:
					if err := ix.Upsert(randomMovie(wr, wr.IntN(2500)), randomMovie(wr, wr.IntN(2500))); err != nil {
						t.Error(err)
					}
				case 1:
					ix.Remove(fmt.Sprint(wr.IntN(2500)))
				default:
					if i%15 == 2 {
						if err := ix.Build(items[:1000+wr.IntN(1000)]); err != nil {
							t.Error(err)
						}
					}
				}
			}
		})
	}
	writers.Wait()
	close(stop)
	readers.Wait()
}
