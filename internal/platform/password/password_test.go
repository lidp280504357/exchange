package password

import (
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	h := NewHasher(2, Cost{MemoryKiB: 1024, Iterations: 1})
	enc := h.Hash("correct horse battery")
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=1024,t=1,p=1$") {
		t.Fatal(enc)
	}
	if ok, err := h.Verify(enc, "correct horse battery"); err != nil || !ok {
		t.Fatalf("verify %v %v", ok, err)
	}
	if ok, _ := h.Verify(enc, "wrong"); ok {
		t.Fatal("a wrong password")
	}
	if _, err := h.Verify("$bcrypt$x", "x"); err == nil {
		t.Fatal("another scheme")
	}
	h.VerifyDummy("anything")
}
