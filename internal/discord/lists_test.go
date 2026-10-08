package discord

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"testing"
)

// pagedUsers serves n items with user IDs 1..n, honoring limit and after,
// and records the query of every request.
func pagedUsers(t *testing.T, n int, item func(id string) any) (*Client, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var queries []string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.RawQuery)
		mu.Unlock()
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		after, _ := strconv.Atoi(r.URL.Query().Get("after"))
		page := []any{}
		for id := after + 1; id <= n && len(page) < limit; id++ {
			page = append(page, item(strconv.Itoa(id)))
		}
		_ = json.NewEncoder(w).Encode(page)
	})
	return c, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(queries)
	}
}

func TestListMembersPagination(t *testing.T) {
	member := func(id string) any { return Member{User: &User{ID: id}} }
	tests := []struct {
		name     string
		members  int
		maxItems int
		want     int
		queries  []string
	}{
		{"empty", 0, 0, 0, []string{"limit=1000&after=0"}},
		{"one short page", 3, 0, 3, []string{"limit=1000&after=0"}},
		{"full page then empty", 1000, 0, 1000, []string{"limit=1000&after=0", "limit=1000&after=1000"}},
		{"two pages", 1500, 0, 1500, []string{"limit=1000&after=0", "limit=1000&after=1000"}},
		{"limit within a page", 1500, 10, 10, []string{"limit=10&after=0"}},
		{"limit of a full page", 1500, 1000, 1000, []string{"limit=1000&after=0"}},
		{"limit across pages", 2500, 1200, 1200, []string{"limit=1000&after=0", "limit=200&after=1000"}},
		{"limit above total", 5, 2000, 5, []string{"limit=1000&after=0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, queries := pagedUsers(t, tt.members, member)
			got, err := c.ListMembers(context.Background(), "1", tt.maxItems)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tt.want {
				t.Errorf("got %d members, want %d", len(got), tt.want)
			}
			if got == nil {
				t.Error("got nil, want an empty slice")
			}
			if q := queries(); !slices.Equal(q, tt.queries) {
				t.Errorf("queries = %v, want %v", q, tt.queries)
			}
		})
	}
}

func TestListBansPagination(t *testing.T) {
	c, queries := pagedUsers(t, 1001, func(id string) any { return Ban{User: &User{ID: id}} })
	got, err := c.ListBans(context.Background(), "1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1001 || got[1000].User.ID != "1001" {
		t.Errorf("got %d bans", len(got))
	}
	if want := []string{"limit=1000&after=0", "limit=1000&after=1000"}; !slices.Equal(queries(), want) {
		t.Errorf("queries = %v, want %v", queries(), want)
	}
}

func TestListPaginationError(t *testing.T) {
	calls := 0
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			page := make([]Member, MaxPageSize)
			for i := range page {
				page[i] = Member{User: &User{ID: strconv.Itoa(i + 1)}}
			}
			_ = json.NewEncoder(w).Encode(page)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":50001,"message":"Missing Access"}`))
	})
	if _, err := c.ListMembers(context.Background(), "1", 0); err == nil {
		t.Fatal("want an error from the second page")
	}
}

func TestListActiveThreadsUnwrapsResponse(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"threads":[{"id":"2","name":"b"},{"id":"1","name":"a"}],"members":[]}`))
	})
	threads, err := c.ListActiveThreads(context.Background(), "1")
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 2 || threads[0].ID != "2" {
		t.Errorf("threads = %+v", threads)
	}
}

func TestListArchivedThreadsPagination(t *testing.T) {
	var queries []string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Path+"?"+r.URL.RawQuery)
		switch r.URL.Query().Get("before") {
		case "":
			_, _ = w.Write([]byte(`{"threads":[{"id":"3","thread_metadata":{"archived":true,"archive_timestamp":"2026-03-01T00:00:00Z"}},` +
				`{"id":"2","thread_metadata":{"archived":true,"archive_timestamp":"2026-02-01T00:00:00Z"}}],"members":[],"has_more":true}`))
		default:
			_, _ = w.Write([]byte(`{"threads":[{"id":"1","thread_metadata":{"archived":true,"archive_timestamp":"2026-01-01T00:00:00Z"}}],"members":[],"has_more":false}`))
		}
	})
	threads, err := c.ListArchivedThreads(context.Background(), "9", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 3 || threads[2].ID != "1" {
		t.Errorf("threads = %+v", threads)
	}
	want := []string{
		"/channels/9/threads/archived/private?limit=100",
		"/channels/9/threads/archived/private?limit=100&before=2026-02-01T00%3A00%3A00Z",
	}
	if !slices.Equal(queries, want) {
		t.Errorf("queries = %v, want %v", queries, want)
	}
}

func TestListArchivedThreadsEmptyPage(t *testing.T) {
	calls := 0
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"threads":[],"members":[],"has_more":true}`))
	})
	threads, err := c.ListArchivedThreads(context.Background(), "9", false)
	if err != nil {
		t.Fatal(err)
	}
	if threads == nil || len(threads) != 0 || calls != 1 {
		t.Errorf("threads = %v after %d calls, want one call and an empty slice", threads, calls)
	}
}

func TestListActiveThreadsError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":10004,"message":"Unknown Guild"}`))
	})
	if _, err := c.ListActiveThreads(context.Background(), "1"); !IsNotFound(err) {
		t.Errorf("err = %v, want not found", err)
	}
}
