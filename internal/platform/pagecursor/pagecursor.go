// Package pagecursor encodes the opaque cursors of lists paged newest
// first: the last item's time and ID, so the next page starts strictly
// after it whatever order IDs sort in.
package pagecursor

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

// ErrBad is returned for a cursor this package did not make.
var ErrBad = errors.New("bad cursor")

// Encode makes the cursor after an item at t with id.
func Encode(t time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(t.UnixMicro(), 10) + "_" + id))
}

// Decode reads a cursor; an empty one is the zero time and ID.
func Decode(s string) (time.Time, string, error) {
	if s == "" {
		return time.Time{}, "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, "", ErrBad
	}
	micros, id, ok := strings.Cut(string(raw), "_")
	n, err := strconv.ParseInt(micros, 10, 64)
	if !ok || err != nil || id == "" || len(id) > 64 {
		return time.Time{}, "", ErrBad
	}
	return time.UnixMicro(n).UTC(), id, nil
}
