package discordtest

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
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

func TestStickerUploads(t *testing.T) {
	s := NewServer()
	defer s.Close()
	c := discord.NewClient(s.URL, Token, "test")
	upload := func(contentType string, data []byte) (*discord.Sticker, error) {
		return c.CreateSticker(t.Context(), GuildID, &discord.Multipart{
			Payload: discord.Payload{"name": "wave", "tags": "wave"},
			Files:   []discord.File{{Field: "file", Name: "sticker", ContentType: contentType, Data: data}},
		})
	}
	tests := []struct {
		name        string
		contentType string
		data        []byte
		format      int
	}{
		{"png", "image/png", []byte("\x89PNG\r\n\x1a\n"), 1},
		{"apng", "image/png", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x08acTL"), 2},
		{"gif", "image/gif", []byte("GIF89a"), 4},
		{"jpeg", "image/jpeg", []byte("\xff\xd8\xff"), 0},
		{"too large", "image/png", make([]byte, 512<<10+1), 0},
		{"lottie without feature", "application/json", []byte("{}"), 0},
	}
	for _, tt := range tests {
		st, err := upload(tt.contentType, tt.data)
		switch {
		case tt.format == 0 && err == nil:
			t.Errorf("%s: upload succeeded", tt.name)
		case tt.format != 0 && (err != nil || st.FormatType != tt.format):
			t.Errorf("%s: format %d, err %v, want format %d", tt.name, st.FormatType, err, tt.format)
		}
	}
	if _, err := c.CreateSticker(t.Context(), GuildID, &discord.Multipart{Payload: discord.Payload{"name": "wave", "tags": "wave"}}); err == nil {
		t.Error("upload without a file succeeded")
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, s.URL+"/guilds/"+GuildID+"/stickers",
		strings.NewReader(`{"name":"wave","tags":"wave"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bot "+Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("JSON upload status %d, want 400", resp.StatusCode)
	}
	s.SetGuildFeatures("PARTNERED")
	if st, err := upload("application/json", []byte("{}")); err != nil || st.FormatType != 3 {
		t.Errorf("lottie: %+v, %v", st, err)
	}
	st, err := upload("image/gif", []byte("GIF89a"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := c.GetSticker(t.Context(), st.ID); err != nil || got.ID != st.ID || got.GuildID == nil {
		t.Errorf("GET /stickers/%s = %+v, %v", st.ID, got, err)
	}
}

func TestListSoundboardSounds(t *testing.T) {
	s := NewServer()
	defer s.Close()
	c := discord.NewClient(s.URL, Token, "test")
	for _, name := range []string{"one", "two"} {
		if _, err := c.CreateSoundboardSound(t.Context(), GuildID, discord.Payload{"name": name, "sound": "data:audio/mpeg;base64,//sYwA=="}); err != nil {
			t.Fatal(err)
		}
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, s.URL+"/guilds/"+GuildID+"/soundboard-sounds", nil)
	req.Header.Set("Authorization", "Bot "+Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var list struct {
		Items []discord.SoundboardSound `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil || len(list.Items) != 2 || list.Items[0].Name != "one" || list.Items[0].Volume != 1 {
		t.Fatalf("list = %+v, err %v", list, err)
	}
}

func TestSoundboardSoundValidation(t *testing.T) {
	s := NewServer()
	defer s.Close()
	c := discord.NewClient(s.URL, Token, "test")
	const data = "data:audio/mpeg;base64,//sYwA=="
	for _, p := range []discord.Payload{
		{"name": "no sound"},
		{"name": "volume", "sound": data, "volume": 2},
		{"name": "emojis", "sound": data, "emoji_id": "1", "emoji_name": "x"},
	} {
		if _, err := c.CreateSoundboardSound(t.Context(), GuildID, p); err == nil {
			t.Errorf("%v: create succeeded", p)
		}
	}
	sound, err := c.CreateSoundboardSound(t.Context(), GuildID, discord.Payload{"name": "ok", "sound": data, "volume": 0.5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ModifySoundboardSound(t.Context(), GuildID, sound.SoundID, discord.Payload{"sound": data}); err == nil {
		t.Error("changing the sound succeeded")
	}
	if got, err := c.ModifySoundboardSound(t.Context(), GuildID, sound.SoundID, discord.Payload{"volume": nil}); err != nil || got.Volume != 1 {
		t.Errorf("volume reset: %+v, %v", got, err)
	}
}

func TestValidSound(t *testing.T) {
	for _, tt := range []struct {
		name, data string
		want       bool
	}{
		{"mp3 frame", "data:audio/mpeg;base64,//sYwA==", true},
		{"mp3 frame after id3 tag", "data:audio/mpeg;base64,SUQzBAAAAAAAAP/7GMA=", true},
		{"ogg opus", "data:audio/ogg;base64,T2dnUwAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAE9wdXNIZWFk", true},
		{"id3 tag only", "data:audio/mpeg;base64,SUQzBAAAAAAAAA==", false},
		{"truncated id3 tag", "data:audio/mpeg;base64,SUQzBAAAAAAAAf/7GMA=", false},
		{"ogg signature only", "data:audio/ogg;base64,T2dnUw==", false},
		{"free bitrate", "data:audio/mpeg;base64,//sIwA==", false},
		{"not base64", "data:audio/mpeg;base64,!!", false},
		{"no comma", "data:audio/mpeg", false},
	} {
		if got := validSound(tt.data); got != tt.want {
			t.Errorf("%s: validSound = %t, want %t", tt.name, got, tt.want)
		}
	}
}
