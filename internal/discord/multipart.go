package discord

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"os"
)

// MaxRequestSize is the largest request body Discord accepts, 25 MiB.
const MaxRequestSize = 25 << 20

// ErrRequestTooLarge is returned, wrapped, for request bodies or files over
// MaxRequestSize.
var ErrRequestTooLarge = errors.New("larger than Discord's 25 MiB request limit")

// Multipart is a multipart/form-data request body for endpoints that upload
// files. Pass a *Multipart as the request body.
type Multipart struct {
	// Payload holds the endpoint's other parameters. When non-nil it is sent
	// JSON-encoded in the payload_json part.
	Payload any
	Files   []File
}

// File is one file part of a multipart request.
type File struct {
	// Field is the form field name the endpoint expects: "files[n]" for
	// message attachments, where n is the attachment's placeholder ID in the
	// payload, or "file" for stickers.
	Field string
	// Name is the file name Discord stores.
	Name string
	// ContentType defaults to application/octet-stream.
	ContentType string
	Data        []byte
}

// encode returns the multipart body and its Content-Type, which carries the
// boundary.
func (m *Multipart) encode() ([]byte, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if m.Payload != nil {
		payload, err := json.Marshal(m.Payload)
		if err != nil {
			return nil, "", fmt.Errorf("encoding payload_json: %w", err)
		}
		part, err := w.CreatePart(textproto.MIMEHeader{
			"Content-Disposition": {`form-data; name="payload_json"`},
			"Content-Type":        {"application/json"},
		})
		if err != nil {
			return nil, "", err
		}
		_, _ = part.Write(payload)
	}
	for _, f := range m.Files {
		contentType := f.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		part, err := w.CreatePart(textproto.MIMEHeader{
			"Content-Disposition": {multipart.FileContentDisposition(f.Field, f.Name)},
			"Content-Type":        {contentType},
		})
		if err != nil {
			return nil, "", err
		}
		_, _ = part.Write(f.Data)
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

// FileContent returns the content of a file given either a local path or a
// base64-encoded value. Exactly one of them must be non-empty. Content over
// MaxRequestSize is rejected; a file is checked before it is read.
func FileContent(path, base64Value string) ([]byte, error) {
	switch {
	case path != "" && base64Value != "":
		return nil, errors.New("set either a file path or base64 content, not both")
	case path != "":
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if info.Size() > MaxRequestSize {
			return nil, fmt.Errorf("file %s is %d bytes, %w", path, info.Size(), ErrRequestTooLarge)
		}
		return os.ReadFile(path)
	case base64Value != "":
		data, err := base64.StdEncoding.DecodeString(base64Value)
		if err != nil {
			return nil, fmt.Errorf("decoding base64 content: %w", err)
		}
		if len(data) > MaxRequestSize {
			return nil, fmt.Errorf("base64 content is %d bytes, %w", len(data), ErrRequestTooLarge)
		}
		return data, nil
	default:
		return nil, errors.New("set a file path or base64 content")
	}
}
