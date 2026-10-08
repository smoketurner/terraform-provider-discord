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
	"net/http"
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

	mu          sync.Mutex
	buckets     map[string]*bucket
	globalUntil time.Time
}

type bucket struct {
	mu        sync.Mutex
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
		buckets:    map[string]*bucket{},
	}
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

var snowflakeSegment = regexp.MustCompile(`/\d{5,}`)

// routeKey groups requests into rate limit buckets: the top-level resource ID
// (the "major parameter") is kept, every other snowflake is collapsed.
func routeKey(method, path string) string {
	first := true
	key := snowflakeSegment.ReplaceAllStringFunc(path, func(s string) string {
		if first {
			first = false
			return s
		}
		return "/:id"
	})
	if i := strings.IndexByte(key, '?'); i >= 0 {
		key = key[:i]
	}
	return method + " " + key
}

func (c *Client) bucketFor(key string) *bucket {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.buckets[key]
	if !ok {
		b = &bucket{remaining: 1}
		c.buckets[key] = b
	}
	return b
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

func (c *Client) waitGlobal(ctx context.Context) error {
	c.mu.Lock()
	until := c.globalUntil
	c.mu.Unlock()
	return sleep(ctx, time.Until(until))
}

// do performs a request, honoring Discord rate limits and retrying on 429 and
// transient gateway errors. A nil body sends no payload, a *Multipart is sent
// as multipart/form-data and anything else as JSON; out may be nil.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
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

	b := c.bucketFor(routeKey(method, path))
	b.mu.Lock()
	defer b.mu.Unlock()

	for attempt := 1; ; attempt++ {
		if b.remaining <= 0 {
			if err := sleep(ctx, time.Until(b.resetAt)); err != nil {
				return err
			}
		}
		if err := c.waitGlobal(ctx); err != nil {
			return err
		}

		resp, err := c.send(ctx, method, path, header, payload)
		if err != nil {
			return err
		}
		respBody, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return fmt.Errorf("reading response body: %w", err)
		}
		c.updateBucket(b, resp.Header)

		switch {
		case resp.StatusCode == http.StatusTooManyRequests && attempt < maxAttempts:
			if err := sleep(ctx, c.retryAfter(resp.Header, respBody)); err != nil {
				return err
			}
			continue
		case resp.StatusCode >= 502 && resp.StatusCode <= 504 && method != http.MethodPost && attempt < maxAttempts:
			// POST is not retried: the request may have succeeded behind the
			// gateway error, and retrying would create a duplicate.
			if err := sleep(ctx, time.Duration(attempt)*time.Second); err != nil {
				return err
			}
			continue
		case resp.StatusCode >= 300:
			apiErr := &APIError{Status: resp.StatusCode, Method: method, Path: path}
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

func (c *Client) updateBucket(b *bucket, h http.Header) {
	remaining, err := strconv.Atoi(h.Get("X-RateLimit-Remaining"))
	if err != nil {
		b.remaining = 1
		return
	}
	b.remaining = remaining
	if after, err := strconv.ParseFloat(h.Get("X-RateLimit-Reset-After"), 64); err == nil {
		b.resetAt = time.Now().Add(time.Duration(after * float64(time.Second)))
	}
}

func (c *Client) retryAfter(h http.Header, body []byte) time.Duration {
	var rl struct {
		RetryAfter float64 `json:"retry_after"`
		Global     bool    `json:"global"`
	}
	_ = json.Unmarshal(body, &rl)
	if rl.RetryAfter == 0 {
		rl.RetryAfter, _ = strconv.ParseFloat(h.Get("Retry-After"), 64)
	}
	wait := time.Duration(rl.RetryAfter * float64(time.Second))
	if wait <= 0 {
		wait = time.Second
	}
	if rl.Global || h.Get("X-RateLimit-Global") == "true" {
		c.mu.Lock()
		c.globalUntil = time.Now().Add(wait)
		c.mu.Unlock()
	}
	return wait
}
