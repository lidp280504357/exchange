package application

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/notification/domain"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/pii"
)

// OTPRequest is a SendOtp call.
type OTPRequest struct {
	ChallengeID string
	Channel     string
	Target      string
	Code        string
	Scene       string
	Language    string
	TTLSeconds  int
	UserID      string
}

// SendOTP delivers a one-time code. The code lives only in the message
// handed to the provider; the delivery record keeps the masked target.
func (d *Dispatcher) SendOTP(ctx context.Context, req OTPRequest) (string, domain.Status, error) {
	ch, err := domain.ParseChannel(req.Channel)
	if err != nil {
		return "", "", err
	}
	target := strings.TrimSpace(req.Target)
	if target == "" || len(req.Code) != 6 || strings.Trim(req.Code, "0123456789") != "" {
		return "", "", apperr.Invalid("target and a 6-digit code are required")
	}
	minutes := max(req.TTLSeconds/60, 1)
	id := uuid.Must(uuid.NewV7()).String()
	m := domain.OTPMessage(ch, target, req.Code, req.Scene, req.Language, minutes)
	m.IdempotencyKey = id
	del := domain.Delivery{
		ID:         id,
		Kind:       domain.KindOTP,
		Channel:    ch,
		Template:   "otp." + strings.ToLower(req.Scene),
		TargetMask: pii.MaskIdentifier(target),
		UserID:     req.UserID,
	}
	status, err := d.Deliver(ctx, del, m)
	return id, status, err
}
