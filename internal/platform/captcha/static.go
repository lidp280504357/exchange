package captcha

import "context"

// Static accepts exactly one pre-shared token and rejects everything else. It
// stands in for a real provider in unit tests and in local development when
// no provider secret is configured. Never enable it in a public environment.
type Static struct {
	Token    string
	Hostname string
}

// Verify implements Verifier.
func (s Static) Verify(_ context.Context, req Request) (Result, error) {
	if err := validateToken(req.Token); err != nil {
		return Result{}, err
	}
	if s.Token == "" || req.Token != s.Token {
		return Result{}, &VerificationError{Reason: "invalid-input-response", Codes: []string{"invalid-input-response"}}
	}
	return Result{Hostname: s.Hostname, Action: req.Action}, nil
}
