package kafka

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/skill/exchange/internal/platform/testenv"
)

// Services starting together race to make the same local topic: every one
// of them returns only once the topic has a leader, the losers too (B206).
func TestEnsureLocalRace(t *testing.T) {
	cfg := Config{Brokers: testenv.KafkaBrokers(t), Namespace: testenv.Name("ns") + "."}
	topic := cfg.Namespace + testenv.Name("race") + ".events"
	cl, err := kgo.NewClient(kgo.SeedBrokers(cfg.Brokers...))
	if err != nil {
		t.Fatal(err)
	}
	adm := kadm.NewClient(cl)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = adm.DeleteTopics(ctx, topic)
		cl.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	quiet := slog.New(slog.DiscardHandler)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Go(func() { errs[i] = ensureLocal(ctx, cfg, quiet, topic) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	details, err := adm.ListTopics(ctx, topic)
	if err != nil || !led(details, []string{topic}) || len(details[topic].Partitions) != localPartitions {
		t.Fatalf("the topic after the race: %+v %v", details[topic], err)
	}
}
