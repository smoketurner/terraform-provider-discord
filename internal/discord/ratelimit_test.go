package discord

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// limiter is a test server that enforces fixed-window rate limits the way
// Discord describes them: per X-RateLimit-Bucket and top-level resource. It
// answers 429 to a request sent while its bucket is exhausted.
type limiter struct {
	limit  int
	window time.Duration
	// bucket returns the X-RateLimit-Bucket for a path with its top-level
	// resource removed.
	bucket func(rest string) string

	mu      sync.Mutex
	windows map[string]*limitWindow
	served  int
	tooMany int
}

type limitWindow struct {
	count int
	end   time.Time
}

func newLimiter(t *testing.T, limit int, window time.Duration, bucket func(string) string) (*limiter, *Client) {
	t.Helper()
	l := &limiter{limit: limit, window: window, bucket: bucket, windows: map[string]*limitWindow{}}
	srv := httptest.NewServer(l)
	t.Cleanup(srv.Close)
	return l, NewClient(srv.URL, "Bot secret", "test")
}

// headerSeconds formats a duration the way Discord does, rounded up to the
// millisecond so a client never retries before the window ends.
func headerSeconds(d time.Duration) string {
	return fmt.Sprintf("%.3f", math.Ceil(float64(d)/float64(time.Millisecond))/1000)
}

func (l *limiter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(r.URL.Path, "/", 4)
	major, rest := strings.Join(parts[:3], "/"), ""
	if len(parts) == 4 {
		rest = parts[3]
	}
	hash := l.bucket(rest)

	l.mu.Lock()
	now := time.Now()
	win := l.windows[hash+major]
	if win == nil || !now.Before(win.end) {
		win = &limitWindow{end: now.Add(l.window)}
		l.windows[hash+major] = win
	}
	exhausted := win.count >= l.limit
	if exhausted {
		l.tooMany++
	} else {
		win.count++
		l.served++
	}
	remaining, resetAfter := l.limit-win.count, win.end.Sub(now)
	l.mu.Unlock()

	w.Header().Set("X-RateLimit-Bucket", hash)
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(l.limit))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	w.Header().Set("X-RateLimit-Reset-After", headerSeconds(resetAfter))
	if exhausted {
		w.Header().Set("X-RateLimit-Scope", "user")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = fmt.Fprintf(w, `{"message":"You are being rate limited.","retry_after":%s,"global":false}`, headerSeconds(resetAfter))
		return
	}
	_, _ = w.Write([]byte(`{}`))
}

func (l *limiter) counts() (served, tooMany int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.served, l.tooMany
}

func get(t *testing.T, c *Client, path string) {
	t.Helper()
	if err := c.do(context.Background(), http.MethodGet, path, nil, nil); err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
}

func sameBucket(string) string { return "shared" }

func TestRateLimitSharedBucketAcrossRoutes(t *testing.T) {
	l, c := newLimiter(t, 2, 200*time.Millisecond, sameBucket)
	start := time.Now()
	get(t, c, "/guilds/1/roles")
	get(t, c, "/guilds/1/channels")
	get(t, c, "/guilds/1/roles")
	if _, tooMany := l.counts(); tooMany != 0 {
		t.Errorf("got %d 429s; routes sharing a bucket should wait for its reset", tooMany)
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Errorf("third request did not wait for the shared bucket to reset (%s)", elapsed)
	}
}

func TestRateLimitMajorParametersAreSeparate(t *testing.T) {
	l, c := newLimiter(t, 1, 300*time.Millisecond, sameBucket)
	start := time.Now()
	get(t, c, "/guilds/1/roles")
	get(t, c, "/guilds/2/roles")
	get(t, c, "/channels/1/messages")
	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Errorf("requests for different top-level resources waited on each other (%s)", elapsed)
	}
	get(t, c, "/guilds/1/roles")
	if elapsed := time.Since(start); elapsed < 250*time.Millisecond {
		t.Errorf("request did not wait for guild 1's bucket to reset (%s)", elapsed)
	}
	if _, tooMany := l.counts(); tooMany != 0 {
		t.Errorf("got %d 429s, want 0", tooMany)
	}
}

func TestRateLimitSeparateBuckets(t *testing.T) {
	l, c := newLimiter(t, 1, time.Second, func(rest string) string { return rest })
	start := time.Now()
	get(t, c, "/guilds/1/roles")
	get(t, c, "/guilds/1/channels")
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("routes in different buckets waited on each other (%s)", elapsed)
	}
	if _, tooMany := l.counts(); tooMany != 0 {
		t.Errorf("got %d 429s, want 0", tooMany)
	}
}

