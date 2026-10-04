package application

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/skill/exchange/internal/wallet/domain"
)

// NetworksOf lists the networks an asset can be deposited or withdrawn on
// (every asset's when asset is empty), with what the deposit and
// withdrawal pages show of them.
func (s *Service) NetworksOf(ctx context.Context, asset string) ([]domain.Network, error) {
	return s.Networks.ForAsset(ctx, strings.ToUpper(asset))
}

// FeatureTestAssets is the eligibility a hidden test asset's networks need
// (ADR-0017).
const FeatureTestAssets = "TEST_ASSETS"

// NetworksFor lists an asset's networks (every asset's for "") as a user
// may see them: a hidden test asset's only to one eligible for
// TEST_ASSETS (ADR-0017).
func (s *Service) NetworksFor(ctx context.Context, userID, asset string) ([]domain.Network, error) {
	list, err := s.NetworksOf(ctx, asset)
	if err != nil || !slices.ContainsFunc(list, func(n domain.Network) bool { return n.Hidden }) || s.testAssets(ctx, userID) {
		return list, err
	}
	return slices.DeleteFunc(list, func(n domain.Network) bool { return n.Hidden }), nil
}

// visible refuses a hidden test asset's network to a user not eligible
// for TEST_ASSETS as if it did not exist (ADR-0017).
func (s *Service) visible(ctx context.Context, userID string, net domain.Network) error {
	if net.Hidden && !s.testAssets(ctx, userID) {
		return domain.ErrUnknownNetwork
	}
	return nil
}

// testAssetsKept is how long a user's eligibility for TEST_ASSETS is kept
// (review AQ: every list of networks asked user-service).
const testAssetsKept = time.Minute

type testAssetsAnswer struct {
	allowed bool
	until   time.Time
}

// testAssets reports whether userID is eligible for TEST_ASSETS, as
// user-service said in the last testAssetsKept. Not when it cannot say: the
// hidden assets are then as if they did not exist, and the other networks
// stay listed (review AQ: fail closed, a restart of user-service must not
// fail everyone's networks).
func (s *Service) testAssets(ctx context.Context, userID string) bool {
	now := s.Now()
	s.testAssetsMu.Lock()
	a, ok := s.testAssetsOf[userID]
	s.testAssetsMu.Unlock()
	if ok && now.Before(a.until) {
		return a.allowed
	}
	allowed, _, err := s.Eligibility.Check(ctx, userID, FeatureTestAssets)
	if err != nil {
		if s.Log != nil {
			s.Log.WarnContext(ctx, "test assets: eligibility unknown, hidden", "user_id", userID, "error", err)
		}
		return false
	}
	s.testAssetsMu.Lock()
	defer s.testAssetsMu.Unlock()
	if s.testAssetsOf == nil || len(s.testAssetsOf) >= 10_000 {
		s.testAssetsOf = map[string]testAssetsAnswer{} // the test accounts are few; anyone else's answer is cheap to ask again
	}
	s.testAssetsOf[userID] = testAssetsAnswer{allowed: allowed, until: now.Add(testAssetsKept)}
	return allowed
}

// AddressValidation is the outcome of checking a withdrawal address.
type AddressValidation struct {
	domain.AddressCheck
	Network domain.Network
	// Internal is set when the address is another user's deposit address:
	// the withdrawal becomes an internal transfer without a chain fee.
	Internal bool
}

// ValidateAddress checks an address for a network before the user saves
// or uses it: its form (domain.CheckAddress), whether it belongs to the
// user (refused like a withdrawal to oneself) or another user (an
// internal transfer), and on a custodian's network what the custodian
// says of it. asset picks the network's asset when several
// share it; empty takes the first.
func (s *Service) ValidateAddress(ctx context.Context, userID, asset, network, address, memo string) (AddressValidation, error) {
	asset, network = strings.ToUpper(asset), strings.ToUpper(network)
	var net domain.Network
	if asset != "" {
		n, err := s.Networks.Network(ctx, asset, network)
		if err != nil {
			return AddressValidation{}, err
		}
		net = n
	} else {
		nets, err := s.Networks.OnNetwork(ctx, network)
		if err != nil {
			return AddressValidation{}, err
		}
		if len(nets) == 0 {
			return AddressValidation{}, domain.ErrUnknownNetwork
		}
		net = nets[0]
	}
	if err := s.visible(ctx, userID, net); err != nil {
		return AddressValidation{}, err
	}
	out := AddressValidation{AddressCheck: domain.CheckAddress(net, address, memo), Network: net}
	if !out.Valid {
		return out, nil
	}
	owners, err := s.Store.Read().Addresses().Owners(ctx, network)
	if err != nil {
		return AddressValidation{}, err
	}
	retired, err := s.Store.Read().Addresses().Retired(ctx, network, out.Normalized)
	if err != nil {
		return AddressValidation{}, err
	}
	switch owner := owners[strings.ToLower(out.Normalized)]; {
	case owner == userID:
		out.Valid, out.Normalized, out.Reason = false, "", domain.ReasonAddressOwn
	case owner != "":
		out.Internal = true
	case retired:
		// The custodian's stand-in made it up: no chain would deliver.
		out.Valid, out.Normalized, out.Reason = false, "", domain.ReasonAddressRetired
	case net.Custody():
		// The custodian has the last word on its chains' addresses; when it
		// cannot be asked, the form alone decides.
		c := s.Custodians[net.Provider]
		if c == nil {
			break
		}
		ok, err := c.CheckAddress(ctx, net, out.Normalized)
		if err != nil {
			s.Log.WarnContext(ctx, "the custodian could not check an address", "network", net.Network, "error", err)
			break
		}
		if !ok {
			out.Valid, out.Normalized, out.Reason = false, "", domain.ReasonAddressNetwork
		}
	}
	return out, nil
}
