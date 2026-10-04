package bootstrap

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/skill/exchange/internal/platform/app"
)

// ConfigPresent exports exchange_config_present{item} as 1 for each third
// party the service is configured for and 0 for the others (design
// 2026-10-04 §4.6): the admin console's launch checklist reads it through
// its health scrape. Only whether, never the value.
func ConfigPresent(a *app.App, items map[string]bool) error {
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "exchange_config_present",
		Help: "1 when the third party's account is configured, 0 when not; the value is never exported.",
	}, []string{"item"})
	if err := a.Metrics().Register(g); err != nil {
		return err
	}
	for item, present := range items {
		v := 0.0
		if present {
			v = 1
		}
		g.WithLabelValues(item).Set(v)
	}
	return nil
}
