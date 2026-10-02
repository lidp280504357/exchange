// Package grpcapi serves instrument-service's gRPC API.
package grpcapi

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	instrumentv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/instrument/v1"
	"github.com/lidp280504357/exchange/internal/instrument/application"
	"github.com/lidp280504357/exchange/internal/instrument/domain"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Server implements instrumentv1.InstrumentServiceServer.
type Server struct {
	instrumentv1.UnimplementedInstrumentServiceServer
	svc *application.Service
}

// NewServer returns the gRPC API over svc.
func NewServer(svc *application.Service) *Server { return &Server{svc: svc} }

// GetAsset returns an asset with its networks.
func (s *Server) GetAsset(ctx context.Context, req *instrumentv1.GetAssetRequest) (*instrumentv1.GetAssetResponse, error) {
	a, err := s.svc.Asset(ctx, req.GetAssetCode())
	if err != nil {
		return nil, err
	}
	return &instrumentv1.GetAssetResponse{Asset: assetOf(a)}, nil
}

// ListAssets returns every asset.
func (s *Server) ListAssets(ctx context.Context, _ *instrumentv1.ListAssetsRequest) (*instrumentv1.ListAssetsResponse, error) {
	list, err := s.svc.Assets(ctx)
	if err != nil {
		return nil, err
	}
	resp := &instrumentv1.ListAssetsResponse{}
	for _, a := range list {
		resp.Assets = append(resp.Assets, assetOf(a))
	}
	return resp, nil
}

func assetOf(a application.AssetView) *instrumentv1.Asset {
	out := application.ToProtoAsset(a.Asset, a.Networks)
	out.Profile = application.ToProtoProfile(a.Profile)
	return out
}

// UpdateAssetProfile changes an asset's profile for an operator.
func (s *Server) UpdateAssetProfile(ctx context.Context, req *instrumentv1.UpdateAssetProfileRequest) (*instrumentv1.UpdateAssetProfileResponse, error) {
	ch := application.ProfileChange{
		DisplayName: req.GetDisplayName(), Description: req.GetDescription(), Links: req.GetLinks(), ClearLogo: req.GetClearLogo(),
	}
	if len(req.GetLogo()) > 0 {
		ch.Logo = &domain.Logo{Data: req.GetLogo(), MIME: req.GetLogoMime()}
	}
	p, err := s.svc.UpdateProfile(ctx, strings.ToUpper(req.GetAssetCode()), ch, req.GetActor(), req.GetReason())
	if err != nil {
		return nil, err
	}
	return &instrumentv1.UpdateAssetProfileResponse{Profile: application.ToProtoProfile(p)}, nil
}

// ExportConfig returns the reference data as a config document.
func (s *Server) ExportConfig(ctx context.Context, _ *instrumentv1.ExportConfigRequest) (*instrumentv1.ExportConfigResponse, error) {
	cfg, err := s.svc.Export(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	return &instrumentv1.ExportConfigResponse{ConfigJson: raw}, nil
}

// ApplyConfig applies a config document for the admin console.
func (s *Server) ApplyConfig(ctx context.Context, req *instrumentv1.ApplyConfigRequest) (*instrumentv1.ApplyConfigResponse, error) {
	var cfg application.Config
	dec := json.NewDecoder(bytes.NewReader(req.GetConfigJson()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, apperr.Invalid("the config is not a valid document: " + err.Error())
	}
	if strings.TrimSpace(req.GetActor()) == "" {
		return nil, apperr.Invalid("the actor is required")
	}
	res, err := s.svc.ApplyWith(ctx, cfg, application.ApplyOptions{
		Actor: req.GetActor(), Reason: req.GetReason(), Source: application.SourceConsole, DryRun: req.GetDryRun(),
	})
	if err != nil {
		return nil, err
	}
	resp := &instrumentv1.ApplyConfigResponse{Unchanged: int32(res.Unchanged)} //nolint:gosec // a document has far fewer items
	for _, c := range res.Changes {
		after, err := json.Marshal(c.After)
		if err != nil {
			return nil, err
		}
		var before []byte
		if c.Before != nil {
			if before, err = json.Marshal(c.Before); err != nil {
				return nil, err
			}
		}
		resp.Changes = append(resp.Changes, &instrumentv1.ConfigChange{
			Entity: c.Entity, Key: c.Key, Action: c.Action, Version: c.Version, BeforeJson: before, AfterJson: after,
		})
	}
	return resp, nil
}

// GetTradingPair returns a pair with its fee rates.
func (s *Server) GetTradingPair(ctx context.Context, req *instrumentv1.GetTradingPairRequest) (*instrumentv1.GetTradingPairResponse, error) {
	p, err := s.svc.Pair(ctx, req.GetSymbol())
	if err != nil {
		return nil, err
	}
	return &instrumentv1.GetTradingPairResponse{Pair: application.ToProtoPair(p)}, nil
}

// ListTradingPairs returns every pair.
func (s *Server) ListTradingPairs(ctx context.Context, _ *instrumentv1.ListTradingPairsRequest) (*instrumentv1.ListTradingPairsResponse, error) {
	list, err := s.svc.Pairs(ctx)
	if err != nil {
		return nil, err
	}
	resp := &instrumentv1.ListTradingPairsResponse{}
	for _, p := range list {
		resp.Pairs = append(resp.Pairs, application.ToProtoPair(p))
	}
	return resp, nil
}

// SetPairStatus moves a trading pair to another status for an operator.
func (s *Server) SetPairStatus(ctx context.Context, req *instrumentv1.SetPairStatusRequest) (*instrumentv1.SetPairStatusResponse, error) {
	from, err := s.svc.SetPairStatus(ctx, req.GetSymbol(), req.GetToStatus(), req.GetActor(), req.GetReason())
	if err != nil {
		return nil, err
	}
	return &instrumentv1.SetPairStatusResponse{FromStatus: from}, nil
}

// GetContract returns a perpetual contract with its fee rates.
func (s *Server) GetContract(ctx context.Context, req *instrumentv1.GetContractRequest) (*instrumentv1.GetContractResponse, error) {
	c, err := s.svc.Contract(ctx, req.GetSymbol())
	if err != nil {
		return nil, err
	}
	return &instrumentv1.GetContractResponse{Contract: application.ToProtoContract(c)}, nil
}

// ListContracts returns every contract.
func (s *Server) ListContracts(ctx context.Context, _ *instrumentv1.ListContractsRequest) (*instrumentv1.ListContractsResponse, error) {
	list, err := s.svc.Contracts(ctx)
	if err != nil {
		return nil, err
	}
	resp := &instrumentv1.ListContractsResponse{}
	for _, c := range list {
		resp.Contracts = append(resp.Contracts, application.ToProtoContract(c))
	}
	return resp, nil
}

// SetContractStatus moves a contract to another status for an operator.
func (s *Server) SetContractStatus(ctx context.Context, req *instrumentv1.SetContractStatusRequest) (*instrumentv1.SetContractStatusResponse, error) {
	from, err := s.svc.SetContractStatus(ctx, req.GetSymbol(), req.GetToStatus(), req.GetActor(), req.GetReason())
	if err != nil {
		return nil, err
	}
	return &instrumentv1.SetContractStatusResponse{FromStatus: from}, nil
}
