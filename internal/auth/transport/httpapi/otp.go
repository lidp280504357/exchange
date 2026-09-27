// Package httpapi serves auth-service's REST endpoints (api/openapi/auth.yaml).
package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/lidp280504357/exchange/internal/auth/application"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
)

// Handler serves the auth endpoints.
type Handler struct {
	OTP *application.OTPService
}

// Routes mounts the endpoints on r.
func (h *Handler) Routes(r chi.Router) {
	r.Post("/v1/auth/otp/request", h.requestOTP)
	r.Post("/v1/auth/otp/verify", h.verifyOTP)
}

type otpRequestBody struct {
	Scene            string `json:"scene"`
	Channel          string `json:"channel"`
	Identifier       string `json:"identifier"`
	LoginChallengeID string `json:"login_challenge_id"`
	CaptchaToken     string `json:"captcha_token"`
	DeviceID         string `json:"device_id"`
	Language         string `json:"language"`
}

type challengeResponse struct {
	ChallengeID string `json:"challenge_id"`
	ExpiresAt   string `json:"expires_at"`
	Delivery    string `json:"delivery"`
}

func (h *Handler) requestOTP(w http.ResponseWriter, r *http.Request) {
	var body otpRequestBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	view, err := h.OTP.Request(r.Context(), application.RequestOTP{
		Scene:            body.Scene,
		Channel:          body.Channel,
		Identifier:       body.Identifier,
		LoginChallengeID: body.LoginChallengeID,
		CaptchaToken:     body.CaptchaToken,
		DeviceID:         body.DeviceID,
		Language:         httpx.Language(r, body.Language),
		IP:               httpx.ClientIPFrom(r.Context()),
		UserID:           httpx.UserID(r),
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, challengeResponse{
		ChallengeID: view.ChallengeID, ExpiresAt: httpx.FormatTime(view.ExpiresAt), Delivery: view.Delivery,
	})
}

type otpVerifyBody struct {
	ChallengeID string `json:"challenge_id"`
	Code        string `json:"code"`
	DeviceID    string `json:"device_id"`
}

type ticketResponse struct {
	OTPTicket string `json:"otp_ticket"`
	Scene     string `json:"scene"`
	ExpiresAt string `json:"expires_at"`
}

func (h *Handler) verifyOTP(w http.ResponseWriter, r *http.Request) {
	var body otpVerifyBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	view, err := h.OTP.Verify(r.Context(), application.VerifyOTP{
		ChallengeID: body.ChallengeID, Code: body.Code, DeviceID: body.DeviceID,
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ticketResponse{
		OTPTicket: view.Ticket, Scene: string(view.Scene), ExpiresAt: httpx.FormatTime(view.ExpiresAt),
	})
}
