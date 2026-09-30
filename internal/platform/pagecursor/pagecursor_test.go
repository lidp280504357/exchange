package pagecursor

import (
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	at := time.Date(2026, 9, 30, 12, 34, 56, 789012000, time.UTC)
	c := Encode(at, "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b")
	got, id, err := Decode(c)
	if err != nil || !got.Equal(at) || id != "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b" {
		t.Fatalf("%v %q %v", got, id, err)
	}
	if got, id, err := Decode(""); err != nil || !got.IsZero() || id != "" {
		t.Fatalf("empty: %v %q %v", got, id, err)
	}
	for _, bad := range []string{"!!", "bm9wZQ", Encode(at, "")} {
		if _, _, err := Decode(bad); err == nil {
			t.Errorf("%q decoded", bad)
		}
	}
}
