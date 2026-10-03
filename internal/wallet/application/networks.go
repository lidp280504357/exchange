package application

import (
	"context"
	"strings"

	"github.com/lidp280504357/exchange/internal/wallet/domain"
)

// NetworksOf lists the networks an asset can be deposited or withdrawn on
// (every asset's when asset is empty), with what the deposit and
// withdrawal pages show of them.
func (s *Service) NetworksOf(ctx context.Context, asset string) ([]domain.Network, error) {
	return s.Networks.ForAsset(ctx, strings.ToUpper(asset))
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
