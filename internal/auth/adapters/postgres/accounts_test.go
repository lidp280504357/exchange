package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lidp280504357/exchange/internal/auth/domain"
	"github.com/lidp280504357/exchange/internal/auth/ports"
)

func TestIdentitiesAndCredentials(t *testing.T) {
	store, _ := setup(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	user, other := uuid.NewString(), uuid.NewString()
	ids := store.Read().Identities()

	if err := ids.Create(ctx, domain.Identity{ID: uuid.NewString(), UserID: user, Kind: "EMAIL", Value: "a@example.com"}, now); err != nil {
		t.Fatal(err)
	}
	err := ids.Create(ctx, domain.Identity{ID: uuid.NewString(), UserID: other, Kind: "EMAIL", Value: "a@example.com"}, now)
	if err != domain.ErrIdentityTaken { //nolint:errorlint // sentinel returned as is
		t.Fatalf("taken: %v", err)
	}
	err = ids.Create(ctx, domain.Identity{ID: uuid.NewString(), UserID: user, Kind: "EMAIL", Value: "b@example.com"}, now)
	if err != domain.ErrIdentityKindBound { //nolint:errorlint // sentinel returned as is
		t.Fatalf("kind bound: %v", err)
	}
	phone := domain.Identity{ID: uuid.NewString(), UserID: user, Kind: "PHONE", Value: "+6591234567"}
	if err := ids.Create(ctx, phone, now); err != nil {
		t.Fatal(err)
	}
	if err := ids.UpdateValue(ctx, phone.ID, "+6597654321", now); err != nil {
		t.Fatal(err)
	}
	list, err := ids.ByUser(ctx, user)
	if err != nil || len(list) != 2 || list[0].Kind != "EMAIL" || list[1].Value != "+6597654321" {
		t.Fatalf("by user: %+v %v", list, err)
	}

	creds := store.Read().Credentials()
	if c, err := creds.Get(ctx, user); err != nil || c != nil {
		t.Fatalf("no credential yet: %v %v", c, err)
	}
	if err := creds.Create(ctx, user, "$argon2id$first", now); err != nil {
		t.Fatal(err)
	}
	if err := creds.RecordFailure(ctx, user, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := creds.RecordFailure(ctx, user, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	c, err := creds.Get(ctx, user)
	if err != nil || c.FailedAttempts != 2 || !c.LockedUntil.Equal(now.Add(time.Hour)) || !c.LastLoginAt.IsZero() {
		t.Fatalf("after failures: %+v %v", c, err)
	}
	if err := creds.RecordLogin(ctx, user, now); err != nil {
		t.Fatal(err)
	}
	if err := creds.SetPassword(ctx, user, "$argon2id$second", now); err != nil {
		t.Fatal(err)
	}
	c, err = creds.Get(ctx, user)
	if err != nil || c.FailedAttempts != 0 || !c.LockedUntil.IsZero() || !c.LastLoginAt.Equal(now) || c.PasswordHash != "$argon2id$second" {
		t.Fatalf("after login: %+v %v", c, err)
	}
}

func TestSessionsAndRefreshTokens(t *testing.T) {
	store, _ := setup(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	user := uuid.NewString()
	s1 := domain.Session{ID: uuid.NewString(), UserID: user, DeviceID: "device-0001", ClientType: domain.ClientWeb, UserAgent: "ua", IP: "203.0.113.9", CreatedAt: now}
	s2 := domain.Session{ID: uuid.NewString(), UserID: user, DeviceID: "device-0002", ClientType: domain.ClientApp, CreatedAt: now.Add(time.Second)}

	err := store.Tx(ctx, func(r ports.Repos) error {
		for _, s := range []domain.Session{s1, s2} {
			if err := r.Sessions().Create(ctx, s); err != nil {
				return err
			}
		}
		return r.Sessions().CreateRefresh(ctx, domain.RefreshToken{Hash: []byte("r1"), SessionID: s1.ID, Generation: 1, ExpiresAt: now.Add(time.Hour)})
	})
	if err != nil {
		t.Fatal(err)
	}
	sessions := store.Read().Sessions()
	active, err := sessions.Active(ctx, user)
	if err != nil || len(active) != 2 || active[0].ID != s2.ID {
		t.Fatalf("active, most recent first: %+v %v", active, err)
	}
	if err := sessions.Touch(ctx, s1.ID, "198.51.100.7", "ua2", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := sessions.Get(ctx, s1.ID)
	if err != nil || got.IP != "198.51.100.7" || got.UserAgent != "ua2" || !got.LastSeenAt.Equal(now.Add(time.Minute)) || !got.RevokedAt.IsZero() {
		t.Fatalf("touched: %+v %v", got, err)
	}

	err = store.Tx(ctx, func(r ports.Repos) error {
		rt, err := r.Sessions().RefreshForUpdate(ctx, []byte("r1"))
		if err != nil || rt == nil || rt.Generation != 1 || !rt.RotatedAt.IsZero() {
			t.Fatalf("refresh: %+v %v", rt, err)
		}
		return r.Sessions().MarkRotated(ctx, []byte("r1"), now)
	})
	if err != nil {
		t.Fatal(err)
	}
	if rt, _ := sessions.RefreshForUpdate(ctx, []byte("r1")); rt == nil || !rt.RotatedAt.Equal(now) {
		t.Fatalf("rotated: %+v", rt)
	}
	if rt, err := sessions.RefreshForUpdate(ctx, []byte("unknown")); err != nil || rt != nil {
		t.Fatalf("unknown token: %v %v", rt, err)
	}

	if ok, err := sessions.Revoke(ctx, s2.ID, domain.RevokeLogout, now); err != nil || !ok {
		t.Fatalf("revoke: %v %v", ok, err)
	}
	if ok, _ := sessions.Revoke(ctx, s2.ID, domain.RevokeLogout, now); ok {
		t.Fatal("revoking twice must report false")
	}
	if active, _ := sessions.Active(ctx, user); len(active) != 1 || active[0].ID != s1.ID {
		t.Fatalf("active after revoke: %+v", active)
	}
	if missing, err := sessions.Get(ctx, uuid.NewString()); err != nil || missing != nil {
		t.Fatalf("unknown session: %v %v", missing, err)
	}
}

func TestChallengesStepUpsDevicesHistory(t *testing.T) {
	store, db := setup(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	user := uuid.NewString()
	r := store.Read()

	lc := domain.LoginChallenge{ID: uuid.NewString(), UserID: user, DeviceID: "device-0001", ExpiresAt: now.Add(time.Minute)}
	if err := r.LoginChallenges().Create(ctx, lc); err != nil {
		t.Fatal(err)
	}
	if got, err := r.LoginChallenges().Get(ctx, lc.ID); err != nil || got.UserID != user || !got.ConsumedAt.IsZero() {
		t.Fatalf("login challenge: %+v %v", got, err)
	}
	if ok, _ := r.LoginChallenges().Consume(ctx, lc.ID, now.Add(2*time.Minute)); ok {
		t.Fatal("expired challenge consumed")
	}
	if ok, err := r.LoginChallenges().Consume(ctx, lc.ID, now); err != nil || !ok {
		t.Fatalf("consume: %v %v", ok, err)
	}
	if ok, _ := r.LoginChallenges().Consume(ctx, lc.ID, now); ok {
		t.Fatal("consumed twice")
	}

	su := domain.StepUp{Hash: []byte("su"), UserID: user, SessionID: uuid.NewString(), Channel: domain.ChannelSMS, ExpiresAt: now.Add(time.Minute)}
	if err := r.StepUps().Create(ctx, su); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.StepUps().Consume(ctx, []byte("su"), uuid.NewString(), now); got != nil {
		t.Fatal("another user's step-up redeemed")
	}
	got, err := r.StepUps().Consume(ctx, []byte("su"), user, now)
	if err != nil || got == nil || got.Channel != domain.ChannelSMS || got.SessionID != su.SessionID {
		t.Fatalf("step-up: %+v %v", got, err)
	}
	if again, _ := r.StepUps().Consume(ctx, []byte("su"), user, now); again != nil {
		t.Fatal("step-up redeemed twice")
	}

	if isNew, err := r.Devices().Seen(ctx, user, "device-0001", now); err != nil || !isNew {
		t.Fatalf("first sight: %v %v", isNew, err)
	}
	if isNew, err := r.Devices().Seen(ctx, user, "device-0001", now); err != nil || isNew {
		t.Fatalf("second sight: %v %v", isNew, err)
	}

	for i, m := range []string{"REGISTER", "PASSWORD", "OTP"} {
		if err := r.History().Add(ctx, domain.LoginEvent{UserID: user, Method: m, Result: "SUCCESS", CreatedAt: now.Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := r.History().List(ctx, user, 0, 2)
	if err != nil || len(page) != 2 || page[0].Method != "OTP" || page[1].Method != "PASSWORD" {
		t.Fatalf("history page: %+v %v", page, err)
	}
	rest, err := r.History().List(ctx, user, page[1].ID, 10)
	if err != nil || len(rest) != 1 || rest[0].Method != "REGISTER" {
		t.Fatalf("history rest: %+v %v", rest, err)
	}

	if err := r.RebindRequests().Create(ctx, domain.RebindRequest{ID: uuid.NewString(), UserID: user, Kind: "EMAIL", NewValue: "n@example.com"}); err != nil {
		t.Fatal(err)
	}

	// Purge removes what expired before the cutoff.
	n, err := store.Purge(ctx, now.Add(time.Hour))
	if err != nil || n != 2 { // the login challenge and the step-up
		t.Fatalf("purge: %d %v", n, err)
	}
	var left int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM step_up_tokens`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("step-ups left: %d %v", left, err)
	}
}

func TestTOTPBindings(t *testing.T) {
	store, _ := setup(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	user := uuid.NewString()
	r := store.Read()
	if got, err := r.TOTP().Get(ctx, user); err != nil || got != nil {
		t.Fatalf("none yet: %+v %v", got, err)
	}
	pending := ports.SealedTOTP{UserID: user, Sealed: []byte{1, 2, 3}, Status: domain.TOTPPending, CreatedAt: now}
	if err := r.TOTP().Put(ctx, pending); err != nil {
		t.Fatal(err)
	}
	active := pending
	active.Status, active.LastStep, active.ActivatedAt = domain.TOTPActive, 59666666, now.Add(time.Minute)
	err := store.Tx(ctx, func(tx ports.Repos) error {
		if got, err := tx.TOTP().GetForUpdate(ctx, user); err != nil || got == nil || got.Status != domain.TOTPPending || !got.ActivatedAt.IsZero() {
			t.Fatalf("pending: %+v %v", got, err)
		}
		return tx.TOTP().Put(ctx, active)
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.TOTP().Get(ctx, user)
	if err != nil || got.Status != domain.TOTPActive || got.LastStep != 59666666 || !got.ActivatedAt.Equal(active.ActivatedAt) || string(got.Sealed) != "\x01\x02\x03" {
		t.Fatalf("active: %+v %v", got, err)
	}
	// Step-ups may now be proven by TOTP.
	su := domain.StepUp{Hash: []byte("totp-su"), UserID: user, SessionID: uuid.NewString(), Channel: domain.ChannelTOTP, ExpiresAt: now.Add(time.Minute)}
	if err := r.StepUps().Create(ctx, su); err != nil {
		t.Fatalf("a TOTP step-up: %v", err)
	}
	if err := r.TOTP().Delete(ctx, user); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.TOTP().Get(ctx, user); got != nil {
		t.Fatal("deleted")
	}
}

func TestSecurityContext(t *testing.T) {
	store, db := setup(t)
	ctx := context.Background()
	user, session := uuid.New(), uuid.New()
	for _, sql := range []string{
		`INSERT INTO identities (id, user_id, kind, value, verified_at) VALUES (gen_random_uuid(), $1, 'EMAIL', 'sec@example.com', '2026-09-20T00:00:00Z')`,
		`INSERT INTO identities (id, user_id, kind, value, verified_at) VALUES (gen_random_uuid(), $1, 'PHONE', '+6590000000', '2026-09-25T00:00:00Z')`,
		`INSERT INTO credentials (user_id, password_hash, password_changed_at) VALUES ($1, 'x', '2026-09-26T00:00:00Z')`,
		`INSERT INTO known_devices (user_id, device_id, first_seen_at) VALUES ($1, 'dev-1', '2026-09-27T00:00:00Z')`,
	} {
		if _, err := db.Exec(ctx, sql, user); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `INSERT INTO sessions (id, user_id, device_id, client_type) VALUES ($1, $2, 'dev-1', 'WEB')`, session, user); err != nil {
		t.Fatal(err)
	}
	c, err := store.Read().Security().Context(ctx, user.String(), session.String())
	if err != nil {
		t.Fatal(err)
	}
	if c.Identities != 2 || c.TOTPEnabled || c.DeviceID != "dev-1" || c.DeviceFirstSeenAt.Day() != 27 ||
		c.IdentityChangedAt.Day() != 25 || c.PasswordChangedAt.Day() != 26 {
		t.Fatalf("context %+v", c)
	}
	if c, err := store.Read().Security().Context(ctx, user.String(), uuid.NewString()); err != nil || c.DeviceID != "" || c.Identities != 2 {
		t.Fatalf("an unknown session has no device: %+v %v", c, err)
	}
}

func TestAdminReads(t *testing.T) {
	store, db := setup(t)
	ctx := context.Background()
	user := uuid.New()
	for _, sql := range []string{
		`INSERT INTO identities (id, user_id, kind, value, verified_at) VALUES (gen_random_uuid(), $1, 'EMAIL', 'adm@example.com', '2026-09-20T00:00:00Z')`,
		`INSERT INTO credentials (user_id, password_hash, password_changed_at) VALUES ($1, 'x', '2026-09-26T00:00:00Z')`,
		`INSERT INTO known_devices (user_id, device_id, first_seen_at, last_seen_at) VALUES ($1, 'old', '2026-09-01T00:00:00Z', '2026-09-02T00:00:00Z')`,
		`INSERT INTO known_devices (user_id, device_id, first_seen_at, last_seen_at) VALUES ($1, 'new', '2026-09-27T00:00:00Z', '2026-09-28T00:00:00Z')`,
	} {
		if _, err := db.Exec(ctx, sql, user); err != nil {
			t.Fatal(err)
		}
	}
	r := store.Read()
	ids, err := r.Identities().ByUser(ctx, user.String())
	if err != nil || len(ids) != 1 || ids[0].VerifiedAt.Day() != 20 || ids[0].CreatedAt.IsZero() {
		t.Fatalf("identities %+v %v", ids, err)
	}
	if c, err := r.Credentials().Get(ctx, user.String()); err != nil || c.PasswordChangedAt.Day() != 26 {
		t.Fatalf("credential %+v %v", c, err)
	}
	if d, err := r.Devices().List(ctx, user.String()); err != nil || len(d) != 2 || d[0].DeviceID != "new" || d[1].FirstSeenAt.Day() != 1 {
		t.Fatalf("devices %+v %v", d, err)
	}

	rebinds := r.RebindRequests()
	first, second := uuid.NewString(), uuid.NewString()
	for _, id := range []string{first, second} {
		if err := rebinds.Create(ctx, domain.RebindRequest{ID: id, UserID: user.String(), Kind: "EMAIL", NewValue: id[:8] + "@example.com"}); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := rebinds.CountPending(ctx, user.String()); err != nil || n != 2 {
		t.Fatalf("pending %d %v", n, err)
	}
	page, err := rebinds.List(ctx, domain.RebindPending, "", time.Time{}, "", 1)
	if err != nil || len(page) != 1 || page[0].Status != domain.RebindPending {
		t.Fatalf("a page %+v %v", page, err)
	}
	rest, err := rebinds.List(ctx, "", user.String(), page[0].CreatedAt, page[0].ID, 10)
	if err != nil || len(rest) != 1 || rest[0].ID == page[0].ID {
		t.Fatalf("the next page %+v %v", rest, err)
	}
	err = store.Tx(ctx, func(tx ports.Repos) error {
		rr, err := tx.RebindRequests().GetForUpdate(ctx, first)
		if err != nil || rr == nil {
			return fmt.Errorf("get %v %w", rr, err)
		}
		rr.Status, rr.DecidedAt, rr.DecidedBy, rr.Reason = domain.RebindRejected, time.Now(), "ops@example.com", "could not confirm"
		return tx.RebindRequests().Decide(ctx, *rr)
	})
	if err != nil {
		t.Fatal(err)
	}
	decided, err := rebinds.List(ctx, domain.RebindRejected, user.String(), time.Time{}, "", 10)
	if err != nil || len(decided) != 1 || decided[0].DecidedBy != "ops@example.com" || decided[0].Reason != "could not confirm" || decided[0].DecidedAt.IsZero() {
		t.Fatalf("decided %+v %v", decided, err)
	}
	if n, err := rebinds.CountPending(ctx, user.String()); err != nil || n != 1 {
		t.Fatalf("pending after %d %v", n, err)
	}
}
