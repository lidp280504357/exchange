package resend

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lidp280504357/exchange/internal/notification/domain"
)

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func reply(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}

func TestSendPostsTheMail(t *testing.T) {
	var got request
	var auth, idem string
	p := New(Config{APIKey: "re_test", From: "Exchange <noreply@astras.vip>"}, roundTripper(func(r *http.Request) (*http.Response, error) {
		auth, idem = r.Header.Get("Authorization"), r.Header.Get("Idempotency-Key")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		return reply(200, `{"id":"msg-1"}`), nil
	}))
	id, err := p.Send(context.Background(), domain.Message{
		Channel: domain.ChannelEmail, To: "a@mail.test",
		Subject: "code", Text: "123456", IdempotencyKey: "d-1",
	})
	if err != nil || id != "msg-1" {
		t.Fatalf("send: %q %v", id, err)
	}
	if auth != "Bearer re_test" || idem != "d-1" || got.From != "Exchange <noreply@astras.vip>" || got.To[0] != "a@mail.test" || got.Text != "123456" {
		t.Fatalf("request: auth=%q idem=%q body=%+v", auth, idem, got)
	}
}

func TestSendClassifiesFailures(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		err       error
		class     domain.FailureClass
		retryable bool
	}{
		{"network", 0, "", errors.New("dial tcp: i/o timeout"), domain.FailureTimeout, true},
		{"rate limited", 429, `{}`, nil, domain.FailureRejected, true},
		{"invalid address", 422, `{"name":"validation_error"}`, nil, domain.FailureInvalidTarget, false},
		{"bad key", 401, `{}`, nil, domain.FailureRejected, false},
		{"server error", 503, ``, nil, domain.FailureUnknown, true},
		{"garbled success", 200, `nope`, nil, domain.FailureUnknown, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := New(Config{APIKey: "k"}, roundTripper(func(*http.Request) (*http.Response, error) {
				if tc.err != nil {
					return nil, tc.err
				}
				return reply(tc.status, tc.body), nil
			}))
			_, err := p.Send(context.Background(), domain.Message{To: "a@mail.test"})
			var se *domain.SendError
			if !errors.As(err, &se) || se.Class != tc.class || se.Retryable != tc.retryable {
				t.Fatalf("got %v, want %s retryable=%v", err, tc.class, tc.retryable)
			}
		})
	}
}
