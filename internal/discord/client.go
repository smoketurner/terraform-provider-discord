// Package discord is a minimal client for the Discord REST API (v10) covering
// the endpoints the Terraform provider manages.
package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the Discord REST API endpoint used when none is configured.
const DefaultBaseURL = "https://discord.com/api/v10"

const maxAttempts = 5

// Client talks to the Discord REST API using a bot token.
type Client struct {
	baseURL    string
	token      string
	userAgent  string
	httpClient *http.Client

	// auditLogReason is sent on requests to endpoints that support it unless
	// the request's context carries its own reason.
	auditLogReason string

	// mu guards routes, buckets, the fields of every bucket except sem, and
	// globalUntil. It is never held while waiting or sending a request.
	mu sync.Mutex
	// routes maps a route to the X-RateLimit-Bucket Discord last returned for
	// it, so routes that share a limit share a bucket.
	routes      map[string]string
	buckets     map[string]*bucket
	globalUntil time.Time
}

// bucket is the rate limit state for one bucket and top-level resource.
// Requests in a bucket are sent one at a time, so the remaining count from
// the last response holds for the next request.
type bucket struct {
	key string
	// sem holds a token while a request in the bucket is in flight. A channel
	// rather than a mutex lets a waiting request give up when its context ends.
	sem chan struct{}
	// refs counts requests holding or waiting for sem; the client only drops
	// a bucket that has none.
	refs      int
	remaining int
	resetAt   time.Time
}

// NewClient returns a client for the given API base URL and bot token. The
// token may be supplied with or without the "Bot " prefix.
func NewClient(baseURL, token, version string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      NormalizeToken(token),
		userAgent:  fmt.Sprintf("DiscordBot (https://github.com/smoketurner/terraform-provider-discord, %s)", version),
		httpClient: &http.Client{Timeout: 60 * time.Second},
		routes:     map[string]string{},
		buckets:    map[string]*bucket{},
	}
}

// SetAuditLogReason sets the reason recorded in the audit log for changes made
// through the client. An empty reason sends none. Call it before the client is
// used.
func (c *Client) SetAuditLogReason(reason string) {
	c.auditLogReason = reason
}

// MaxAuditLogReasonLength is the most characters Discord accepts in an audit
// log reason.
const MaxAuditLogReasonLength = 512

type auditLogReasonKey struct{}

// WithAuditLogReason returns a context whose requests record reason in the
// audit log in place of the client's default. Endpoints that do not support
// the header never receive it.
func WithAuditLogReason(ctx context.Context, reason string) context.Context {
	return context.WithValue(ctx, auditLogReasonKey{}, reason)
}

// encodeAuditLogReason URL-encodes a reason as Discord requires. Spaces become
// %20 rather than "+", which decodes the same under path and form rules.
func encodeAuditLogReason(reason string) string {
	return strings.ReplaceAll(url.QueryEscape(reason), "+", "%20")
}

// NormalizeToken strips surrounding whitespace and an optional "Bot " prefix.
func NormalizeToken(token string) string {
	token = strings.TrimSpace(token)
	if len(token) > 4 && strings.EqualFold(token[:4], "bot ") {
		token = strings.TrimSpace(token[4:])
	}
	return token
}

// APIError is an error response returned by Discord.
type APIError struct {
	Status  int             `json:"-"`
	Method  string          `json:"-"`
	Path    string          `json:"-"`
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Errors  json.RawMessage `json:"errors,omitempty"`
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("%s %s: HTTP %d", e.Method, e.Path, e.Status)
	if e.Message != "" {
		msg += fmt.Sprintf(": %s (code %d)", e.Message, e.Code)
	}
	if len(e.Errors) > 0 {
		msg += ": " + string(e.Errors)
	}
	return msg
}

// IsNotFound reports whether err is a Discord 404 response.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound
}

var (
	snowflakeSegment = regexp.MustCompile(`/\d{5,}`)
	// majorParameter matches the top-level resource Discord computes rate
	// limits for separately: a channel, a guild, or a webhook with its token.
	majorParameter = regexp.MustCompile(`^/(?:(channels|guilds)/\d+|(webhooks)/\d+(?:/[^/]+)?)`)
)

// parseRoute splits a request into its route, the method and path with IDs
// replaced by placeholders, and its major parameter, the top-level resource
// in the path (empty when there is none). Requests share a rate limit when
// their routes map to the same X-RateLimit-Bucket and their major parameters
// are equal.
func parseRoute(method, path string) (route, major string) {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	if m := majorParameter.FindStringSubmatch(path); m != nil {
		major = m[0]
		path = "/" + m[1] + m[2] + "/:major" + path[len(major):]
	}
	return method + " " + snowflakeSegment.ReplaceAllString(path, "/:id"), major
}

