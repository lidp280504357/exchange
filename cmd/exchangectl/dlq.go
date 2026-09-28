package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/lidp280504357/exchange/internal/platform/kafka"
)

// offsetsFlag collects repeated --offset partition:offset values.
type offsetsFlag map[[2]int64]bool

func (o offsetsFlag) String() string { return fmt.Sprint(len(o)) }

func (o offsetsFlag) Set(s string) error {
	p, off, ok := strings.Cut(s, ":")
	pn, err1 := strconv.ParseInt(p, 10, 32)
	on, err2 := strconv.ParseInt(off, 10, 64)
	if !ok || err1 != nil || err2 != nil {
		return fmt.Errorf("want partition:offset, got %q", s)
	}
	o[[2]int64{pn, on}] = true
	return nil
}

// dlqCmd inspects and replays dead-letter topics (requirements §8.2: DLQ
// 有后台重放工具).
func dlqCmd(ctx context.Context, cfg settings, args []string, out io.Writer) error {
	if len(args) < 2 {
		fmt.Fprint(out, usage)
		return errUsage
	}
	if err := cfg.Kafka.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	topic := strings.TrimSuffix(args[1], ".dlq")
	recs, err := kafka.ReadDLQ(ctx, cfg.Kafka, topic)
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		return dlqList(recs, topic, out)
	case "replay":
		return dlqReplay(ctx, cfg, recs, topic, args[2:], out)
	default:
		return fmt.Errorf("unknown dlq command %q", args[0])
	}
}

func dlqList(recs []kafka.DLQRecord, topic string, out io.Writer) error {
	if len(recs) == 0 {
		fmt.Fprintf(out, "%s.dlq is empty\n", topic)
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "RECORD\tPARKED_AT\tGROUP\tATTEMPT\tEVENT\tERROR")
	for _, r := range recs {
		ev := r.EventType + " " + r.EventID
		if r.EventID == "" {
			ev = "(undecodable)"
		}
		msg := r.Error
		if len(msg) > 100 {
			msg = msg[:100] + "…"
		}
		fmt.Fprintf(w, "%d:%d\t%s\t%s\t%d\t%s\t%s\n", r.Partition, r.Offset, r.ParkedAt.UTC().Format(time.RFC3339), r.Group, r.Attempt, ev, msg)
	}
	return w.Flush()
}

func dlqReplay(ctx context.Context, cfg settings, recs []kafka.DLQRecord, topic string, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("dlq replay", flag.ContinueOnError)
	fs.SetOutput(out)
	all := fs.Bool("all", false, "replay every dead letter (of --group, when given)")
	group := fs.String("group", "", "only records parked by this consumer group")
	offsets := offsetsFlag{}
	fs.Var(offsets, "offset", "partition:offset of a record to replay (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *all == (len(offsets) > 0) {
		fs.Usage()
		return errors.New("give either --all or one or more --offset")
	}
	var chosen []kafka.DLQRecord
	for _, r := range recs {
		picked := *all || offsets[[2]int64{int64(r.Partition), r.Offset}]
		if *all && r.EventID == "" {
			picked = false // undecodable: inspect only
		}
		if picked && (*group == "" || r.Group == *group) {
			chosen = append(chosen, r)
			delete(offsets, [2]int64{int64(r.Partition), r.Offset})
		}
	}
	if len(offsets) > 0 {
		missing := make([]string, 0, len(offsets))
		for o := range offsets {
			missing = append(missing, fmt.Sprintf("%d:%d", o[0], o[1]))
		}
		slices.Sort(missing)
		return fmt.Errorf("%w in %s.dlq: %s", kafka.ErrNoSuchDeadLetter, topic, strings.Join(missing, ", "))
	}
	if len(chosen) == 0 {
		fmt.Fprintln(out, "nothing to replay")
		return nil
	}
	n, err := kafka.ReplayDLQ(ctx, cfg.Kafka, chosen)
	fmt.Fprintf(out, "replayed %d record(s) to %s.retry as first attempts of their groups\n", n, topic)
	return err
}
