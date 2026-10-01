package kafka

import (
	"context"
	"fmt"
	"strings"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/lidp280504357/exchange/internal/platform/event"
)

// ReadToEnd hands handle, in batches of at most maxBatch, the records of
// topic from the given offsets up to the end each partition had when it
// was called, without a consumer group, and returns how many it handed
// over. from holds, per partition, the next offset to read; a partition it
// does not list, or lists before its first retained record, starts at that
// record. Records that cannot be decoded are skipped (the consumer group
// that reads the topic parks them). A handler error ends the read.
//
// The matching engine catches up on its reference books with it before it
// consumes commands (ADR-0015).
func ReadToEnd(ctx context.Context, cfg Config, topic string, from map[int32]int64, maxBatch int, handle BatchHandler) (int, error) {
	if maxBatch <= 0 {
		maxBatch = 500
	}
	name := cfg.Namespace + topic
	start, end, err := bounds(ctx, cfg, name)
	if err != nil {
		return 0, err
	}
	// next tracks, per partition, the offset still to read up to its end.
	next, offsets := map[int32]int64{}, map[int32]kgo.Offset{}
	for p, e := range end {
		n := start[p]
		if f, ok := from[p]; ok && f > n {
			n = f
		}
		if n < e {
			next[p] = n
			offsets[p] = kgo.NewOffset().At(n)
		}
	}
	if len(next) == 0 {
		return 0, nil
	}
	cl, err := kgo.NewClient(kgo.SeedBrokers(cfg.Brokers...),
		kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{name: offsets}),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", name, err)
	}
	defer cl.Close()
	handed := 0
	var batch []Delivery
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := handle(ctx, batch); err != nil {
			return err
		}
		handed += len(batch)
		batch = batch[:0]
		return nil
	}
	for len(next) > 0 {
		fetches := cl.PollFetches(ctx)
		if err := ctx.Err(); err != nil {
			return handed, fmt.Errorf("read %s: %w", name, err)
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			return handed, fmt.Errorf("read %s: fetch: %w", name, errs[0].Err)
		}
		var herr error
		fetches.EachRecord(func(r *kgo.Record) {
			if _, want := next[r.Partition]; !want || r.Offset >= end[r.Partition] || herr != nil {
				return
			}
			if env, _, err := event.Decode(r.Value); err == nil {
				batch = append(batch, Delivery{Topic: strings.TrimPrefix(r.Topic, cfg.Namespace), Partition: r.Partition, Offset: r.Offset, Envelope: env})
			}
			next[r.Partition] = r.Offset + 1
			if next[r.Partition] >= end[r.Partition] {
				delete(next, r.Partition)
			}
			if len(batch) >= maxBatch {
				herr = flush()
			}
		})
		if herr != nil {
			return handed, herr
		}
	}
	return handed, flush()
}

// bounds lists, per partition of topic, its first retained offset and its
// end.
func bounds(ctx context.Context, cfg Config, topic string) (start, end map[int32]int64, err error) {
	cl, err := kgo.NewClient(kgo.SeedBrokers(cfg.Brokers...))
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", topic, err)
	}
	defer cl.Close()
	adm := kadm.NewClient(cl)
	starts, err := adm.ListStartOffsets(ctx, topic)
	if err == nil {
		err = starts.Error()
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: start offsets: %w", topic, err)
	}
	ends, err := adm.ListEndOffsets(ctx, topic)
	if err == nil {
		err = ends.Error()
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: end offsets: %w", topic, err)
	}
	start, end = map[int32]int64{}, map[int32]int64{}
	ends.Each(func(o kadm.ListedOffset) {
		end[o.Partition] = o.Offset
		if s, ok := starts.Lookup(topic, o.Partition); ok {
			start[o.Partition] = s.Offset
		}
	})
	return start, end, nil
}