// TestRateLimitConcurrentDiscovery sends requests on several routes at once
// before the client knows they share a bucket, so the mapping is learned
// while other requests are waiting in the routes' provisional buckets.
func TestRateLimitConcurrentDiscovery(t *testing.T) {
	routes := []string{"/guilds/1/roles", "/guilds/1/channels", "/guilds/1/members", "/guilds/2/roles"}
	l, c := newLimiter(t, 3, 50*time.Millisecond, sameBucket)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const n = 40
	var (
		wg     sync.WaitGroup
		failed atomic.Int32
	)
	for i := range n {
		wg.Go(func() {
			if err := c.do(ctx, http.MethodGet, routes[i%len(routes)], nil, nil); err != nil {
				failed.Add(1)
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if served, _ := l.counts(); served != n-int(failed.Load()) || failed.Load() != 0 {
		t.Errorf("served %d of %d requests", served, n)
	}

	c.mu.Lock()
	if len(c.routes) != 3 {
		t.Errorf("learned %d routes, want 3: %v", len(c.routes), c.routes)
	}
	for route, hash := range c.routes {
		if hash != "shared" {
			t.Errorf("route %s mapped to %q", route, hash)
		}
	}
	c.mu.Unlock()

	assertBucketsDropped(t, c)
}

// TestRateLimitWaiterMovesToLearnedBucket queues a request behind the first
// request on a route; the first response maps the route to an exhausted
// bucket, so the queued request must wait for that bucket's reset rather than
// go out on the route's provisional bucket.
func TestRateLimitWaiterMovesToLearnedBucket(t *testing.T) {
	l := &limiter{limit: 1, window: 300 * time.Millisecond, bucket: sameBucket, windows: map[string]*limitWindow{}}
	arrived, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(arrived)
			<-release
		}
		l.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "Bot secret", "test")

	first := make(chan error)
	go func() { first <- c.do(context.Background(), http.MethodGet, "/guilds/1/roles", nil, nil) }()
	<-arrived
	second := make(chan error)
	go func() { second <- c.do(context.Background(), http.MethodGet, "/guilds/1/roles", nil, nil) }()
	for {
		c.mu.Lock()
		refs := c.buckets["GET /guilds/:major/roles /guilds/1"].refs
		c.mu.Unlock()
		if refs == 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 250*time.Millisecond {
		t.Errorf("queued request did not wait for the learned bucket to reset (%s)", elapsed)
	}
	if _, tooMany := l.counts(); tooMany != 0 {
		t.Errorf("got %d 429s, want 0", tooMany)
	}
}

// assertBucketsDropped checks that the client forgets buckets once nothing
// uses them and their limits have reset.
func assertBucketsDropped(t *testing.T, c *Client) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.mu.Lock()
		left := len(c.buckets)
		c.mu.Unlock()
		if left == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("client still holds %d idle buckets", left)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRateLimitBucketsDroppedAfterReset(t *testing.T) {
	_, c := newLimiter(t, 1, 100*time.Millisecond, sameBucket)
	for i := range 20 {
		get(t, c, fmt.Sprintf("/channels/%d/messages", i))
	}
	c.mu.Lock()
	if len(c.buckets) == 0 {
		t.Error("exhausted buckets were dropped before they reset")
	}
	c.mu.Unlock()
	assertBucketsDropped(t, c)
}

func TestRateLimitScopes(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		body    string
		// global reports whether the 429 should block other buckets too.
		global bool
	}{
		{"global header", map[string]string{"X-RateLimit-Global": "true", "X-RateLimit-Scope": "global"}, `{"retry_after":60,"global":true}`, true},
		{"global scope only", map[string]string{"X-RateLimit-Scope": "global"}, `{"retry_after":60}`, true},
		{"user", map[string]string{"X-RateLimit-Bucket": "a", "X-RateLimit-Remaining": "0", "X-RateLimit-Reset-After": "60", "X-RateLimit-Scope": "user"}, `{"retry_after":60,"global":false}`, false},
		{"shared with requests remaining", map[string]string{"X-RateLimit-Bucket": "a", "X-RateLimit-Remaining": "9", "X-RateLimit-Reset-After": "1", "X-RateLimit-Scope": "shared"}, `{"retry_after":60,"global":false}`, false},
		{"no headers", nil, `{"retry_after":60}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				mu   sync.Mutex
				hits = map[string]int{}
			)
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				hits[r.URL.Path]++
				mu.Unlock()
				if r.URL.Path == "/guilds/1/roles" {
					for k, v := range tt.headers {
						w.Header().Set(k, v)
					}
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = w.Write([]byte(tt.body))
					return
				}
				_, _ = w.Write([]byte(`{}`))
			})
			short := func(path string) error {
				ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
				defer cancel()
				return c.do(ctx, http.MethodGet, path, nil, nil)
			}

			if err := short("/guilds/1/roles"); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("rate limited request: err = %v, want deadline exceeded while waiting", err)
			}
			if err := short("/guilds/1/roles"); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("second request: err = %v, want deadline exceeded while waiting", err)
			}
			err := short("/channels/2")
			if tt.global && !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("other bucket: err = %v, want it to wait for the global limit", err)
			}
			if !tt.global && err != nil {
				t.Errorf("other bucket: err = %v, want it unaffected", err)
			}

			mu.Lock()
			defer mu.Unlock()
			if hits["/guilds/1/roles"] != 1 {
				t.Errorf("rate limited route was sent %d times before the wait ended, want 1", hits["/guilds/1/roles"])
			}
			if want := map[bool]int{true: 0, false: 1}[tt.global]; hits["/channels/2"] != want {
				t.Errorf("other route was sent %d times, want %d", hits["/channels/2"], want)
			}
		})
	}
}

func TestRateLimitWaiterHonorsContext(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		<-release
		_, _ = w.Write([]byte(`{}`))
	})
	done := make(chan error)
	go func() { done <- c.do(context.Background(), http.MethodGet, "/guilds/1/roles", nil, nil) }()
	for calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.do(ctx, http.MethodGet, "/guilds/1/roles", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want deadline exceeded while waiting for the bucket", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", calls.Load())
	}
	assertBucketsDropped(t, c)
}

func TestRateLimitRouteChangesBucket(t *testing.T) {
	var hash atomic.Pointer[string]
	one, two := "one", "two"
	hash.Store(&one)
	l, c := newLimiter(t, 1, 200*time.Millisecond, func(string) string { return *hash.Load() })
	get(t, c, "/guilds/1/roles")
	hash.Store(&two)
	start := time.Now()
	get(t, c, "/guilds/1/roles")
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Errorf("request did not wait for bucket one to reset (%s)", elapsed)
	}
	c.mu.Lock()
	if got := c.routes["GET /guilds/:major/roles"]; got != "two" {
		t.Errorf("route maps to %q, want two", got)
	}
	c.mu.Unlock()
	if _, tooMany := l.counts(); tooMany != 0 {
		t.Errorf("got %d 429s, want 0", tooMany)
	}
}

func TestResetTime(t *testing.T) {
	now := time.Unix(1470173000, 0)
	tests := []struct {
		name    string
		headers map[string]string
		want    time.Time
	}{
		{"reset after with decimals", map[string]string{"X-RateLimit-Reset-After": "64.57"}, now.Add(64570 * time.Millisecond)},
		{"reset after wins over reset", map[string]string{"X-RateLimit-Reset-After": "1", "X-RateLimit-Reset": "1470173023.123"}, now.Add(time.Second)},
		{"reset timestamp", map[string]string{"X-RateLimit-Reset": "1470173023.123"}, time.UnixMilli(1470173023123)},
		{"malformed", map[string]string{"X-RateLimit-Reset-After": "soon", "X-RateLimit-Reset": "later"}, time.Time{}},
		{"missing", nil, time.Time{}},
	}
	for _, tt := range tests {
		h := http.Header{}
		for k, v := range tt.headers {
			h.Set(k, v)
		}
		if got := resetTime(h, now); !got.Equal(tt.want) {
			t.Errorf("%s: resetTime = %s, want %s", tt.name, got, tt.want)
		}
	}
}

func TestRetryAfter(t *testing.T) {
	tests := []struct {
		name       string
		headers    map[string]string
		body       string
		wantWait   time.Duration
		wantGlobal bool
	}{
		{"body", nil, `{"retry_after":64.57,"global":false}`, 64570 * time.Millisecond, false},
		{"body over header", map[string]string{"Retry-After": "65"}, `{"retry_after":1.5}`, 1500 * time.Millisecond, false},
		{"header fallback", map[string]string{"Retry-After": "65"}, ``, 65 * time.Second, false},
		{"missing", nil, `not json`, time.Second, false},
		{"global body", nil, `{"retry_after":1,"global":true}`, time.Second, true},
		{"global header", map[string]string{"X-RateLimit-Global": "true"}, `{"retry_after":1}`, time.Second, true},
		{"global scope", map[string]string{"X-RateLimit-Scope": "global"}, `{"retry_after":1}`, time.Second, true},
		{"shared scope", map[string]string{"X-RateLimit-Scope": "shared"}, `{"retry_after":1336.57}`, 1336570 * time.Millisecond, false},
	}
	for _, tt := range tests {
		h := http.Header{}
		for k, v := range tt.headers {
			h.Set(k, v)
		}
		wait, global := retryAfter(h, []byte(tt.body))
		if wait != tt.wantWait || global != tt.wantGlobal {
			t.Errorf("%s: retryAfter = %s, %t; want %s, %t", tt.name, wait, global, tt.wantWait, tt.wantGlobal)
		}
	}
}
