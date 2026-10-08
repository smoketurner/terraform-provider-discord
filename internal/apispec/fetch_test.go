package apispec

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testCommit = "0123456789abcdef0123456789abcdef01234567"

func specServer(t *testing.T, latest string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /commits/main", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/vnd.github.sha" {
			http.Error(w, "wrong accept header", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(latest + "\n"))
	})
	mux.HandleFunc("GET /"+testCommit+"/specs/openapi.json", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(fixtureSpec))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestLatestCommit(t *testing.T) {
	srv := specServer(t, testCommit)
	got, err := LatestCommit(t.Context(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got != testCommit {
		t.Errorf("LatestCommit() = %q, want %q", got, testCommit)
	}
}

func TestLatestCommitErrors(t *testing.T) {
	srv := specServer(t, "<html>")
	if _, err := LatestCommit(t.Context(), srv.Client(), srv.URL); err == nil || !strings.Contains(err.Error(), "unexpected commit") {
		t.Errorf("got %v, want an unexpected commit error", err)
	}
	if _, err := LatestCommit(t.Context(), srv.Client(), srv.URL+"/missing"); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("got %v, want an HTTP 404 error", err)
	}
	srv.Close()
	if _, err := LatestCommit(t.Context(), srv.Client(), srv.URL); err == nil {
		t.Error("request to a closed server succeeded")
	}
}

func TestFetchSpec(t *testing.T) {
	srv := specServer(t, testCommit)
	b, err := FetchSpec(t.Context(), srv.Client(), srv.URL, testCommit)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySHA256(b, SHA256([]byte(fixtureSpec))); err != nil {
		t.Error(err)
	}
	if err := VerifySHA256(b, strings.Repeat("0", 64)); err == nil {
		t.Error("checksum mismatch was not reported")
	}
}

func TestFetchSpecErrors(t *testing.T) {
	srv := specServer(t, testCommit)
	if _, err := FetchSpec(t.Context(), srv.Client(), srv.URL, "main"); err == nil || !strings.Contains(err.Error(), "not a full SHA") {
		t.Errorf("got %v, want a full SHA error", err)
	}
	if _, err := FetchSpec(t.Context(), srv.Client(), srv.URL, strings.Repeat("f", 40)); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("got %v, want an HTTP 404 error", err)
	}
}

func TestPinnedChecksumFormat(t *testing.T) {
	if !commitSHA.MatchString(PinnedCommit) {
		t.Errorf("PinnedCommit %q is not a full commit SHA", PinnedCommit)
	}
	if len(PinnedSHA256) != 64 {
		t.Errorf("PinnedSHA256 %q is not a SHA-256 checksum", PinnedSHA256)
	}
}
