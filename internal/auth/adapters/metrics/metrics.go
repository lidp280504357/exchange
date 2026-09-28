// Package metrics exports auth-service's business counters to Prometheus.
package metrics

import "github.com/prometheus/client_golang/prometheus"

// OTP implements ports.OTPMetrics.
type OTP struct {
	requests      *prometheus.CounterVec
	verifications *prometheus.CounterVec
}

// NewOTP registers the OTP counters with reg.
func NewOTP(reg prometheus.Registerer) *OTP {
	m := &OTP{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "auth_otp_requests_total",
			Help: "OTP requests by scene, channel and outcome (queued codes are the send volume; decoys are unknown accounts).",
		}, []string{"scene", "channel", "outcome"}),
		verifications: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "auth_otp_verifications_total",
			Help: "OTP verifications by outcome.",
		}, []string{"outcome"}),
	}
	reg.MustRegister(m.requests, m.verifications)
	// Export every verification outcome from the start, so rates and
	// alerts see zero instead of a missing series.
	for _, outcome := range []string{"verified", "invalid", "expired", "attempts_exceeded", "error"} {
		m.verifications.WithLabelValues(outcome)
	}
	return m
}

// Requested counts an OTP request.
func (m *OTP) Requested(scene, channel, outcome string) {
	m.requests.WithLabelValues(scene, channel, outcome).Inc()
}

// Verified counts an OTP verification.
func (m *OTP) Verified(outcome string) { m.verifications.WithLabelValues(outcome).Inc() }
