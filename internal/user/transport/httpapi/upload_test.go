package httpapi

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/user/domain"
)

// upload is a multipart request with a part named field holding data.
func upload(t *testing.T, field string, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("note", "ignored"); err != nil {
		t.Fatal(err)
	}
	part, err := w.CreateFormFile(field, "a.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(data)
	_ = w.Close()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/user/avatar", &body)
	r.Header.Set("Content-Type", w.FormDataContentType())
	return r
}

// The file part is read whatever comes before it; none, or not multipart,
// is 400; more than 5 MB is USER_AVATAR_TOO_LARGE, answered 413.
func TestReadingAnUpload(t *testing.T) {
	data, err := readUpload(upload(t, "file", []byte("png bytes")))
	if err != nil || string(data) != "png bytes" {
		t.Fatalf("read %q %v", data, err)
	}
	if _, err := readUpload(upload(t, "picture", []byte("x"))); apperr.From(err).Code != apperr.CodeInvalidArgument {
		t.Fatalf("no file part: %v", err)
	}
	plain := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/user/avatar", strings.NewReader("{}"))
	plain.Header.Set("Content-Type", "application/json")
	if _, err := readUpload(plain); apperr.From(err).Code != apperr.CodeInvalidArgument {
		t.Fatalf("not multipart: %v", err)
	}
	if _, err := readUpload(upload(t, "file", make([]byte, domain.MaxAvatarBytes+1))); !errors.Is(err, domain.ErrAvatarTooLarge) {
		t.Fatalf("too large: %v", err)
	}
	// Past the request's limit while reading the parts.
	big := upload(t, "file", make([]byte, maxUploadBytes+10))
	rec := httptest.NewRecorder()
	big.Body = http.MaxBytesReader(rec, big.Body, maxUploadBytes)
	if _, err := readUpload(big); !errors.Is(err, domain.ErrAvatarTooLarge) {
		t.Fatalf("past the request's limit: %v", err)
	}
	writeTooLarge(rec, big)
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "USER_AVATAR_TOO_LARGE") {
		t.Fatalf("answer %d %s", rec.Code, rec.Body.String())
	}
}
