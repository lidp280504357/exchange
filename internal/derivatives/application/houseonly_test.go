package application

import (
	"testing"

	"github.com/lidp280504357/exchange/internal/derivatives/domain"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

type houseFlags map[string]bool

func (f houseFlags) Enabled(key string, _ flags.Subject) bool { return f[key] }

// Only a contract whose index pair follows a reference market trades with
// HOUSE alone: the platform coin's perpetual has no HOUSE quotes, so its
// orders match each other whatever the flags say.
func TestHouseOnlyNeedsAFollowedIndex(t *testing.T) {
	s := &Service{Features: houseFlags{flags.KeyHouseLiquidity: true}}
	if !s.houseOnly(domain.Contract{Symbol: "BTC-USDT-PERP", Followed: true}) {
		t.Fatal("a followed contract with HOUSE liquidity")
	}
	if s.houseOnly(domain.Contract{Symbol: "ASTRA-USDT-PERP"}) {
		t.Fatal("the platform coin's perpetual went to HOUSE")
	}
	s.Features = houseFlags{flags.KeyHouseLiquidity: true, flags.KeyInternalMatching: true}
	if s.houseOnly(domain.Contract{Symbol: "BTC-USDT-PERP", Followed: true}) {
		t.Fatal("users matching each other")
	}
}
