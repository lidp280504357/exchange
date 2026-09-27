package domain

import (
	"testing"

	"github.com/google/uuid"
)

func TestNewUser(t *testing.T) {
	id := uuid.NewString()
	u, err := NewUser(id, " sg ", "en", "Asia/Singapore")
	if err != nil || u.Region != "SG" || u.Language != "en" || u.Timezone != "Asia/Singapore" || u.Status != StatusActive {
		t.Fatalf("valid: %+v %v", u, err)
	}
	for _, tc := range []struct{ id, region, lang, tz string }{
		{"nope", "SG", "", ""},
		{id, "SGP", "", ""},
		{id, "S1", "", ""},
		{id, "SG", "English!", ""},
		{id, "SG", "", "Mars/Olympus"},
	} {
		if _, err := NewUser(tc.id, tc.region, tc.lang, tc.tz); err == nil {
			t.Errorf("NewUser(%q, %q, %q, %q) accepted", tc.id, tc.region, tc.lang, tc.tz)
		}
	}
}
