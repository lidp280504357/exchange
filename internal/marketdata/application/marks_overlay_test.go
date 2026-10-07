package application

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// A price event that reaches risk has the perpetual's mark computed while
// it lasts; after it the reference market's mark returns as after its
// loss: once it streamed for referenceRecover (review GD, J0 contract §8
// question 2), not at once and not a fixed time later.
func TestAPriceEventComputesTheMarkUntilTheMarketStreamsAgain(t *testing.T) {
	r := newFollowRig(t, "2026-10-07T03:00:00Z")
	r.follow = true
	r.stream("60120", "60060", "0.00023", at("2026-10-07T08:00:00Z"))
	fl := &overlayFlags{}
	fl.on.Store(true)
	o := NewOverlay(fl, func(s string) bool { return s == perp.IndexSymbol }, prometheus.NewRegistry())
	o.now = func() time.Time { return r.now }
	r.marks.WithOverlay(o)
	r.tick(false)
	source := func() string {
		p, _ := r.marks.Latest(perp.Symbol)
		return p.Source
	}
	if source() != MarkSourceBinance {
		t.Fatalf("before: %s", source())
	}
	push := func(f string, seq int64) {
		t.Helper()
		if err := o.Set(perp.IndexSymbol, OverlayPush{Factor: d(f), Until: r.now.Add(4 * time.Second), Risk: true, EventID: "e1", Seq: seq}); err != nil {
			t.Fatal(err)
		}
	}
	push("1.1", 1)
	r.tick(false)
	if p, _ := r.marks.Latest(perp.Symbol); p.Source != MarkSourcePlatform || p.SourceDegraded {
		t.Fatalf("during: %+v", p)
	}
	push("1", 2)
	for i := range 4 {
		r.tick(false)
		if source() != MarkSourcePlatform {
			t.Fatalf("%d s after: %s", i+1, source())
		}
	}
	r.tick(false)
	r.tick(false)
	if source() != MarkSourceBinance {
		t.Fatalf("6 s after: %s", source())
	}
}
