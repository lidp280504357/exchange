package main

import (
	"strings"
	"testing"
)

// HOUSE_USER_ID is a user ID or nothing (A129): the coin's holders ask the
// ledger with it, which refuses anything else.
func TestHouseUserIsAUserID(t *testing.T) {
	for id, bad := range map[string]bool{
		"": false, "01a0ee10-be13-765b-8db7-31ec8a45dbed": false, "01A0EE10-BE13-765B-8DB7-31EC8A45DBED": false, "house": true,
	} {
		err := (&settings{HouseUser: id}).Validate()
		if got := err != nil && strings.Contains(err.Error(), "HOUSE_USER_ID"); got != bad {
			t.Errorf("%q: %v", id, err)
		}
	}
}
