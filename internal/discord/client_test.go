package discord

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
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

func TestParseRoute(t *testing.T) {
	tests := []struct{ method, path, route, major string }{
		{"GET", "/channels/123456789012345678/messages/223456789012345678", "GET /channels/:major/messages/:id", "/channels/123456789012345678"},
		{"GET", "/channels/923456789012345678/messages/323456789012345678", "GET /channels/:major/messages/:id", "/channels/923456789012345678"},
		{"PATCH", "/guilds/1/roles", "PATCH /guilds/:major/roles", "/guilds/1"},
		{"GET", "/guilds/1234567/members/search?limit=1000&query=a", "GET /guilds/:major/members/search", "/guilds/1234567"},
		{"GET", "/guilds/1234567", "GET /guilds/:major", "/guilds/1234567"},
		{"GET", "/webhooks/1234567", "GET /webhooks/:major", "/webhooks/1234567"},
		{"POST", "/webhooks/1234567/tok-en_A?wait=true", "POST /webhooks/:major", "/webhooks/1234567/tok-en_A"},
		{"PATCH", "/webhooks/1234567/tokenB/messages/7654321", "PATCH /webhooks/:major/messages/:id", "/webhooks/1234567/tokenB"},
		{"DELETE", "/invites/abc", "DELETE /invites/abc", ""},
		{"GET", "/users/@me/guilds", "GET /users/@me/guilds", ""},
		{"GET", "/applications/1234567/commands", "GET /applications/:id/commands", ""},
		{"GET", "/guilds/templates/abc", "GET /guilds/templates/abc", ""},
	}
	for _, tt := range tests {
		if route, major := parseRoute(tt.method, tt.path); route != tt.route || major != tt.major {
			t.Errorf("parseRoute(%q, %q) = %q, %q; want %q, %q", tt.method, tt.path, route, major, tt.route, tt.major)
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

func TestEncodeAuditLogReason(t *testing.T) {
	for _, reason := range []string{"Managed by Terraform", "a+b & c=d/e?f#g%", "Café ✨ 変更", "line\nbreak"} {
		encoded := encodeAuditLogReason(reason)
		if strings.ContainsAny(encoded, " +\n") {
			t.Errorf("encodeAuditLogReason(%q) = %q contains an unescaped character", reason, encoded)
		}
		for name, unescape := range map[string]func(string) (string, error){"path": url.PathUnescape, "query": url.QueryUnescape} {
			if got, err := unescape(encoded); err != nil || got != reason {
				t.Errorf("%s-unescaping %q = %q, %v; want %q", name, encoded, got, err, reason)
			}
		}
	}
	if got := encodeAuditLogReason("Managed by Terraform"); got != "Managed%20by%20Terraform" {
		t.Errorf("encoded = %q", got)
	}
}

func TestAuditLogReasonHeader(t *testing.T) {
	var (
		mu      sync.Mutex
		headers = map[string][]string{}
	)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		key := r.Method + " " + r.URL.Path
		headers[key] = append(headers[key], r.Header.Get("X-Audit-Log-Reason"))
		calls := len(headers[key])
		mu.Unlock()
		if key == "DELETE /guilds/1/roles/2" && calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"retry_after":0.001}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	ctx := context.Background()

	if _, err := c.CreateRole(ctx, "1", Payload{"name": "r"}); err != nil {
		t.Fatal(err)
	}
	c.SetAuditLogReason("Managed by Terraform")
	if _, err := c.ModifyRole(ctx, "1", "2", Payload{"name": "r"}); err != nil {
		t.Fatal(err)
	}
	override := WithAuditLogReason(ctx, "per resource ✨")
	if err := c.DeleteRole(override, "1", "2"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetGuild(override, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateMessage(override, "3", Payload{"content": "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := c.ModifyChannelPositions(override, "1", []PositionUpdate{{ID: "4", Position: 1}}); err != nil {
		t.Fatal(err)
	}

	want := map[string][]string{
		"POST /guilds/1/roles":      {""},
		"PATCH /guilds/1/roles/2":   {"Managed%20by%20Terraform"},
		"DELETE /guilds/1/roles/2":  {"per%20resource%20%E2%9C%A8", "per%20resource%20%E2%9C%A8"},
		"GET /guilds/1":             {""},
		"POST /channels/3/messages": {""},
		"PATCH /guilds/1/channels":  {""},
	}
	for key, w := range want {
		if !slices.Equal(headers[key], w) {
			t.Errorf("%s X-Audit-Log-Reason = %q, want %q", key, headers[key], w)
		}
	}
}

// TestAuditLogReasonEndpoints pins which endpoints receive the header to the
// ones Discord documents as supporting it.
func TestAuditLogReasonEndpoints(t *testing.T) {
	var (
		mu      sync.Mutex
		reasons = map[string]string{}
	)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		reasons[r.Method+" "+r.URL.Path] = r.Header.Get("X-Audit-Log-Reason")
		mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	})
	c.SetAuditLogReason("why")
	ctx := context.Background()
	p := Payload{}
	calls := []struct {
		key     string
		audited bool
		call    func() error
	}{
		{"PATCH /guilds/1", true, func() error { _, err := c.ModifyGuild(ctx, "1", p); return err }},
		{"POST /guilds/1/roles", true, func() error { _, err := c.CreateRole(ctx, "1", p); return err }},
		{"PATCH /guilds/1/roles/2", true, func() error { _, err := c.ModifyRole(ctx, "1", "2", p); return err }},
		{"DELETE /guilds/1/roles/2", true, func() error { return c.DeleteRole(ctx, "1", "2") }},
		{"PATCH /guilds/1/roles", true, func() error { return c.ModifyRolePositions(ctx, "1", nil) }},
		{"POST /guilds/1/channels", true, func() error { _, err := c.CreateChannel(ctx, "1", p); return err }},
		{"PATCH /channels/3", true, func() error { _, err := c.ModifyChannel(ctx, "3", p); return err }},
		{"DELETE /channels/3", true, func() error { return c.DeleteChannel(ctx, "3") }},
		{"PATCH /guilds/1/channels", false, func() error { return c.ModifyChannelPositions(ctx, "1", nil) }},
		{"PUT /channels/3/permissions/4", true, func() error { return c.EditChannelPermission(ctx, "3", Overwrite{ID: "4"}) }},
		{"DELETE /channels/3/permissions/4", true, func() error { return c.DeleteChannelPermission(ctx, "3", "4") }},
		{"PATCH /guilds/1/members/5", true, func() error { _, err := c.ModifyMember(ctx, "1", "5", p); return err }},
		{"PUT /guilds/1/members/5/roles/2", true, func() error { return c.AddMemberRole(ctx, "1", "5", "2") }},
		{"DELETE /guilds/1/members/5/roles/2", true, func() error { return c.RemoveMemberRole(ctx, "1", "5", "2") }},
		{"POST /channels/3/webhooks", true, func() error { _, err := c.CreateWebhook(ctx, "3", p); return err }},
		{"PATCH /webhooks/6", true, func() error { _, err := c.ModifyWebhook(ctx, "6", p); return err }},
		{"DELETE /webhooks/6", true, func() error { return c.DeleteWebhook(ctx, "6") }},
		{"POST /channels/3/invites", true, func() error { _, err := c.CreateInvite(ctx, "3", p); return err }},
		{"DELETE /invites/abc", true, func() error { return c.DeleteInvite(ctx, "abc") }},
		{"POST /channels/3/messages", false, func() error { _, err := c.CreateMessage(ctx, "3", p); return err }},
		{"PATCH /channels/3/messages/7", false, func() error { _, err := c.EditMessage(ctx, "3", "7", p); return err }},
		{"DELETE /channels/3/messages/7", true, func() error { return c.DeleteMessage(ctx, "3", "7") }},
		{"PUT /channels/3/messages/pins/7", true, func() error { return c.PinMessage(ctx, "3", "7") }},
		{"DELETE /channels/3/messages/pins/7", true, func() error { return c.UnpinMessage(ctx, "3", "7") }},
		{"POST /guilds/1/emojis", true, func() error { _, err := c.CreateEmoji(ctx, "1", p); return err }},
		{"PATCH /guilds/1/emojis/8", true, func() error { _, err := c.ModifyEmoji(ctx, "1", "8", p); return err }},
		{"DELETE /guilds/1/emojis/8", true, func() error { return c.DeleteEmoji(ctx, "1", "8") }},
		{"POST /guilds/1/stickers", true, func() error { _, err := c.CreateSticker(ctx, "1", &Multipart{Payload: p}); return err }},
		{"PATCH /guilds/1/stickers/9", true, func() error { _, err := c.ModifySticker(ctx, "1", "9", p); return err }},
		{"DELETE /guilds/1/stickers/9", true, func() error { return c.DeleteSticker(ctx, "1", "9") }},
		{"GET /guilds/1/stickers/9", false, func() error { _, err := c.GetGuildSticker(ctx, "1", "9"); return err }},
		{"POST /guilds/1/soundboard-sounds", true, func() error { _, err := c.CreateSoundboardSound(ctx, "1", p); return err }},
		{"PATCH /guilds/1/soundboard-sounds/10", true, func() error { _, err := c.ModifySoundboardSound(ctx, "1", "10", p); return err }},
		{"DELETE /guilds/1/soundboard-sounds/10", true, func() error { return c.DeleteSoundboardSound(ctx, "1", "10") }},
		{"GET /guilds/1/soundboard-sounds/10", false, func() error { _, err := c.GetSoundboardSound(ctx, "1", "10"); return err }},
		{"GET /guilds/1", false, func() error { _, err := c.GetGuild(ctx, "1"); return err }},
	}
	for _, tt := range calls {
		if err := tt.call(); err != nil {
			t.Fatalf("%s: %v", tt.key, err)
		}
		got, ok := reasons[tt.key]
		switch {
		case !ok:
			t.Errorf("%s was not requested", tt.key)
		case tt.audited && got != "why":
			t.Errorf("%s X-Audit-Log-Reason = %q, want %q", tt.key, got, "why")
		case !tt.audited && got != "":
			t.Errorf("%s sent X-Audit-Log-Reason %q to an endpoint that does not support it", tt.key, got)
		}
	}
}
