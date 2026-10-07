package discord

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNormalizeToken(t *testing.T) {
	for in, want := range map[string]string{
		"abc":          "abc",
		"Bot abc":      "abc",
		"bot abc":      "abc",
		"  Bot  abc  ": "abc",
		"Bot":          "Bot",
		"":             "",
		"Bothersome":   "Bothersome",
	} {
		if got := NormalizeToken(in); got != want {
			t.Errorf("NormalizeToken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRouteKey(t *testing.T) {
	tests := []struct{ method, path, want string }{
		{"GET", "/channels/123456789012345678/messages/223456789012345678", "GET /channels/123456789012345678/messages/:id"},
		{"GET", "/channels/123456789012345678/messages/323456789012345678", "GET /channels/123456789012345678/messages/:id"},
		{"PATCH", "/guilds/123456789012345678/roles", "PATCH /guilds/123456789012345678/roles"},
		{"GET", "/guilds/1234567/members/search?limit=1000&query=a", "GET /guilds/1234567/members/search"},
	}
	for _, tt := range tests {
		if got := routeKey(tt.method, tt.path); got != tt.want {
			t.Errorf("routeKey(%q, %q) = %q, want %q", tt.method, tt.path, got, tt.want)
		}
	}
}

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, "Bot secret", "test")
}

func TestClientSendsAuthAndBody(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bot secret" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("User-Agent"); !strings.HasPrefix(got, "DiscordBot (") {
			t.Errorf("User-Agent = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		_, _ = w.Write([]byte(`{"id":"1","name":"r","permissions":"8"}`))
	})
	role, err := c.CreateRole(context.Background(), "1", Payload{"name": "r", "unicode_emoji": nil})
	if err != nil {
		t.Fatal(err)
	}
	if role.Permissions != "8" {
		t.Errorf("permissions = %q", role.Permissions)
	}
}

func TestClientRetriesRateLimit(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message":"rate limited","retry_after":0.01,"global":true}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"1"}`))
	})
	if _, err := c.GetGuild(context.Background(), "1"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3", calls.Load())
	}
}

func TestClientRetryAfterHeaderFallback(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0.01")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	if _, err := c.GetGuild(context.Background(), "1"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
}

func TestClientGivesUpAfterMaxAttempts(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"rate limited","retry_after":0.001}`))
	})
	_, err := c.GetGuild(context.Background(), "1")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusTooManyRequests {
		t.Fatalf("err = %v, want 429 APIError", err)
	}
	if calls.Load() != maxAttempts {
		t.Errorf("calls = %d, want %d", calls.Load(), maxAttempts)
	}
}

func TestClientRetriesGatewayErrors(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	if _, err := c.GetGuild(context.Background(), "1"); err != nil {
		t.Fatal(err)
	}
}

func TestClientWaitsForExhaustedBucket(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset-After", "0.05")
		_, _ = w.Write([]byte(`{}`))
	})
	ctx := context.Background()
	if _, err := c.GetGuild(ctx, "1"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := c.GetGuild(ctx, "1"); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Errorf("second request did not wait for the bucket reset (%s)", elapsed)
	}
}

func TestClientHonorsContextCancellation(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"retry_after":60}`))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.GetGuild(ctx, "1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}

func TestAPIError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Unknown Channel","code":10003}`))
	})
	_, err := c.GetChannel(context.Background(), "1")
	if !IsNotFound(err) {
		t.Fatalf("IsNotFound(%v) = false", err)
	}
	if !strings.Contains(err.Error(), "Unknown Channel (code 10003)") {
		t.Errorf("error = %q", err)
	}
	if IsNotFound(errors.New("other")) {
		t.Error("IsNotFound(non-API error) = true")
	}
}

func TestAPIErrorWithFormErrors(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"Invalid Form Body","code":50035,"errors":{"name":{}}}`))
	})
	_, err := c.ModifyGuild(context.Background(), "1", Payload{"name": ""})
	if err == nil || !strings.Contains(err.Error(), `{"name":{}}`) || IsNotFound(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestClientMalformedResponse(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	})
	if _, err := c.GetGuild(context.Background(), "1"); err == nil || !strings.Contains(err.Error(), "decoding response") {
		t.Fatalf("err = %v", err)
	}
}

func TestClientNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	c := NewClient(srv.URL, "x", "test")
	if _, err := c.GetGuild(context.Background(), "1"); err == nil {
		t.Fatal("expected error from closed server")
	}
}

func TestPermissionBits(t *testing.T) {
	got, err := PermissionBits([]string{"view_channel", " SEND_MESSAGES ", "ADMINISTRATOR"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "3080" {
		t.Errorf("PermissionBits = %q, want 3080", got)
	}
	if got, _ := PermissionBits(nil); got != "0" {
		t.Errorf("PermissionBits(nil) = %q, want 0", got)
	}
	if _, err := PermissionBits([]string{"NOT_A_PERMISSION"}); err == nil {
		t.Error("expected error for unknown permission")
	}
	if got, _ := PermissionBits([]string{"BYPASS_SLOWMODE"}); got != "4503599627370496" {
		t.Errorf("BYPASS_SLOWMODE = %s", got)
	}
}

func TestPermissionNamesFromBits(t *testing.T) {
	names, err := PermissionNamesFromBits("3080")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ADMINISTRATOR", "SEND_MESSAGES", "VIEW_CHANNEL"}; !slices.Equal(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}
	if _, err := PermissionNamesFromBits("abc"); err == nil {
		t.Error("expected error for invalid bitfield")
	}
}

func TestPermissionBitsAreUnique(t *testing.T) {
	seen := map[uint64]string{}
	for name, bit := range Permissions {
		if bit == 0 || bit&(bit-1) != 0 {
			t.Errorf("%s is not a single bit", name)
		}
		if other, ok := seen[bit]; ok {
			t.Errorf("%s and %s share bit %d", name, other, bit)
		}
		seen[bit] = name
	}
}

func TestClientDoesNotRetryPostOnGatewayError(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	})
	if _, err := c.CreateMessage(context.Background(), "1", Payload{"content": "hi"}); err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Errorf("POST was sent %d times, want 1", calls.Load())
	}
}
