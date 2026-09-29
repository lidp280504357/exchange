// Package grpcapi serves instrument-service's gRPC API.
package grpcapi

import (
	"context"

	instrumentv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/instrument/v1"
	"github.com/lidp280504357/exchange/internal/instrument/application"
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
	return &instrumentv1.GetAssetResponse{Asset: application.ToProtoAsset(a.Asset, a.Networks)}, nil
}

// ListAssets returns every asset.
func (s *Server) ListAssets(ctx context.Context, _ *instrumentv1.ListAssetsRequest) (*instrumentv1.ListAssetsResponse, error) {
	list, err := s.svc.Assets(ctx)
	if err != nil {
		return nil, err
	}
	resp := &instrumentv1.ListAssetsResponse{}
	for _, a := range list {
		resp.Assets = append(resp.Assets, application.ToProtoAsset(a.Asset, a.Networks))
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
