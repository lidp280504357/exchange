package backends

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthReadsTheServicesMetrics(t *testing.T) {
	consumer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/readyz":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/metrics":
			_, _ = w.Write([]byte(`# HELP exchange_build_info The build.
# TYPE exchange_build_info gauge
exchange_build_info{service="ledger-service",version="abc1234"} 1
kafka_consumer_lag{group="ledger",partition="0",topic="trade.events"} 3
kafka_consumer_lag{group="ledger",partition="1",topic="trade.events"} 4
kafka_consumer_records_total{group="ledger",result="ok",topic="trade.events"} 120
kafka_consumer_records_total{group="ledger",result="dlq",topic="trade.events"} 2
`))
		}
	}))
	defer consumer.Close()
	quiet := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			_, _ = w.Write([]byte("exchange_build_info{service=\"api-gateway\",version=\"abc1234\"} 1\n"))
		}
	}))
	defer quiet.Close()
	h := Health{Client: &http.Client{Timeout: time.Second}, Targets: []HealthTarget{
		{Service: "ledger-service", URL: consumer.URL}, {Service: "api-gateway", URL: quiet.URL}, {Service: "gone", URL: "http://127.0.0.1:1"},
	}}
	plain := h.Check(context.Background(), false)
	if !plain[0].Ready || plain[0].Version != "" || plain[0].KafkaLag != nil {
		t.Fatalf("without details %+v", plain[0])
	}
	got := h.Check(context.Background(), true)
	if l := got[0]; !l.Ready || l.Version != "abc1234" || l.KafkaLag == nil || *l.KafkaLag != 7 || l.DLQ == nil || *l.DLQ != 2 {
		t.Fatalf("ledger %+v", l)
	}
	if g := got[1]; g.Version != "abc1234" || g.KafkaLag != nil || g.DLQ != nil {
		t.Fatalf("a service without consumers %+v", g)
	}
	if gone := got[2]; gone.Ready || gone.Error != "unreachable" || gone.Version != "" {
		t.Fatalf("unreachable %+v", gone)
	}
}

func TestMetricLine(t *testing.T) {
	for _, c := range []struct {
		line  string
		name  string
		value float64
		ok    bool
	}{
		{`up 1`, "up", 1, true},
		{`kafka_consumer_lag{topic="a,b"} 5`, "kafka_consumer_lag", 5, true},
		{`# TYPE x gauge`, "", 0, false},
		{`broken{a="b" 1`, "", 0, false},
		{`noval{}`, "", 0, false},
	} {
		name, _, value, ok := metricLine(c.line)
		if name != c.name || value != c.value || ok != c.ok {
			t.Fatalf("%q: %q %v %v", c.line, name, value, ok)
		}
	}
}
