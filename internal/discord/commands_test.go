package discord

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

func TestApplicationIDIsFetchedOnce(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/applications/@me" {
			http.NotFound(w, r)
			return
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"id":"123456789012345678"}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "token", "test")

	// A failed fetch is not cached; concurrent callers then share one fetch.
	if _, err := c.ApplicationID(context.Background()); err == nil {
		t.Fatal("ApplicationID() succeeded on HTTP 500")
	}
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			id, err := c.ApplicationID(context.Background())
			if err != nil || id != "123456789012345678" {
				t.Errorf("ApplicationID() = %q, %v", id, err)
			}
		})
	}
	wg.Wait()
	if n := calls.Load(); n != 2 {
		t.Errorf("GET /applications/@me was called %d times, want 2", n)
	}
}
