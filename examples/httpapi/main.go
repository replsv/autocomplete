// Command httpapi serves an autocomplete over HTTP:
//
//	GET    /search?q=...   matches as JSON
//	PUT    /items          upsert a JSON array of items
//	DELETE /items/{id}     remove one item
//
// The whole index is rebuilt from the -data file every -every. The rebuild and the item routes only see the
// exported build, update and remove funcs, not the index.
//
// A rebuild briefly holds the old and the new index, so give the process a soft memory limit with headroom for
// two copies; at about 60k items, 150MiB is plenty:
//
//	GOMEMLIMIT=150MiB go run ./examples/httpapi -data products.json -every 5m
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/replsv/autocomplete"
)

type Product struct {
	SKU   string `searchindex:"id" json:"sku"`
	Name  string `searchindex:"true,boost=20" json:"name"`
	Brand string `searchindex:"true" json:"brand"`
	Sales int    `json:"sales"`
}

type match struct {
	SKU        string               `json:"sku"`
	Name       string               `json:"name"`
	Brand      string               `json:"brand"`
	NameMatch  []autocomplete.Range `json:"nameMatch,omitempty"`
	BrandMatch []autocomplete.Range `json:"brandMatch,omitempty"`
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	data := flag.String("data", "", "JSON array of products; a small built-in catalog when empty")
	every := flag.Duration("every", 5*time.Minute, "rebuild interval")
	flag.Parse()

	ix, err := autocomplete.New[Product](
		autocomplete.WithScore(func(p Product) float64 { return float64(min(p.Sales, 1000)) / 10 }),
		autocomplete.WithMaxItems(1_000_000),
		autocomplete.WithLimit(10),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	load := func() ([]Product, error) { return loadProducts(*data) }
	if err := rebuild(load, ix.Build); err != nil {
		log.Fatal(err)
	}
	go rebuildEvery(ctx, *every, load, ix.Build)

	mux := http.NewServeMux()
	mux.Handle("GET /search", searchHandler(ix.Search))
	mux.Handle("PUT /items", upsertHandler(ix.Upsert))
	mux.Handle("DELETE /items/{id}", removeHandler(ix.Remove))

	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	log.Printf("listening on %s with %d products", *addr, ix.Len())
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func rebuild(load func() ([]Product, error), build autocomplete.BuildFunc[Product]) error {
	items, err := load()
	if err != nil {
		return err
	}
	return build(items)
}

// rebuildEvery replaces the index from load until ctx is done. A failed load keeps the current index.
func rebuildEvery(ctx context.Context, every time.Duration, load func() ([]Product, error), build autocomplete.BuildFunc[Product]) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := rebuild(load, build); err != nil {
				log.Printf("rebuild: %v", err)
			}
		}
	}
}

func searchHandler(search func(string) []autocomplete.Result[Product]) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items := []match{}
		for _, res := range search(r.URL.Query().Get("q")) {
			items = append(items, match{
				SKU: res.ID, Name: res.Item.Name, Brand: res.Item.Brand,
				NameMatch: res.Ranges("Name"), BrandMatch: res.Ranges("Brand"),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"count": len(items), "items": items})
	})
}

func upsertHandler(update autocomplete.UpsertFunc[Product]) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var items []Product
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&items); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := update(items...); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"upserted": len(items)})
	})
}

func removeHandler(remove autocomplete.RemoveFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if remove(r.PathValue("id")) == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func loadProducts(path string) ([]Product, error) {
	if path == "" {
		return []Product{
			{SKU: "kb-01", Name: "Mechanical Keyboard", Brand: "Keychron", Sales: 900},
			{SKU: "ms-01", Name: "Wireless Mouse", Brand: "Logitech", Sales: 1200},
			{SKU: "hp-01", Name: "Noise Cancelling Headphones", Brand: "Sony", Sales: 700},
			{SKU: "mn-01", Name: "Écran 27 pouces", Brand: "Dell", Sales: 300},
			{SKU: "kb-02", Name: "Low Profile Keyboard", Brand: "Logitech", Sales: 400},
		}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var items []Product
	return items, json.NewDecoder(f).Decode(&items)
}
