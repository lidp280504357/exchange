package application_test

import (
	"context"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/skill/exchange/internal/derivatives/application"
)

// A contract under reduce-only while its mark price is fresh is observed
// (review C70): reduce-only is lifted by hand, and the alert
// DerivativesReduceOnlyWithFreshMark says when one has waited 10 minutes.
// A stale mark, or reduce-only lifted, is not.
func TestReduceOnlyWithAFreshMarkIsObserved(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	observed := func(want int) {
		t.Helper()
		n, err := r.svc.ObserveReduceOnly(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var m dto.Metric
		if err := r.svc.Metrics.ReduceOnlyFresh.WithLabelValues(perp.Symbol).Write(&m); err != nil {
			t.Fatal(err)
		}
		if gauge := m.GetGauge().GetValue(); n != want || gauge != float64(want) {
			t.Fatalf("observed %d, the gauge %v; want %d", n, gauge, want)
		}
	}
	observed(0)
	if err := r.svc.OnDegraded(ctx, perp.Symbol, application.ReasonMarkStale); err != nil {
		t.Fatal(err)
	}
	observed(1)
	r.book.Set(perp.Symbol, d("60000"), time.Now().Add(-time.Minute))
	observed(0)
	r.book.Set(perp.Symbol, d("60000"), time.Now())
	observed(1)
	if lifted, err := r.svc.LiftReduceOnly(ctx, perp.Symbol, "test"); err != nil || !lifted {
		t.Fatalf("lift: %v %v", lifted, err)
	}
	observed(0)
}
