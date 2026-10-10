package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/retention"
)

// What each rule keeps, schema by schema (B201, B200 ⑪): beside every row
// a rule deletes, one of the same table it must leave - still pending,
// still open, ended within the window, the latest of its kind.
func TestRetentionKeeps(t *testing.T) {
	ctx := context.Background()
	schemas := []string{"admin", "notify", "wallet", "marketsim", "marketmaker", "auth"}
	dbs := map[string]*pg.DB{}
	for _, schema := range schemas {
		dbs[schema] = migrated(ctx, t, schema)
	}
	now := time.Now()
	old, recent, ancient, later := now.AddDate(0, 0, -20), now.AddDate(0, 0, -1), now.AddDate(0, 0, -100), now.Add(time.Hour)

	type seeded struct {
		schema, label, check string
		keep                 bool
		key                  any
	}
	var rows []seeded
	// add inserts a row whose key is the insert's $1, and how to count it
	// (check, by the same $1) once the run is done.
	add := func(schema, label string, keep bool, insert, check string, args ...any) {
		t.Helper()
		if _, err := dbs[schema].Exec(ctx, insert, args...); err != nil {
			t.Fatalf("%s %s: %v", schema, label, err)
		}
		rows = append(rows, seeded{schema: schema, label: label, check: check, keep: keep, key: args[0]})
	}
	id := uuid.NewString

	// admin: decided approvals, ended sessions, closed or applied changes.
	a1, a2 := id(), id()
	for _, a := range []string{a1, a2} {
		add("admin", "admin "+a, true, `INSERT INTO admins (id, email, name, role, password_hash, totp_sealed, created_at)
			VALUES ($1, $2, 'a', 'ADMIN', 'x', '\x00', $3)`, `SELECT count(*) FROM admins WHERE id = $1`, a, a+"@example.com", ancient)
	}
	approval := `INSERT INTO approvals (id, kind, payload, reason, status, requested_by, decided_by, created_at, decided_at, mode)
		VALUES ($1, 'LEDGER_ADJUSTMENT', '{}', 'r', $2, $3, $4, $5, $6, 'TWO_PERSON')`
	approvalCheck := `SELECT count(*) FROM approvals WHERE id = $1`
	add("admin", "approval decided before the window", false, approval, approvalCheck, id(), "EXECUTED", a1, a2, old, old)
	add("admin", "approval decided within it", true, approval, approvalCheck, id(), "REJECTED", a1, a2, old, recent)
	add("admin", "approval still pending", true, approval, approvalCheck, id(), "PENDING", a1, nil, ancient, nil)
	adminSession := `INSERT INTO admin_sessions (token_hash, admin_id, created_at, last_seen_at, expires_at, revoked_at)
		VALUES ($1, $2, $3, $3, $4, $5)`
	adminSessionCheck := `SELECT count(*) FROM admin_sessions WHERE token_hash = $1`
	add("admin", "console session revoked before the window", false, adminSession, adminSessionCheck, []byte("revoked"), a1, old, later, old)
	add("admin", "console session expired before the window", false, adminSession, adminSessionCheck, []byte("expired"), a1, old, old, nil)
	add("admin", "console session still valid", true, adminSession, adminSessionCheck, []byte("live"), a1, old, later, nil)
	change := `INSERT INTO instrument_changes (id, kind, target, payload, summary, reason, status, requested_by, closed_by, closed_at,
		effective_at, applied_at, created_at) VALUES ($1, 'CONFIG', 'BTC-USDT', '{}', '{}', 'r', $2, $3, $4, $5, $6, $7, $8)`
	changeCheck := `SELECT count(*) FROM instrument_changes WHERE id = $1`
	add("admin", "change applied before the window", false, change, changeCheck, id(), "APPLIED", a1, nil, nil, old, old, old)
	add("admin", "change canceled before the window", false, change, changeCheck, id(), "CANCELED", a1, a2, old, nil, nil, old)
	add("admin", "change scheduled long ago, not applied", true, change, changeCheck, id(), "SCHEDULED", a1, nil, nil, old, nil, old)
	add("admin", "change applied within the window", true, change, changeCheck, id(), "APPLIED", a1, nil, nil, old, recent, old)

	// notify: notifications, deliveries done, broadcasts finished, the dev inbox.
	notification := `INSERT INTO notifications (id, user_id, type, title, body, created_at) VALUES ($1, $2, 't', 't', 'b', $3)`
	notificationCheck := `SELECT count(*) FROM notifications WHERE id = $1`
	add("notify", "notification before the window", false, notification, notificationCheck, id(), id(), old)
	add("notify", "notification within it", true, notification, notificationCheck, id(), id(), recent)
	delivery := `INSERT INTO deliveries (id, kind, channel, template, target_mask, status, created_at, next_attempt_at)
		VALUES ($1, 'NOTICE', 'EMAIL', 't', 'm', $2, $3, $4)`
	deliveryCheck := `SELECT count(*) FROM deliveries WHERE id = $1`
	add("notify", "delivery sent before the window", false, delivery, deliveryCheck, id(), "SENT", old, nil)
	add("notify", "delivery failed for good before the window", false, delivery, deliveryCheck, id(), "FAILED", old, nil)
	add("notify", "delivery still queued", true, delivery, deliveryCheck, id(), "QUEUED", old, nil)
	add("notify", "delivery to be retried", true, delivery, deliveryCheck, id(), "FAILED_RETRYING", old, later)
	broadcast := `INSERT INTO broadcasts (id, audience, title, body, status, created_by, created_at) VALUES ($1, 'ALL', '{}', '{}', $2, 'test', $3)`
	broadcastCheck := `SELECT count(*) FROM broadcasts WHERE id = $1`
	add("notify", "broadcast sent before the window", false, broadcast, broadcastCheck, id(), "SENT", old)
	add("notify", "broadcast still sending", true, broadcast, broadcastCheck, id(), "SENDING", old)
	inbox := `INSERT INTO mock_messages (channel, target, body, created_at) VALUES ('EMAIL', $1, 'b', $2)`
	inboxCheck := `SELECT count(*) FROM mock_messages WHERE target = $1`
	add("notify", "dev inbox message before the window", false, inbox, inboxCheck, "old@example.com", old)
	add("notify", "dev inbox message within it", true, inbox, inboxCheck, "recent@example.com", recent)

	// wallet: deposits for the keys' window unless a person must look at
	// them, a withdrawal whose fee is held, the latest check, fees,
	// commands and sweeps not ended, prices within the window.
	deposit := `INSERT INTO deposits (id, user_id, asset, network, address, tx_hash, log_index, block_number, block_hash, amount,
		raw_amount, required_confirmations, status, journal_id, detected_at, updated_at, provider_tx_id, unclaimed, reason,
		discrepancy, resolution) VALUES ($1, $2, 'USDT', 'UDUN-TRON', 'T1', gen_random_uuid()::text, -1, 1, 'h', 1, 1, 1, $3, $4, $5, $5,
		gen_random_uuid()::text, $6, $7, $8, $9)`
	depositCheck := `SELECT count(*) FROM deposits WHERE id = $1`
	add("wallet", "deposit credited before the keys' window", false, deposit, depositCheck, id(), id(), "CREDITED", id(), ancient, false, nil, "", "")
	add("wallet", "deposit credited within it", true, deposit, depositCheck, id(), id(), "CREDITED", id(), old, false, nil, "", "")
	add("wallet", "deposit disputed", true, deposit, depositCheck, id(), id(), "CREDITED", id(), ancient, false, nil, "amount differs", "")
	add("wallet", "unclaimed deposit not resolved", true, deposit, depositCheck, id(), id(), "REJECTED", nil, ancient, true, "UNKNOWN_ADDRESS", "", "")
	add("wallet", "unclaimed deposit dismissed", false, deposit, depositCheck, id(), id(), "REJECTED", nil, ancient, true, "UNKNOWN_ADDRESS", "", "DISMISSED")
	withdrawal := `INSERT INTO withdrawals (id, user_id, asset, network, address, amount, fee, status, required_confirmations, created_at,
		updated_at) VALUES ($1, $2, 'USDT', 'TRON', 'T1', 10, 1, $3, 1, $4, $4)`
	withdrawalCheck := `SELECT count(*) FROM withdrawals WHERE id = $1`
	ended, held, open := id(), id(), id()
	add("wallet", "withdrawal ended before the keys' window", false, withdrawal, withdrawalCheck, ended, id(), "CANCELED", ancient)
	add("wallet", "withdrawal ended long ago, its fee held", true, withdrawal, withdrawalCheck, held, id(), "FAILED", ancient)
	add("wallet", "withdrawal not ended", true, withdrawal, withdrawalCheck, open, id(), "PENDING_REVIEW", ancient)
	attempt := `INSERT INTO withdrawal_attempts (tx_hash, withdrawal_id, nonce, max_fee, max_tip, raw_tx, created_at) VALUES ($1, $2, 1, 1, 1, '', $3)`
	attemptCheck := `SELECT count(*) FROM withdrawal_attempts WHERE tx_hash = $1`
	add("wallet", "attempt of the ended withdrawal", false, attempt, attemptCheck, "0xattempt-ended", ended, ancient)
	add("wallet", "attempt of the withdrawal not ended", true, attempt, attemptCheck, "0xattempt-open", open, ancient)
	fee := `INSERT INTO chain_fees (tx_hash, network, asset, amount, purpose, reference, journal_id, booked_at, created_at, status, hold_reason,
		resolved_at) VALUES ($1, 'TRON', 'TRX', 1, $2, $3, $4, $5, $6, $7, $8, $9)`
	feeCheck := `SELECT count(*) FROM chain_fees WHERE tx_hash = $1`
	add("wallet", "fee booked before the window", false, fee, feeCheck, "0xfee-booked", "SWEEP", "s", id(), old, old, "BOOKABLE", "", nil)
	add("wallet", "fee written off before the window", false, fee, feeCheck, "0xfee-off", "SWEEP", "s", nil, nil, old, "WRITTEN_OFF", "x", old)
	add("wallet", "fee held for a person", true, fee, feeCheck, "0xfee-held", "WITHDRAWAL", held, nil, nil, ancient, "HELD", "x", nil)
	add("wallet", "fee not booked yet", true, fee, feeCheck, "0xfee-unbooked", "SWEEP", "s", nil, nil, old, "BOOKABLE", "", nil)
	check := `INSERT INTO chain_checks (id, network, asset, chain, ledger, unbooked, shortfall, addresses, checked_at)
		VALUES ($1, 'TRON', 'USDT', 1, 1, 0, 0, 1, $2)`
	checkCheck := `SELECT count(*) FROM chain_checks WHERE id = $1`
	add("wallet", "check before the window, not the latest", false, check, checkCheck, 900001, old.Add(-time.Hour))
	add("wallet", "the latest check, before the window", true, check, checkCheck, 900002, old)
	command := `INSERT INTO commands (id, network, kind, status, requested_by, created_at, done_at) VALUES ($1, 'TRON', 'SWEEP', $2, 'test', $3, $4)`
	commandCheck := `SELECT count(*) FROM commands WHERE id = $1`
	add("wallet", "command done before the window", false, command, commandCheck, id(), "DONE", old, old)
	add("wallet", "command still pending", true, command, commandCheck, id(), "PENDING", old, nil)
	sweep := `INSERT INTO sweeps (id, network, address, derivation_index, asset, amount, nonce, tx_hash, raw_tx, status, created_at, updated_at)
		VALUES ($1, 'TRON', 'T1', 0, 'USDT', 1, 1, gen_random_uuid()::text, '', $2, $3, $3)`
	sweepCheck := `SELECT count(*) FROM sweeps WHERE id = $1`
	add("wallet", "sweep confirmed before the window", false, sweep, sweepCheck, id(), "CONFIRMED", old)
	add("wallet", "sweep still broadcast", true, sweep, sweepCheck, id(), "BROADCAST", old)
	price := `INSERT INTO price_snapshots (day, asset, usdt, source, created_at) VALUES ($1::date, 'BTC', 1, 'test', $1::date)`
	priceCheck := `SELECT count(*) FROM price_snapshots WHERE day = $1::date`
	add("wallet", "price before the window", false, price, priceCheck, old)
	add("wallet", "price within it", true, price, priceCheck, recent)

	// marketsim: samples, events ended (by their end), the latest parameters.
	sample := `INSERT INTO samples (at, target) VALUES ($1, 1)`
	sampleCheck := `SELECT count(*) FROM samples WHERE at = $1`
	add("marketsim", "sample before the window", false, sample, sampleCheck, old)
	add("marketsim", "sample within it", true, sample, sampleCheck, recent)
	simEvent := `INSERT INTO events (id, type, starts_at, status, created_by, reason, created_at, ended_at) VALUES ($1, 'JUMP', $2, $3, 'test', 'r', $2, $4)`
	simEventCheck := `SELECT count(*) FROM events WHERE id = $1`
	add("marketsim", "event ended before the window", false, simEvent, simEventCheck, id(), old, "DONE", old)
	add("marketsim", "event made before the window, ended within it", true, simEvent, simEventCheck, id(), old, "DONE", recent)
	add("marketsim", "event scheduled long ago, not run", true, simEvent, simEventCheck, id(), old, "SCHEDULED", nil)
	add("marketsim", "event still running", true, simEvent, simEventCheck, id(), old, "RUNNING", nil)
	params := `INSERT INTO param_changes (version, at, actor) VALUES ($1, $2, 'test')`
	paramsCheck := `SELECT count(*) FROM param_changes WHERE version = $1`
	add("marketsim", "parameters changed before the window", false, params, paramsCheck, 1_000_000, old)
	add("marketsim", "the latest parameters, before the window", true, params, paramsCheck, 1_000_001, old)

	// marketmaker: HOUSE caps changes but the latest.
	caps := `INSERT INTO house_caps_changes (version, caps, actor, reason, at) VALUES ($1, '{}', 'test', 'r', $2)`
	capsCheck := `SELECT count(*) FROM house_caps_changes WHERE version = $1`
	add("marketmaker", "caps changed before the window", false, caps, capsCheck, 1_000_000, old)
	add("marketmaker", "the latest caps, before the window", true, caps, capsCheck, 1_000_001, old)

	// auth: sign-ins, decided rebinding requests (sessions: TestRetentionRun).
	signIn := `INSERT INTO login_history (user_id, method, result, created_at) VALUES ($1, 'PASSWORD', 'OK', $2)`
	signInCheck := `SELECT count(*) FROM login_history WHERE user_id = $1`
	add("auth", "sign-in before the window", false, signIn, signInCheck, id(), old)
	add("auth", "sign-in within it", true, signIn, signInCheck, id(), recent)
	rebind := `INSERT INTO identity_rebind_requests (id, user_id, kind, new_value, status, created_at, decided_at) VALUES ($1, $2, 'EMAIL', 'x', $3, $4, $5)`
	rebindCheck := `SELECT count(*) FROM identity_rebind_requests WHERE id = $1`
	add("auth", "rebinding decided before the window", false, rebind, rebindCheck, id(), id(), "APPROVED", old, old)
	add("auth", "rebinding still under review", true, rebind, rebindCheck, id(), id(), "PENDING_REVIEW", ancient, nil)

	own := func(schema string) (*pg.DB, func(), error) { return dbs[schema], func() {}, nil }
	policies, _, err := pickPolicies(retentionPolicies(), "admin,notify,wallet,marketsim,marketmaker,auth")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	w := retention.Window{Now: now, Days: 15, KeyDays: 90, Batch: 2}
	if err := runRetention(ctx, policies, own, nil, w, &out); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	for _, r := range rows {
		var n int
		if err := dbs[r.schema].QueryRow(ctx, r.check, r.key).Scan(&n); err != nil {
			t.Fatalf("%s %s: %v", r.schema, r.label, err)
		}
		if want := map[bool]int{true: 1, false: 0}[r.keep]; n != want {
			t.Errorf("%s: %s: %d rows, want %d", r.schema, r.label, n, want)
		}
	}
	if t.Failed() {
		t.Log(out.String())
	}
}