// bucketKeyLocked returns the key of the bucket that limits a request: the
// route's X-RateLimit-Bucket once Discord has returned one, until then the
// route itself.
func (c *Client) bucketKeyLocked(route, major string) string {
	if hash, ok := c.routes[route]; ok {
		return hash + " " + major
	}
	return route + " " + major
}

func (c *Client) bucketLocked(key string) *bucket {
	b, ok := c.buckets[key]
	if !ok {
		b = &bucket{key: key, sem: make(chan struct{}, 1), remaining: 1}
		c.buckets[key] = b
	}
	return b
}

// acquire waits until no other request is in flight in the request's bucket
// and returns the bucket. If the route was mapped to another bucket while it
// waited, it moves to that one. A request holds at most one bucket at a time,
// so learning that two routes share a bucket cannot deadlock.
func (c *Client) acquire(ctx context.Context, route, major string) (*bucket, error) {
	for {
		c.mu.Lock()
		b := c.bucketLocked(c.bucketKeyLocked(route, major))
		b.refs++
		c.mu.Unlock()

		select {
		case b.sem <- struct{}{}:
		case <-ctx.Done():
			c.mu.Lock()
			b.refs--
			c.dropIdleLocked(b)
			c.mu.Unlock()
			return nil, ctx.Err()
		}

		c.mu.Lock()
		current := c.buckets[c.bucketKeyLocked(route, major)] == b
		c.mu.Unlock()
		if current {
			return b, nil
		}
		c.release(b)
	}
}

func (c *Client) release(b *bucket) {
	<-b.sem
	c.mu.Lock()
	b.refs--
	c.dropIdleLocked(b)
	c.mu.Unlock()
}

// dropIdleLocked forgets a bucket no request is using once it no longer
// limits anything, so the client does not keep state for every resource it
// has touched. A new bucket allows one request, the same as an idle bucket
// with requests remaining.
func (c *Client) dropIdleLocked(b *bucket) {
	if b.refs > 0 || c.buckets[b.key] != b {
		return
	}
	if wait := time.Until(b.resetAt); b.remaining <= 0 && wait > 0 {
		time.AfterFunc(wait, func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.dropIdleLocked(b)
		})
		return
	}
	delete(c.buckets, b.key)
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// wait blocks until neither the bucket nor the global rate limit forbids a
// request. Other requests can extend either limit while it sleeps.
func (c *Client) wait(ctx context.Context, b *bucket) error {
	for {
		c.mu.Lock()
		until := c.globalUntil
		if b.remaining <= 0 && b.resetAt.After(until) {
			until = b.resetAt
		}
		c.mu.Unlock()
		d := time.Until(until)
		if d <= 0 {
			return nil
		}
		if err := sleep(ctx, d); err != nil {
			return err
		}
	}
}

// do performs a request to an endpoint that does not accept an audit log
// reason.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	return c.request(ctx, method, path, "", body, out)
}

// doAudited performs a request to an endpoint whose documentation says it
// supports the X-Audit-Log-Reason header.
func (c *Client) doAudited(ctx context.Context, method, path string, body, out any) error {
	reason := c.auditLogReason
	if r, ok := ctx.Value(auditLogReasonKey{}).(string); ok {
		reason = r
	}
	return c.request(ctx, method, path, reason, body, out)
}

// request performs a request, honoring Discord rate limits and retrying on
// 429 and transient gateway errors. A nil body sends no payload, a *Multipart
// is sent as multipart/form-data and anything else as JSON; out may be nil.
// A non-empty reason is sent in the X-Audit-Log-Reason header.
func (c *Client) request(ctx context.Context, method, path, reason string, body, out any) error {
	payload, contentType, err := encodeBody(body)
	if err != nil {
		return err
	}
	if len(payload) > MaxRequestSize {
		return fmt.Errorf("%s %s: request body is %d bytes, %w", method, path, len(payload), ErrRequestTooLarge)
	}
	header := http.Header{}
	header.Set("Authorization", "Bot "+c.token)
	header.Set("User-Agent", c.userAgent)
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	if reason != "" {
		header.Set("X-Audit-Log-Reason", encodeAuditLogReason(reason))
	}

	route, major := parseRoute(method, path)
	for attempt := 1; ; attempt++ {
		status, respBody, err := c.attempt(ctx, route, major, method, path, header, payload)
		if err != nil {
			return err
		}

		switch {
		case status == http.StatusTooManyRequests && attempt < maxAttempts:
			// The next attempt waits as long as the 429 asked. Bucket
			// headers can be inaccurate (Discord says so for emoji routes),
			// so a 429 is possible even after waiting ahead of time.
			continue
		case status >= 502 && status <= 504 && method != http.MethodPost && attempt < maxAttempts:
			// POST is not retried: the request may have succeeded behind the
			// gateway error, and retrying would create a duplicate.
			if err := sleep(ctx, time.Duration(attempt)*time.Second); err != nil {
				return err
			}
			continue
		case status >= 300:
			apiErr := &APIError{Status: status, Method: method, Path: path}
			_ = json.Unmarshal(respBody, apiErr)
			return apiErr
		}

		if out == nil || len(respBody) == 0 {
			return nil
		}
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decoding response from %s %s: %w", method, path, err)
		}
		return nil
	}
}

