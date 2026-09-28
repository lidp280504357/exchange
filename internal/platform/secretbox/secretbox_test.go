package secretbox

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func key(t *testing.T) string {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(k)
}

func TestSealAndOpen(t *testing.T) {
	b, err := New(key(t))
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("12345678901234567890")
	sealed := b.Seal(secret, []byte("user-1"))
	if bytes.Contains(sealed, secret) || bytes.Equal(sealed, b.Seal(secret, []byte("user-1"))) {
		t.Fatal("sealed values must hide the secret and differ each time")
	}
	if got, err := b.Open(sealed, []byte("user-1")); err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("open: %q, %v", got, err)
	}
	if _, err := b.Open(sealed, []byte("user-2")); err == nil {
		t.Fatal("a value sealed for one user must not open for another")
	}
	other, _ := New(key(t))
	if _, err := other.Open(sealed, []byte("user-1")); err == nil {
		t.Fatal("another key must not open it")
	}
	if _, err := b.Open([]byte{1, 2, 3}, nil); err == nil {
		t.Fatal("garbage must not open")
	}
	for _, bad := range []string{"", "not base64!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := New(bad); err == nil {
			t.Fatalf("key %q accepted", bad)
		}
	}
}
