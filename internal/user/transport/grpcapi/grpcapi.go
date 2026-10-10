// Package grpcapi serves user-service's gRPC API.
package grpcapi

import (
	"context"
	"slices"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	userv1 "github.com/skill/exchange/api/gen/go/exchange/user/v1"
	"github.com/skill/exchange/internal/user/application"
	"github.com/skill/exchange/internal/user/domain"
	"github.com/skill/exchange/internal/user/ports"
)

// Server implements userv1.UserServiceServer.
type Server struct {
	userv1.UnimplementedUserServiceServer
	svc *application.Service
}

// NewServer returns the gRPC API over svc.
func NewServer(svc *application.Service) *Server { return &Server{svc: svc} }

func toProto(u domain.User) *userv1.User {
	out := &userv1.User{
		Id: u.ID, Status: u.Status, Region: u.Region, Language: u.Language, Timezone: u.Timezone,
		KycLevel:         int32(u.KYCLevel), //nolint:gosec // small level number
		AntiPhishingCode: u.AntiPhishingCode, CreatedAt: timestamppb.New(u.CreatedAt),
		Username: u.Username, AvatarUrl: u.Avatar.URL(), AvatarThumbUrl: u.Avatar.ThumbURL(), Kind: u.Kind, PurgeExempt: u.PurgeExempt,
	}
	if !u.UsernameChangedAt.IsZero() {
		out.UsernameChangedAt = timestamppb.New(u.UsernameChangedAt)
	}
	if !u.PurgedAt.IsZero() {
		out.PurgedAt = timestamppb.New(u.PurgedAt)
	}
	return out
}

// ResetUsername gives an account a new drawn username for an operator.
func (s *Server) ResetUsername(ctx context.Context, req *userv1.ResetUsernameRequest) (*userv1.ResetUsernameResponse, error) {
	u, previous, err := s.svc.ResetUsername(ctx, req.GetUserId(), req.GetActor(), req.GetReason())
	if err != nil {
		return nil, err
	}
	return &userv1.ResetUsernameResponse{User: toProto(u), Previous: previous}, nil
}

// ResetAvatar takes an account back to the default avatar for an operator.
func (s *Server) ResetAvatar(ctx context.Context, req *userv1.ResetAvatarRequest) (*userv1.ResetAvatarResponse, error) {
	u, removed, err := s.svc.ResetAvatar(ctx, req.GetUserId(), req.GetActor(), req.GetReason())
	if err != nil {
		return nil, err
	}
	return &userv1.ResetAvatarResponse{User: toProto(u), Removed: removed}, nil
}

// CreateUser creates a profile idempotently.
func (s *Server) CreateUser(ctx context.Context, req *userv1.CreateUserRequest) (*userv1.CreateUserResponse, error) {
	u, err := s.svc.Create(ctx, application.CreateInput{
		UserID: req.GetUserId(), Region: req.GetRegion(), Language: req.GetLanguage(), Timezone: req.GetTimezone(),
		TermsVersion: req.GetTermsVersion(), RiskVersion: req.GetRiskDisclosureVersion(),
	})
	if err != nil {
		return nil, err
	}
	return &userv1.CreateUserResponse{User: toProto(u)}, nil
}

