package discordtest

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

func multipartRequest(t *testing.T, build func(w *multipart.Writer)) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	build(w)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", &buf)
	r.Header.Set("Content-Type", w.FormDataContentType())
	return r
}

func TestDecodeMultipart(t *testing.T) {
	r := multipartRequest(t, func(w *multipart.Writer) {
		part, _ := w.CreatePart(textproto.MIMEHeader{
			"Content-Disposition": {`form-data; name="payload_json"`},
			"Content-Type":        {"application/json"},
		})
		_, _ = part.Write([]byte(`{"content":"hi","tts":true}`))
		_ = w.WriteField("name", "plain field")
		part, _ = w.CreatePart(textproto.MIMEHeader{
			"Content-Disposition": {multipart.FileContentDisposition("files[0]", "a.png")},
			"Content-Type":        {"image/png"},
		})
		_, _ = part.Write([]byte("png bytes"))
	})
	body, err := decode(r)
	if err != nil {
		t.Fatal(err)
	}
	var content, name string
	var tts bool
	var upload Upload
	set(body, "content", &content)
	set(body, "tts", &tts)
	set(body, "name", &name)
	set(body, "files[0]", &upload)
	if content != "hi" || !tts || name != "plain field" {
		t.Errorf("fields = %q %v %q", content, tts, name)
	}
	if upload.Filename != "a.png" || upload.ContentType != "image/png" || string(upload.Data) != "png bytes" {
		t.Errorf("upload = %+v", upload)
	}
}

func TestDecodeMultipartInvalidPayloadJSON(t *testing.T) {
	r := multipartRequest(t, func(w *multipart.Writer) {
		_ = w.WriteField("payload_json", "{not json")
	})
	if _, err := decode(r); err == nil {
		t.Fatal("expected error for invalid payload_json")
	}
}

func TestDecodeMultipartMissingBoundary(t *testing.T) {
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", bytes.NewReader([]byte("x")))
	r.Header.Set("Content-Type", "multipart/form-data")
	if _, err := decode(r); err == nil {
		t.Fatal("expected error for a multipart body without a boundary")
	}
}

func TestCreateMessageFromMultipart(t *testing.T) {
	s := NewServer()
	defer s.Close()
	s.channels["300"] = &discord.Channel{ID: "300", GuildID: GuildID}

	r := multipartRequest(t, func(w *multipart.Writer) {
		_ = w.WriteField("payload_json", `{"content":"from multipart"}`)
	})
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, s.URL+"/channels/300/messages", r.Body)
	req.Header.Set("Content-Type", r.Header.Get("Content-Type"))
	req.Header.Set("Authorization", "Bot "+Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var m struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil || resp.StatusCode != http.StatusOK || m.Content != "from multipart" {
		t.Fatalf("status %d, message %+v, err %v", resp.StatusCode, m, err)
	}
}
