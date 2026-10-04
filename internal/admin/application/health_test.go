package application

import (
	"context"
	"errors"
	"testing"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
)

type fakeProbe struct{ details []bool }

func (f *fakeProbe) Check(_ context.Context, details bool) []ports.ServiceHealth {
	f.details = append(f.details, details)
	return []ports.ServiceHealth{{Service: "ledger-service", Ready: true}}
}

type fakeFeed struct{ err error }

func (f fakeFeed) Feed(context.Context) (ports.FeedStatus, error) {
	return ports.FeedStatus{State: "OK", Followed: []string{"BTC-USDT"}}, f.err
}

func TestHealthWithDetails(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "aud@example.com", domain.RoleAuditor)
	p := h.login(t, "aud@example.com")
	probe := &fakeProbe{}
	h.svc.Probe, h.svc.Market = probe, fakeFeed{}

	rep, err := h.svc.Health(ctx, p, false)
	if err != nil || len(rep.Services) != 1 || rep.Feed != nil || probe.details[0] {
		t.Fatalf("plain %+v %v", rep, err)
	}
	rep, err = h.svc.Health(ctx, p, true)
	if err != nil || rep.Feed == nil || rep.Feed.State != "OK" || !probe.details[1] {
		t.Fatalf("details %+v %v", rep, err)
	}
	h.svc.Market = fakeFeed{err: errors.New("market-data-service is down")}
	if rep, err = h.svc.Health(ctx, p, true); err != nil || rep.Feed != nil || len(rep.Services) != 1 {
		t.Fatalf("the feed left out %+v %v", rep, err)
	}
}
