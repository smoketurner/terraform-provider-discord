package discord

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type receivedPart struct {
	name, filename, contentType, data string
}

// readParts parses a multipart request in order, failing the test unless the
// Content-Type is multipart/form-data with a boundary.
func readParts(t *testing.T, r *http.Request) []receivedPart {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		return nil
	}
	mr := multipart.NewReader(r.Body, params["boundary"])
	var parts []receivedPart
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			return parts
		}
		if err != nil {
			t.Errorf("reading part: %v", err)
			return parts
		}
		data, _ := io.ReadAll(p)
		parts = append(parts, receivedPart{p.FormName(), p.FileName(), p.Header.Get("Content-Type"), string(data)})
	}
}

func TestMultipartPartLayout(t *testing.T) {
	var got []receivedPart
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = readParts(t, r)
		_, _ = w.Write([]byte(`{"id":"1"}`))
	})
	body := &Multipart{
		Payload: Payload{"content": "hi", "attachments": []Payload{{"id": 0, "filename": "a.png"}}},
		Files: []File{
			{Field: "files[0]", Name: "a.png", ContentType: "image/png", Data: []byte("png bytes")},
			{Field: "files[1]", Name: `quote"d.bin`, Data: []byte{0, 1, 2}},
		},
	}
	var m Message
	if err := c.do(context.Background(), http.MethodPost, "/channels/1/messages", body, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "1" {
		t.Errorf("decoded ID = %q", m.ID)
	}
	want := []receivedPart{
		{"payload_json", "", "application/json", `{"attachments":[{"filename":"a.png","id":0}],"content":"hi"}`},
		{"files[0]", "a.png", "image/png", "png bytes"},
		{"files[1]", `quote"d.bin`, "application/octet-stream", "\x00\x01\x02"},
	}
	if len(got) != len(want) {
		t.Fatalf("parts = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("part %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestMultipartWithoutPayload(t *testing.T) {
	var got []receivedPart
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = readParts(t, r)
		w.WriteHeader(http.StatusNoContent)
	})
	body := &Multipart{Files: []File{{Field: "file", Name: "s.png", Data: []byte("x")}}}
	if err := c.do(context.Background(), http.MethodPost, "/guilds/1/stickers", body, nil); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].name != "file" || got[0].filename != "s.png" {
		t.Errorf("parts = %+v", got)
	}
}

func TestMultipartRetryResendsSameBody(t *testing.T) {
	var (
		mu           sync.Mutex
		bodies       [][]byte
		contentTypes []string
		calls        atomic.Int32
	)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, data)
		contentTypes = append(contentTypes, r.Header.Get("Content-Type"))
		mu.Unlock()
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"retry_after":0.01}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	body := &Multipart{Payload: Payload{"name": "s"}, Files: []File{{Field: "file", Name: "s.png", Data: []byte("data")}}}
	if err := c.do(context.Background(), http.MethodPost, "/guilds/1/stickers", body, nil); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 {
		t.Fatalf("requests = %d, want 2", len(bodies))
	}
	if !bytes.Equal(bodies[0], bodies[1]) || len(bodies[0]) == 0 {
		t.Errorf("retry sent a different body:\n%q\n%q", bodies[0], bodies[1])
	}
	if contentTypes[0] != contentTypes[1] || !strings.HasPrefix(contentTypes[0], "multipart/form-data; boundary=") {
		t.Errorf("Content-Type = %q then %q", contentTypes[0], contentTypes[1])
	}
}

func TestRequestSizeLimit(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{}`))
	})
	ctx := context.Background()

	tooLarge := &Multipart{Files: []File{{Field: "files[0]", Name: "big.bin", Data: make([]byte, MaxRequestSize)}}}
	err := c.do(ctx, http.MethodPost, "/channels/1/messages", tooLarge, nil)
	if !errors.Is(err, ErrRequestTooLarge) {
		t.Fatalf("err = %v, want ErrRequestTooLarge", err)
	}
	if calls.Load() != 0 {
		t.Errorf("an oversized request was sent")
	}

	// The limit covers the whole body, so a file must leave room for the
	// multipart framing.
	fits := &Multipart{Files: []File{{Field: "files[0]", Name: "ok.bin", Data: make([]byte, MaxRequestSize-1024)}}}
	if err := c.do(ctx, http.MethodPost, "/channels/1/messages", fits, nil); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestMultipartPayloadEncodingError(t *testing.T) {
	c := newTestClient(t, func(http.ResponseWriter, *http.Request) {
		t.Error("request sent despite an encoding error")
	})
	body := &Multipart{Payload: Payload{"bad": make(chan int)}}
	if err := c.do(context.Background(), http.MethodPost, "/x", body, nil); err == nil || !strings.Contains(err.Error(), "payload_json") {
		t.Fatalf("err = %v", err)
	}
}

func TestJSONEncodingError(t *testing.T) {
	c := newTestClient(t, func(http.ResponseWriter, *http.Request) {
		t.Error("request sent despite an encoding error")
	})
	if err := c.do(context.Background(), http.MethodPost, "/x", Payload{"bad": make(chan int)}, nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestFileContent(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.png")
	if err := os.WriteFile(file, []byte("from file"), 0o600); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(big, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(big, MaxRequestSize+1); err != nil {
		t.Fatal(err)
	}
	exact := filepath.Join(dir, "exact.bin")
	if err := os.WriteFile(exact, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(exact, MaxRequestSize); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name, path, b64 string
		want            string
		wantLen         int
		wantErr         string
		tooLarge        bool
	}{
		{name: "path", path: file, want: "from file"},
		{name: "base64", b64: base64.StdEncoding.EncodeToString([]byte("from base64")), want: "from base64"},
		{name: "file at the limit", path: exact, wantLen: MaxRequestSize},
		{name: "both", path: file, b64: "eA==", wantErr: "not both"},
		{name: "neither", wantErr: "set a file path or base64 content"},
		{name: "missing file", path: filepath.Join(dir, "missing"), wantErr: "no such file"},
		{name: "directory", path: dir, wantErr: "directory"},
		{name: "invalid base64", b64: "not base64!", wantErr: "decoding base64"},
		{name: "file too large", path: big, tooLarge: true},
		{name: "base64 too large", b64: base64.StdEncoding.EncodeToString(make([]byte, MaxRequestSize+1)), tooLarge: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FileContent(tt.path, tt.b64)
			switch {
			case tt.tooLarge:
				if !errors.Is(err, ErrRequestTooLarge) {
					t.Fatalf("err = %v, want ErrRequestTooLarge", err)
				}
			case tt.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
			case err != nil:
				t.Fatal(err)
			case tt.wantLen > 0:
				if len(got) != tt.wantLen {
					t.Errorf("len = %d, want %d", len(got), tt.wantLen)
				}
			case string(got) != tt.want:
				t.Errorf("content = %q, want %q", got, tt.want)
			}
		})
	}
}
