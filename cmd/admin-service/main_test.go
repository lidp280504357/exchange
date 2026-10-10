package main

import (
	"strings"
	"testing"
)

// HOUSE_USER_ID is a user ID or nothing (A129): the coin's holders ask the
// ledger with it, which refuses anything else; it is used as the ledger
// writes user IDs.
func TestHouseUserIsAUserID(t *testing.T) {
	const want = "01a0ee10-be13-765b-8db7-31ec8a45dbed"
	for id, bad := range map[string]bool{
		"": false, want: false, "01A0EE10-BE13-765B-8DB7-31EC8A45DBED": false, "{" + want + "}": false, "house": true, " " + want: true,
	} {
		err := (&settings{HouseUser: id}).Validate()
		if got := err != nil && strings.Contains(err.Error(), "HOUSE_USER_ID"); got != bad {
			t.Errorf("%q: %v", id, err)
		}
		if got := houseUser(id); !bad && id != "" && got != want {
			t.Errorf("%q used as %q", id, got)
		}
	}
	if houseUser("") != "" {
		t.Error("not configured")
	}
}
