package domain

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNewUser(t *testing.T) {
	id := uuid.NewString()
	u, err := NewUser(id, " sg ", "en", "Asia/Singapore")
	if err != nil || u.Region != "SG" || u.Language != "en" || u.Timezone != "Asia/Singapore" || u.Status != StatusActive {
		t.Fatalf("valid: %+v %v", u, err)
	}
	for _, tc := range []struct{ id, region, lang, tz string }{
		{"nope", "SG", "", ""},
		{id, "SGP", "", ""},
		{id, "S1", "", ""},
		{id, "SG", "English!", ""},
		{id, "SG", "", "Mars/Olympus"},
	} {
		if _, err := NewUser(tc.id, tc.region, tc.lang, tc.tz); err == nil {
			t.Errorf("NewUser(%q, %q, %q, %q) accepted", tc.id, tc.region, tc.lang, tc.tz)
		}
	}
}

func TestFavorites(t *testing.T) {
	got, err := Favorites([]string{"btc-usdt", "ETH-USDT", "BTC-USDT", " BTC-USDT-PERP "})
	if err != nil || strings.Join(got, ",") != "BTC-USDT,ETH-USDT,BTC-USDT-PERP" {
		t.Fatalf("got %v %v", got, err)
	}
	if got, err := Favorites(nil); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty: %v %v", got, err)
	}
	for _, bad := range []string{"BTCUSDT", "BTC_USDT", "", "BTC-USDT-SWAP"} {
		if _, err := Favorites([]string{bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	many := make([]string, MaxFavorites+1)
	for i := range many {
		many[i] = "BTC-USDT"
	}
	if _, err := Favorites(many); err == nil {
		t.Error("more than the maximum accepted")
	}
}
