package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Stream timing: the counts are checked every eventsCheck, and a comment
// goes out after eventsQuiet without an event, so proxies (nginx reads
// for 60 s, Cloudflare 100 s) keep the stream open.
const (
	eventsCheck = 10 * time.Second
	eventsQuiet = 20 * time.Second
)

// events streams what waits for the administrator as Server-Sent Events:
// "todo" with the counts at once and whenever they change, and
// "signed_out" when the session ends. The stream is not a request: it
// does not keep the session alive.
func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	every := h.EventsEvery
	if every <= 0 {
		every = eventsCheck
	}
	rc := http.NewResponseController(w)
	// The server's read and write timeouts bound a request; a stream lasts.
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no") // nginx passes it on at once
	w.WriteHeader(http.StatusOK)
	ctx, p := r.Context(), principal(r)
	send := func(event string, data []byte) bool {
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	// Reconnect after 5 seconds when the stream drops.
	if _, err := fmt.Fprint(w, "retry: 5000\n\n"); err != nil {
		return
	}
	var last []byte
	quiet := time.Now()
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		if !h.Svc.SignedIn(ctx, p) {
			send("signed_out", []byte("{}"))
			return
		}
		if todo, err := h.Svc.Todo(ctx, p); err == nil {
			if b, err := json.Marshal(todo); err == nil && !bytes.Equal(b, last) {
				if !send("todo", b) {
					return
				}
				last, quiet = b, time.Now()
			}
		}
		if time.Since(quiet) >= eventsQuiet {
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
			quiet = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
