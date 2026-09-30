package domain

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// MaxFavorites bounds a user's favorite markets.
const MaxFavorites = 100

var favoriteRE = regexp.MustCompile(`^[A-Z0-9]{2,10}-[A-Z0-9]{2,10}(-PERP)?$`)

// Favorites checks a list of market symbols (pairs such as BTC-USDT,
// contracts such as BTC-USDT-PERP) and returns it upper-cased without
// repeats, in the given order. Symbols are not checked against the
// listing: a delisted market stays harmlessly in the list.
func Favorites(symbols []string) ([]string, error) {
	if len(symbols) > MaxFavorites {
		return nil, apperr.Invalid(fmt.Sprintf("at most %d favorites", MaxFavorites))
	}
	out := make([]string, 0, len(symbols))
	for _, s := range symbols {
		s = strings.ToUpper(strings.TrimSpace(s))
		if !favoriteRE.MatchString(s) {
			return nil, apperr.Invalid(fmt.Sprintf("%q is not a market symbol such as BTC-USDT", s))
		}
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out, nil
}