// encodeBody serializes a request body once so that retries resend the same
// bytes, including the same multipart boundary.
func encodeBody(body any) ([]byte, string, error) {
	switch b := body.(type) {
	case nil:
		return nil, "", nil
	case *Multipart:
		return b.encode()
	default:
		payload, err := json.Marshal(b)
		if err != nil {
			return nil, "", fmt.Errorf("encoding request body: %w", err)
		}
		return payload, "application/json", nil
	}
}

func (c *Client) send(ctx context.Context, method, path string, header http.Header, payload []byte) (*http.Response, error) {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header = header.Clone()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	return resp, nil
}

// attempt sends a request once its bucket and the global limit allow it and
// records the rate limit headers of the response.
func (c *Client) attempt(ctx context.Context, route, major, method, path string, header http.Header, payload []byte) (int, []byte, error) {
	b, err := c.acquire(ctx, route, major)
	if err != nil {
		return 0, nil, err
	}
	defer c.release(b)
	if err := c.wait(ctx, b); err != nil {
		return 0, nil, err
	}

	resp, err := c.send(ctx, method, path, header, payload)
	if err != nil {
		return 0, nil, err
	}
	respBody, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return 0, nil, fmt.Errorf("reading response body: %w", err)
	}
	c.update(b, route, major, resp.StatusCode, resp.Header, respBody)
	return resp.StatusCode, respBody, nil
}

// update records the rate limit headers of a response to a request sent in
// bucket b. A new X-RateLimit-Bucket for the route moves the route, and every
// request waiting in its old bucket, to the bucket Discord named.
func (c *Client) update(b *bucket, route, major string, status int, h http.Header, body []byte) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	target := b
	if hash := h.Get("X-RateLimit-Bucket"); hash != "" && c.routes[route] != hash {
		c.routes[route] = hash
		target = c.bucketLocked(c.bucketKeyLocked(route, major))
	}

	if remaining, err := strconv.Atoi(h.Get("X-RateLimit-Remaining")); err == nil {
		target.remaining = remaining
		target.resetAt = resetTime(h, now)
	}

	if status == http.StatusTooManyRequests {
		wait, global := retryAfter(h, body)
		until := now.Add(wait)
		if global {
			if until.After(c.globalUntil) {
				c.globalUntil = until
			}
		} else {
			// A shared (per-resource) 429 does not count toward the invalid
			// request limit, but its Remaining header does not reflect the
			// limit that was hit, so it is waited out like a user 429.
			target.remaining = 0
			if until.After(target.resetAt) {
				target.resetAt = until
			}
		}
	}

	if target != b {
		c.dropIdleLocked(target)
	}
}

// resetTime returns when a bucket resets. X-RateLimit-Reset-After is used
// when present because, unlike the X-RateLimit-Reset timestamp, it does not
// depend on the local clock agreeing with Discord's.
func resetTime(h http.Header, now time.Time) time.Time {
	if after, err := strconv.ParseFloat(h.Get("X-RateLimit-Reset-After"), 64); err == nil {
		return now.Add(seconds(after))
	}
	if at, err := strconv.ParseFloat(h.Get("X-RateLimit-Reset"), 64); err == nil {
		return time.UnixMilli(int64(at * 1000))
	}
	return time.Time{}
}

// seconds converts a rate limit value, which Discord gives in seconds with
// millisecond precision, to a duration.
func seconds(s float64) time.Duration {
	return time.Duration(math.Round(s*1000)) * time.Millisecond
}

// retryAfter returns how long a 429 response asks to wait and whether the
// limit hit is the global one rather than the bucket's.
func retryAfter(h http.Header, body []byte) (time.Duration, bool) {
	var rl struct {
		RetryAfter float64 `json:"retry_after"`
		Global     bool    `json:"global"`
	}
	_ = json.Unmarshal(body, &rl)
	if rl.RetryAfter == 0 {
		rl.RetryAfter, _ = strconv.ParseFloat(h.Get("Retry-After"), 64)
	}
	wait := seconds(rl.RetryAfter)
	if wait <= 0 {
		wait = time.Second
	}
	global := rl.Global || h.Get("X-RateLimit-Global") == "true" || h.Get("X-RateLimit-Scope") == "global"
	return wait, global
}
