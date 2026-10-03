package main

import (
	"strings"
	"testing"
	"time"
)

// TestRetentionHasAFloor: a keep shorter than a week is refused at start,
// unset ones take the defaults (C5.5 ㉓).
func TestRetentionHasAFloor(t *testing.T) {
	short := settings{NoticeRetention: time.Hour, DeliveryRetention: 3 * 24 * time.Hour}
	err := short.Validate()
	if err == nil || !strings.Contains(err.Error(), "NOTICE_RETENTION must be at least") || !strings.Contains(err.Error(), "DELIVERY_RETENTION must be at least") {
		t.Fatalf("short keeps: %v", err)
	}
	for _, ok := range []settings{{}, {NoticeRetention: 7 * 24 * time.Hour, DeliveryRetention: 90 * 24 * time.Hour}} {
		if err := ok.Validate(); err != nil && strings.Contains(err.Error(), "RETENTION") {
			t.Fatalf("%+v: %v", ok, err)
		}
	}
}
