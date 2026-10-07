package application

import (
	"context"
	"testing"

	"github.com/skill/exchange/internal/derivatives/domain"
	"github.com/skill/exchange/internal/platform/flags"
)

type houseFlags map[string]bool

func (f houseFlags) Enabled(key string, _ flags.Subject) bool { return f[key] }
func (f houseFlags) Closed(string) bool                       { return false }
func (f houseFlags) Get(string) (flags.Flag, bool)            { return flags.Flag{}, false }
func (f houseFlags) Refresh(context.Context) error            { return nil }

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
