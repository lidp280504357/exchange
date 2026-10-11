package kafka

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
)

// A local stack's topics (B205, the user 2026-10-11): with a namespace
// (KAFKA_NAMESPACE, scripts/dev.sh) each client makes the topics it is
// about to use when they are missing - one partition, kept a day and at
// most 256 MiB - so `task run -- SERVICE` makes that service's topics and
// no other's, and no second list says which service uses which. The test
// server's Redpanda bounds its partitions by its memory (2 GB): a copy of
// the deployed set with its partition counts (deploy/redpanda/topics.sh)
// was refused as INVALID_PARTITIONS. A topic already there is left as it
// is; one with another partition count is reported. Without a namespace -
// the deployed environments - nothing is made here: topics.sh makes theirs.
const (
	localPartitions     = 1
	localRetention      = 24 * time.Hour
	localRetentionBytes = 256 << 20
)

// ensureLocal makes the missing ones among topics (broker names) when cfg
// has a namespace, and does nothing without one.
func ensureLocal(ctx context.Context, cfg Config, log *slog.Logger, topics ...string) error {
	if cfg.Namespace == "" || len(topics) == 0 {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	cl, err := kgo.NewClient(kgo.SeedBrokers(cfg.Brokers...))
	if err != nil {
		return fmt.Errorf("kafka: local topics: %w", err)
	}
	defer cl.Close()
	adm := kadm.NewClient(cl)
	ms, size := strconv.FormatInt(localRetention.Milliseconds(), 10), strconv.Itoa(localRetentionBytes)
	resp, err := adm.CreateTopics(ctx, localPartitions, 1, map[string]*string{"retention.ms": &ms, "retention.bytes": &size}, topics...)
	if err != nil {
		return fmt.Errorf("kafka: create local topics: %w", err)
	}
	var there []string
	for _, t := range resp.Sorted() {
		switch {
		case t.Err == nil:
			log.Info("kafka: made a local topic", "topic", t.Topic, "partitions", localPartitions, "retention", localRetention.String())
		case errors.Is(t.Err, kerr.TopicAlreadyExists), errors.Is(t.Err, kerr.RequestTimedOut):
			// There, or being made (by another service in the same start,
			// or by a request that timed out): waited for below as well.
			there = append(there, t.Topic)
		default:
			return fmt.Errorf("kafka: create local topic %s: %w", t.Topic, t.Err)
		}
	}
	// Every topic asked for, not only those made here: the loser of a race
	// to make one reads it next too (B206).
	if err := awaitLeaders(ctx, adm, topics); err != nil {
		return err
	}
	if len(there) == 0 {
		return nil
	}
	details, err := adm.ListTopics(ctx, there...)
	if err != nil {
		log.Warn("kafka: local topics not checked", "error", err)
		return nil
	}
	for _, d := range details.Sorted() {
		if d.Err == nil && len(d.Partitions) != localPartitions {
			log.Warn("kafka: a local topic has another partition count; left as it is", "topic", d.Topic, "partitions", len(d.Partitions))
		}
	}
	return nil
}

// localLeaderWait bounds the wait for a new topic's leader.
const localLeaderWait = 10 * time.Second

// awaitLeaders waits until every partition of topics has a leader: a read
// of a new topic's offsets right after (the engines' catch-up on their
// reference books) fails until then.
func awaitLeaders(ctx context.Context, adm *kadm.Client, topics []string) error {
	if len(topics) == 0 {
		return nil
	}
	deadline := time.Now().Add(localLeaderWait)
	for {
		details, err := adm.ListTopics(ctx, topics...)
		if err == nil && led(details, topics) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("kafka: local topics %s have no leader after %s", strings.Join(topics, ", "), localLeaderWait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// led reports whether each topic's partitions all have a leader.
func led(details kadm.TopicDetails, topics []string) bool {
	for _, t := range topics {
		d, ok := details[t]
		if !ok || d.Err != nil || len(d.Partitions) == 0 {
			return false
		}
		for _, p := range d.Partitions {
			if p.Err != nil || p.Leader < 0 {
				return false
			}
		}
	}
	return true
}