// GetUser returns a profile.
func (s *Server) GetUser(ctx context.Context, req *userv1.GetUserRequest) (*userv1.GetUserResponse, error) {
	u, err := s.svc.Get(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	return &userv1.GetUserResponse{User: toProto(u)}, nil
}

// CheckEligibility decides whether a user may use a feature now.
func (s *Server) CheckEligibility(ctx context.Context, req *userv1.CheckEligibilityRequest) (*userv1.CheckEligibilityResponse, error) {
	allowed, reason, err := s.svc.CheckEligibility(ctx, req.GetUserId(), req.GetFeature(), req.GetAsset(), req.GetSymbol())
	if err != nil {
		return nil, err
	}
	return &userv1.CheckEligibilityResponse{Allowed: allowed, ReasonCode: reason}, nil
}

// ChangeStatus moves an account to another status for an operator.
func (s *Server) ChangeStatus(ctx context.Context, req *userv1.ChangeStatusRequest) (*userv1.ChangeStatusResponse, error) {
	c, err := s.svc.ChangeStatus(ctx, req.GetUserId(), req.GetToStatus(), req.GetReasonCode(), req.GetActor(), req.GetNote())
	if err != nil {
		return nil, err
	}
	return &userv1.ChangeStatusResponse{FromStatus: c.From, ToStatus: c.To}, nil
}

// ListUsers pages through accounts newest first.
func (s *Server) ListUsers(ctx context.Context, req *userv1.ListUsersRequest) (*userv1.ListUsersResponse, error) {
	f := ports.UserFilter{
		Status: req.GetStatus(), Region: req.GetRegion(), Q: req.GetQ(), IDs: req.GetUserIds(), Kinds: req.GetKinds(), Limit: int(req.GetLimit()),
		IncludePurged: req.GetIncludePurged(),
	}
	if req.GetCreatedFrom() != nil {
		f.CreatedFrom = req.GetCreatedFrom().AsTime()
	}
	if req.GetCreatedBefore() != nil {
		f.CreatedBefore = req.GetCreatedBefore().AsTime()
	}
	page, err := s.svc.ListUsers(ctx, f, req.GetCursor())
	if err != nil {
		return nil, err
	}
	resp := &userv1.ListUsersResponse{NextCursor: page.Next}
	for _, u := range page.Users {
		resp.Users = append(resp.Users, toProto(u))
	}
	return resp, nil
}

// FindUsername returns the account of a username (B167).
func (s *Server) FindUsername(ctx context.Context, req *userv1.FindUsernameRequest) (*userv1.FindUsernameResponse, error) {
	id, err := s.svc.FindUsername(ctx, req.GetUsername())
	if err != nil {
		return nil, err
	}
	return &userv1.FindUsernameResponse{UserId: id}, nil
}

// UserStats counts accounts.
func (s *Server) UserStats(ctx context.Context, req *userv1.UserStatsRequest) (*userv1.UserStatsResponse, error) {
	var since time.Time
	if req.GetSince() != nil {
		since = req.GetSince().AsTime()
	}
	st, err := s.svc.UserStats(ctx, since, int(req.GetDays()), req.GetKinds())
	if err != nil {
		return nil, err
	}
	resp := &userv1.UserStatsResponse{Total: st.Total, CreatedSince: st.CreatedSince}
	days := make([]string, 0, len(st.Days))
	for d := range st.Days {
		days = append(days, d)
	}
	slices.Sort(days)
	for _, d := range days {
		resp.Days = append(resp.Days, &userv1.DayCount{Day: d, Count: st.Days[d]})
	}
	for _, k := range domain.Kinds {
		c := st.ByKind[k]
		resp.ByKind = append(resp.ByKind, &userv1.KindCount{Kind: k, Total: c.Total, CreatedSince: c.CreatedSince})
	}
	return resp, nil
}

// GetUserHistory returns an account's status changes and consents.
func (s *Server) GetUserHistory(ctx context.Context, req *userv1.GetUserHistoryRequest) (*userv1.GetUserHistoryResponse, error) {
	changes, consents, err := s.svc.History(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	resp := &userv1.GetUserHistoryResponse{}
	for _, c := range changes {
		resp.StatusChanges = append(resp.StatusChanges, &userv1.StatusChange{
			FromStatus: c.From, ToStatus: c.To, ReasonCode: c.Reason, Actor: c.Actor, At: timestamppb.New(c.At),
		})
	}
	for _, c := range consents {
		resp.Consents = append(resp.Consents, &userv1.Consent{Document: c.Document, Version: c.Version, AcceptedAt: timestamppb.New(c.AcceptedAt)})
	}
	return resp, nil
}
