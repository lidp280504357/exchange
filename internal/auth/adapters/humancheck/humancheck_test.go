package humancheck

import (
	"context"
	"errors"
	"testing"

	"github.com/lidp280504357/exchange/internal/platform/captcha"
)

func TestBypassAndProvider(t *testing.T) {
	provider := captcha.Static{Token: "real-token", Hostname: "astras.vip"}
	v := New(provider, "e2e-bypass")
	ctx := context.Background()
	if err := v.Verify(ctx, "e2e-bypass", ""); err != nil {
		t.Fatalf("bypass: %v", err)
	}
	if err := v.Verify(ctx, "real-token", "203.0.113.9"); err != nil {
		t.Fatalf("provider: %v", err)
	}
	if err := v.Verify(ctx, "forged", ""); err == nil {
		t.Fatal("unknown tokens must fail")
	}
	if err := New(nil, "").Verify(ctx, "anything", ""); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("no provider: %v", err)
	}
	if err := New(provider, "").Verify(ctx, "", ""); err == nil {
		t.Fatal("an empty bypass must not match an empty token")
	}
}
