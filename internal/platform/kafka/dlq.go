package kafka

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/lidp280504357/exchange/internal/platform/event"
)

// HeaderReplayedFrom marks a record republished from a dead-letter topic
// with its "partition:offset" there.
const HeaderReplayedFrom = "x-replayed-from"

// DLQRecord is a record parked in a dead-letter topic (requirements §8.2).
type DLQRecord struct {
	Partition int32
	Offset    int64
	// Origin is the business topic and Group the consumer group that gave
	// up on the record, both without the namespace.
	Origin    string
	Group     string
	Attempt   int
	Error     string
	EventID   string // empty when the record cannot be decoded
	EventType string
	ParkedAt  time.Time
	record    *kgo.Record
}

// ReadDLQ returns the records of topic's dead-letter topic ("<topic>.dlq")
// that exist when it is called, oldest first per partition.
func ReadDLQ(ctx context.Context, cfg Config, topic string) ([]DLQRecord, error) {
	dlq := cfg.Namespace + topic + ".dlq"
	cl, err := kgo.NewClient(kgo.SeedBrokers(cfg.Brokers...), kgo.ConsumeTopics(dlq),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		return nil, fmt.Errorf("dlq %s: %w", dlq, err)
	}
	defer cl.Close()
	adm := kadm.NewClient(cl)
	starts, err := adm.ListStartOffsets(ctx, dlq)
	if err == nil {
		err = starts.Error()
	}
	if err != nil {
		return nil, fmt.Errorf("dlq %s: start offsets: %w", dlq, err)
	}
	ends, err := adm.ListEndOffsets(ctx, dlq)
	if err == nil {
		err = ends.Error()
	}
	if err != nil {
		return nil, fmt.Errorf("dlq %s: end offsets: %w", dlq, err)
	}
	// next tracks, per partition, the offset still to read up to its end.
	next, end := map[int32]int64{}, map[int32]int64{}
	ends.Each(func(o kadm.ListedOffset) {
		if s, ok := starts.Lookup(dlq, o.Partition); ok && s.Offset < o.Offset {
			next[o.Partition], end[o.Partition] = s.Offset, o.Offset
		}
	})
	var out []DLQRecord
	for len(next) > 0 {
		fetches := cl.PollFetches(ctx)
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("dlq %s: %w", dlq, err)
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			return nil, fmt.Errorf("dlq %s: fetch: %w", dlq, errs[0].Err)
		}
		fetches.EachRecord(func(r *kgo.Record) {
			if _, want := next[r.Partition]; !want || r.Offset >= end[r.Partition] {
				return
			}
			out = append(out, dlqRecord(r, cfg.Namespace))
			next[r.Partition] = r.Offset + 1
			if next[r.Partition] >= end[r.Partition] {
				delete(next, r.Partition)
			}
		})
	}
	return out, nil
}

func dlqRecord(r *kgo.Record, ns string) DLQRecord {
	d := DLQRecord{
		Partition: r.Partition, Offset: r.Offset,
		Origin: strings.TrimPrefix(header(r, HeaderOriginTopic), ns), Group: strings.TrimPrefix(header(r, HeaderGroup), ns),
		Error: header(r, HeaderError), ParkedAt: r.Timestamp, record: r,
	}
	d.Attempt, _ = strconv.Atoi(header(r, HeaderAttempt))
	if env, _, err := event.Decode(r.Value); err == nil {
		d.EventID, d.EventType = env.GetEventId(), env.GetEventType()
	}
	return d
}

// ReplayDLQ republishes dead letters to their origin's retry topic as a
// fresh first attempt for the group that parked them, so only that group
// handles them again and a new failure goes through the whole retry cycle
// before parking again. Handlers are idempotent (inbox, keyed writes), so
// replaying a record twice is harmless. Records that cannot be decoded
// are refused: replaying them cannot succeed.
func ReplayDLQ(ctx context.Context, cfg Config, recs []DLQRecord) (int, error) {
	cl, err := kgo.NewClient(kgo.SeedBrokers(cfg.Brokers...), kgo.RequiredAcks(kgo.AllISRAcks()))
	if err != nil {
		return 0, fmt.Errorf("dlq replay: %w", err)
	}
	defer cl.Close()
	n := 0
	for _, d := range recs {
		if d.EventID == "" {
			return n, fmt.Errorf("dlq replay: %d:%d cannot be decoded; it can only be inspected", d.Partition, d.Offset)
		}
		if d.Origin == "" || d.Group == "" {
			return n, fmt.Errorf("dlq replay: %d:%d lacks its origin or group", d.Partition, d.Offset)
		}
		ns := cfg.Namespace
		out := &kgo.Record{
			Topic: ns + d.Origin + ".retry",
			Key:   d.record.Key,
			Value: d.record.Value,
			Headers: []kgo.RecordHeader{
				{Key: HeaderOriginTopic, Value: []byte(ns + d.Origin)},
				{Key: HeaderGroup, Value: []byte(ns + d.Group)},
				{Key: HeaderAttempt, Value: []byte("0")},
				{Key: HeaderNotBefore, Value: []byte(strconv.FormatInt(time.Now().UnixMilli(), 10))},
				{Key: HeaderReplayedFrom, Value: []byte(fmt.Sprintf("%d:%d", d.Partition, d.Offset))},
			},
		}
		if err := cl.ProduceSync(ctx, out).FirstErr(); err != nil {
			return n, fmt.Errorf("dlq replay %d:%d: %w", d.Partition, d.Offset, err)
		}
		n++
	}
	return n, nil
}

// ErrNoSuchDeadLetter is returned when a selected record is not in the
// dead-letter topic.
var ErrNoSuchDeadLetter = errors.New("no such dead letter")
