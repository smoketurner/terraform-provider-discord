package apispec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// Default locations of the spec repository.
const (
	RawBaseURL = "https://raw.githubusercontent.com/discord/discord-api-spec"
	APIBaseURL = "https://api.github.com/repos/discord/discord-api-spec"
)

var commitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// LatestCommit returns the commit at the head of the spec repository's main
// branch.
func LatestCommit(ctx context.Context, c *http.Client, apiBaseURL string) (string, error) {
	b, err := get(ctx, c, apiBaseURL+"/commits/main", "application/vnd.github.sha")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(b))
	if !commitSHA.MatchString(sha) {
		return "", fmt.Errorf("unexpected commit %q", sha)
	}
	return sha, nil
}

// FetchSpec downloads specs/openapi.json at a commit.
func FetchSpec(ctx context.Context, c *http.Client, rawBaseURL, commit string) ([]byte, error) {
	if !commitSHA.MatchString(commit) {
		return nil, fmt.Errorf("commit %q is not a full SHA", commit)
	}
	return get(ctx, c, rawBaseURL+"/"+commit+"/specs/openapi.json", "")
}

// VerifySHA256 checks b against a hex SHA-256 checksum.
func VerifySHA256(b []byte, want string) error {
	if got := SHA256(b); got != want {
		return fmt.Errorf("spec checksum is %s, want %s", got, want)
	}
	return nil
}

// SHA256 returns the hex SHA-256 checksum of b.
func SHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func get(ctx context.Context, c *http.Client, url, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	return b, nil
}
