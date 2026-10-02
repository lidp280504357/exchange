// Package application runs the admin console (requirements §5.12):
// sign-in with a password and an authenticator code (the code can be
// switched off with the flag admin.login_without_totp), role checks on every
// action, fund operations (ledger adjustments and insurance fund
// contributions) approved by a second administrator or, in single-person
// mode, carried out alone within limits, and an audit event for every
// change the acting service does not audit itself.
package application

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	auditv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/audit/v1"
	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/flags"
	"github.com/lidp280504357/exchange/internal/platform/password"
	"github.com/lidp280504357/exchange/internal/platform/secretbox"
	"github.com/lidp280504357/exchange/internal/platform/totp"
)

// Service is the admin console.
type Service struct {
	Store  ports.Store
	Hasher *password.Hasher
	Box    *secretbox.Box
	Users  ports.Users
	// Security is auth-service's view of an account's sign-in security;
	// History user-service's status changes and consents; Risk
	// risk-service's assessments.
	Security ports.AccountSecurity
	History  ports.AccountHistory
	Risk     ports.Risk
	Orders   ports.Orders
	// Deposits handles the deposits that need a person (wallet-service).
	Deposits ports.Deposits
	Wallet   ports.Withdrawals
	Catalog  ports.Instruments
	// Reference checks the reference symbols of edited pairs; nil notes
	// them unchecked.
	Reference ports.ReferenceMarket
	// Derivatives is derivatives-service (perpetual contracts).
	Derivatives ports.Derivatives
	Flags       ports.Flags
	// Features switches the console's own behavior; nil leaves every
	// flag off (the authenticator code is required).
	Features ports.Features
	Ledger   ports.Ledger
	AuditLog ports.AuditLog
	Reports  ports.Reports
	// Records pages through the read models' orders, trades and deposits;
	// Market reads market-data-service's reference feed.
	Records ports.Records
	Market  ports.Market
	// Prices values fund operations in USDT for the single-person limits;
	// nil values only USDT.
	Prices ports.MarketPrices
	// HouseBook reads HOUSE's book; Probe the services' readiness; Reconciler
	// the ledger's reconciliation runs.
	HouseBook  HouseDeps
	Probe      ports.Health
	Reconciler ports.Reconciler
	Log        *slog.Logger
	Now        func() time.Time
	// CloseWait is the pause between attempts to close a position while
	// its closing orders are being canceled (700 ms when zero).
	CloseWait time.Duration
}

// Principal is the administrator behind a request.
type Principal struct {
	Admin   domain.Admin
	Session []byte // the session's token hash
}

func (p Principal) require(perm string) error {
	if !domain.Allows(p.Admin.Role, perm) {
		return domain.ErrForbidden.WithDetail("permission", perm)
	}
	return nil
}

// TOTPRequired reports whether sign-in asks for the authenticator code:
// always, unless the flag admin.login_without_totp is on (test
// environments, user decision of 2026-09-30).
func (s *Service) TOTPRequired() bool {
	return s.Features == nil || !s.Features.Enabled(flags.KeyAdminNoTOTP, flags.Subject{})
}

// Login checks the password and the authenticator code and opens a
// session; it returns the session token. Five failures lock the account
// for 15 minutes; an unknown email costs the same time as a known one.
// With the code switched off (TOTPRequired) the password alone signs in,
// the code is ignored and the audit event says it was not checked.
func (s *Service) Login(ctx context.Context, email, pw, code, ip, userAgent string) (string, domain.Session, domain.Admin, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	now := s.Now()
	checkCode := s.TOTPRequired()
	var (
		token   string
		session domain.Session
		admin   domain.Admin
		failure error
	)
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		a, err := r.Admins().ByEmailForUpdate(ctx, email)
		if err != nil {
			return err
		}
		if a == nil || a.Status != domain.StatusActive {
			s.Hasher.VerifyDummy(pw)
			failure = domain.ErrLoginFailed
			return nil
		}
		if a.Locked(now) {
			failure = domain.ErrLocked
			return nil
		}
		ok, err := s.Hasher.Verify(a.PasswordHash, pw)
		if err != nil {
			return err
		}
		step := a.TOTPLastStep
		if ok && checkCode {
			secret, err := s.Box.Open(a.TOTPSealed, []byte(a.ID))
			if err != nil {
				return err
			}
			step, ok = totp.Verify(secret, code, now, a.TOTPLastStep)
		}
		if !ok {
			a.Failed(now)
			failure = domain.ErrLoginFailed
			if err := r.Admins().Update(ctx, *a); err != nil {
				return err
			}
			return r.Audit(ctx, &auditv1.AdminActionPerformed{
				Target: "admin:" + a.ID, Action: "admin.login_failed", Actor: a.Email, Reason: "wrong password or code",
				Details: fmt.Sprintf(`{"ip":%q,"locked":%t}`, ip, a.Locked(now)),
			}, a.Email)
		}
		a.Succeeded(step, now)
		if err := r.Admins().Update(ctx, *a); err != nil {
			return err
		}
		var hash []byte
		token, hash = domain.NewToken()
		session = domain.Session{
			TokenHash: hash, AdminID: a.ID, IP: ip, UserAgent: truncate(userAgent, 200), CreatedAt: now, LastSeenAt: now,
			ExpiresAt: now.Add(domain.SessionTTL),
		}
		admin = *a
		if err := r.Sessions().Insert(ctx, session); err != nil {
			return err
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "admin:" + a.ID, Action: "admin.login", Actor: a.Email, Reason: "signed in",
			Details: fmt.Sprintf(`{"ip":%q,"totp_checked":%t}`, ip, checkCode),
		}, a.Email)
	})
	if err == nil {
		err = failure
	}
	if err != nil {
		return "", domain.Session{}, domain.Admin{}, err
	}
	return token, session, admin, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Authenticate returns the administrator of a session token.
func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	if token == "" {
		return Principal{}, domain.ErrUnauthorized
	}
	hash := domain.HashToken(token)
	r := s.Store.Read()
	sess, err := r.Sessions().Get(ctx, hash)
	if err != nil {
		return Principal{}, err
	}
	now := s.Now()
	if sess == nil || !sess.Live(now) {
		return Principal{}, domain.ErrUnauthorized
	}
	a, err := r.Admins().Get(ctx, sess.AdminID)
	if err != nil {
		return Principal{}, err
	}
	if a == nil || a.Status != domain.StatusActive {
		return Principal{}, domain.ErrUnauthorized
	}
	// Idle time counts from the last request, written at most once a minute.
	if now.Sub(sess.LastSeenAt) > time.Minute {
		if err := r.Sessions().Touch(ctx, hash, now); err != nil {
			return Principal{}, err
		}
	}
	return Principal{Admin: *a, Session: hash}, nil
}

// SignedIn reports whether a principal's session still holds, without
// counting as a request: an open stream of events keeps no session alive.
func (s *Service) SignedIn(ctx context.Context, p Principal) bool {
	r := s.Store.Read()
	sess, err := r.Sessions().Get(ctx, p.Session)
	if err != nil || sess == nil || !sess.Live(s.Now()) {
		return false
	}
	a, err := r.Admins().Get(ctx, sess.AdminID)
	return err == nil && a != nil && a.Status == domain.StatusActive
}

// Logout ends the session.
func (s *Service) Logout(ctx context.Context, p Principal) error {
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.Sessions().Revoke(ctx, p.Session, s.Now()); err != nil {
			return err
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "admin:" + p.Admin.ID, Action: "admin.logout", Actor: p.Admin.Email, Reason: "signed out",
		}, p.Admin.Email)
	})
}

// audit records an action the acting service does not audit itself.
func (s *Service) audit(ctx context.Context, p Principal, target, action, reason, details string) error {
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: target, Action: action, Actor: p.Admin.Email, Reason: reason, Details: details,
		}, p.Admin.Email)
	})
}

func needReason(reason string) error {
	if len(strings.TrimSpace(reason)) < 3 {
		return apperr.Invalid("a reason of at least 3 characters is required")
	}
	return nil
}

// UserView is an account with its balances.
type UserView struct {
	User     ports.User
	Balances []ports.Balance
}

// FindUser looks an account up by user ID, email address or phone number.
func (s *Service) FindUser(ctx context.Context, p Principal, query string) (UserView, error) {
	if err := p.require(domain.PermUsersRead); err != nil {
		return UserView{}, err
	}
	id := strings.TrimSpace(query)
	if _, err := uuid.Parse(id); err != nil {
		if id, err = s.Users.Find(ctx, id); err != nil {
			return UserView{}, err
		}
	}
	u, err := s.Users.Get(ctx, id)
	if err != nil {
		return UserView{}, err
	}
	b, err := s.Users.Balances(ctx, id)
	if err != nil {
		return UserView{}, err
	}
	return UserView{User: u, Balances: b}, nil
}

// ChangeUserStatus moves an account to another status; user-service
// records the change and its audit event with this administrator as the
// actor.
func (s *Service) ChangeUserStatus(ctx context.Context, p Principal, userID, to, reason, note string) (string, error) {
	if err := p.require(domain.PermUsersStatus); err != nil {
		return "", err
	}
	return s.Users.ChangeStatus(ctx, userID, to, reason, p.Admin.Email, note)
}

// CancelOrders cancels every open order of a user.
func (s *Service) CancelOrders(ctx context.Context, p Principal, userID, reason string) error {
	if err := p.require(domain.PermOrdersCancel); err != nil {
		return err
	}
	if err := needReason(reason); err != nil {
		return err
	}
	if _, err := uuid.Parse(userID); err != nil {
		return apperr.Invalid("user_id must be a UUID")
	}
	if err := s.Orders.CancelAll(ctx, userID); err != nil {
		return err
	}
	return s.audit(ctx, p, "user:"+userID, "admin.orders.cancel_all", reason, "{}")
}

// Withdrawals returns a page of withdrawals (default: PENDING_REVIEW,
// oldest first).
func (s *Service) Withdrawals(ctx context.Context, p Principal, q ports.WithdrawalQuery) ([]byte, error) {
	if err := p.require(domain.PermWithdrawalsRead); err != nil {
		return nil, err
	}
	q.Status, q.Asset, q.Network, q.Order = strings.ToUpper(q.Status), strings.ToUpper(q.Asset), strings.ToUpper(q.Network), strings.ToLower(q.Order)
	if q.Order != "" && q.Order != "asc" && q.Order != "desc" {
		return nil, apperr.Invalid("order must be asc or desc")
	}
	if q.UserID != "" {
		if _, err := uuid.Parse(q.UserID); err != nil {
			return nil, apperr.Invalid("user_id must be a user ID")
		}
	}
	q.Limit = pageLimit(q.Limit)
	return s.Wallet.List(ctx, q)
}

// pageLimit bounds a page: 1 to 200, 50 by default.
func pageLimit(n int) int {
	if n <= 0 || n > 200 {
		return 50
	}
	return n
}

// ReviewWithdrawal approves or rejects a withdrawal in review; the wallet
// records it with this administrator as the reviewer and audits it. In
// single-person mode one approval completes a withdrawal worth at most
// the settings' WithdrawalMax, however many reviewers it needs.
func (s *Service) ReviewWithdrawal(ctx context.Context, p Principal, id string, approve bool, reason string) ([]byte, error) {
	if err := p.require(domain.PermWithdrawalsEdit); err != nil {
		return nil, err
	}
	if err := needReason(reason); err != nil {
		return nil, err
	}
	var soleMax decimal.Decimal
	if approve && !s.TwoPerson() {
		set, err := s.settings(ctx, s.Store.Read())
		if err != nil {
			return nil, err
		}
		soleMax = set.WithdrawalMax
	}
	return s.Wallet.Review(ctx, id, approve, p.Admin.Email, reason, soleMax)
}

// MaxBatch bounds a batch review.
const MaxBatch = 50

// BatchResult is one withdrawal of a batch review: its status after the
// review, or why it failed.
type BatchResult struct {
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Status  string `json:"status,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// ReviewBatch approves or rejects withdrawals one by one with one reason
// (each reviewed, and audited by the wallet, on its own); a failure leaves
// the others decided.
func (s *Service) ReviewBatch(ctx context.Context, p Principal, ids []string, approve bool, reason string) ([]BatchResult, error) {
	if err := p.require(domain.PermWithdrawalsEdit); err != nil {
		return nil, err
	}
	if err := needReason(reason); err != nil {
		return nil, err
	}
	if len(ids) == 0 || len(ids) > MaxBatch {
		return nil, apperr.Invalid(fmt.Sprintf("a batch has 1 to %d withdrawals", MaxBatch))
	}
	seen := map[string]bool{}
	out := make([]BatchResult, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		raw, err := s.ReviewWithdrawal(ctx, p, id, approve, reason)
		if err != nil {
			e := apperr.From(err)
			out = append(out, BatchResult{ID: id, Code: e.Code, Message: e.Message})
			continue
		}
		var w struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(raw, &w)
		out = append(out, BatchResult{ID: id, OK: true, Status: w.Status})
	}
	return out, nil
}

// Custody describes the custody wallet (ADR-0011).
func (s *Service) Custody(ctx context.Context, p Principal) ([]byte, error) {
	if err := p.require(domain.PermWithdrawalsRead); err != nil {
		return nil, err
	}
	return s.Wallet.Custody(ctx)
}

// CustodyCallbacks returns a page of the custodian's callbacks.
func (s *Service) CustodyCallbacks(ctx context.Context, p Principal, q ports.CallbackQuery) ([]byte, error) {
	if err := p.require(domain.PermWithdrawalsRead); err != nil {
		return nil, err
	}
	q.Result, q.Kind, q.Limit = strings.ToUpper(q.Result), strings.ToUpper(q.Kind), pageLimit(q.Limit)
	return s.Wallet.Callbacks(ctx, q)
}

// CustodyCallback returns one callback as received.
func (s *Service) CustodyCallback(ctx context.Context, p Principal, id string) ([]byte, error) {
	if err := p.require(domain.PermWithdrawalsRead); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, apperr.NotFound("no such callback")
	}
	return s.Wallet.Callback(ctx, id)
}

// ReplayCallback applies a stored callback again; the wallet audits it
// with this administrator as the actor.
func (s *Service) ReplayCallback(ctx context.Context, p Principal, id, reason string) ([]byte, error) {
	if err := p.require(domain.PermWithdrawalsEdit); err != nil {
		return nil, err
	}
	if err := needReason(reason); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, apperr.NotFound("no such callback")
	}
	return s.Wallet.Replay(ctx, id, p.Admin.Email, reason)
}

// Instruments lists assets and pairs.
func (s *Service) Instruments(ctx context.Context, p Principal) ([]byte, error) {
	if err := p.require(domain.PermInstrumentsRead); err != nil {
		return nil, err
	}
	return s.Catalog.List(ctx)
}

// FlagList returns the feature flags.
func (s *Service) FlagList(ctx context.Context, p Principal) ([]ports.Flag, error) {
	if err := p.require(domain.PermFlagsRead); err != nil {
		return nil, err
	}
	return s.Flags.List(ctx)
}

// SwitchFlag turns a flag on or off; the change is audited with it. The
// console's own flags (admin.*: sign-in and two-person approval) take the
// right to change its settings too.
func (s *Service) SwitchFlag(ctx context.Context, p Principal, key string, enabled bool, reason string) (ports.Flag, error) {
	if err := p.require(domain.PermFlagsEdit); err != nil {
		return ports.Flag{}, err
	}
	if strings.HasPrefix(key, "admin.") {
		if err := p.require(domain.PermSettingsEdit); err != nil {
			return ports.Flag{}, err
		}
	}
	if err := needReason(reason); err != nil {
		return ports.Flag{}, err
	}
	return s.Flags.Switch(ctx, key, enabled, p.Admin.Email, reason)
}

// Adjustment is a requested manual ledger adjustment.
type Adjustment struct {
	UserID string
	Asset  string
	Amount decimal.Decimal
	Reason string
}

// DerivativesContracts returns each perpetual contract's status,
// reduce-only state, mark price and open interest.
func (s *Service) DerivativesContracts(ctx context.Context, p Principal) ([]byte, error) {
	if err := p.require(domain.PermDerivativesRead); err != nil {
		return nil, err
	}
	return s.Derivatives.Contracts(ctx)
}

// LiftReduceOnly ends a contract's reduce-only (requirements §11.7: the
// system degrades on its own, a person decides the market is sound again).
func (s *Service) LiftReduceOnly(ctx context.Context, p Principal, symbol, reason string) ([]byte, error) {
	if err := p.require(domain.PermDerivativesEdit); err != nil {
		return nil, err
	}
	if err := needReason(reason); err != nil {
		return nil, err
	}
	raw, err := s.Derivatives.LiftReduceOnly(ctx, symbol, p.Admin.Email)
	if err != nil {
		return nil, err
	}
	return raw, s.audit(ctx, p, "contract:"+strings.ToUpper(symbol), "admin.derivatives.reduce_only_lifted", reason, string(raw))
}

// DerivativesRisk returns the positions warned, taken over by the
// liquidation engine or close to it.
func (s *Service) DerivativesRisk(ctx context.Context, p Principal) ([]byte, error) {
	if err := p.require(domain.PermDerivativesRead); err != nil {
		return nil, err
	}
	return s.Derivatives.Risk(ctx)
}

// System accounts of perpetual contracts in the ledger.
const (
	accountInsuranceFund = "INSURANCE_FUND"
	accountPnLClearing   = "PNL_CLEARING"
)

// InsuranceFund is the insurance fund's balance in an asset, with the
// PnL clearing account's (the open positions' unsettled results; it may
// be negative).
type InsuranceFund struct {
	Asset       string `json:"asset"`
	Balance     string `json:"balance"`
	PnLClearing string `json:"pnl_clearing"`
}

func assetOrUSDT(asset string) string {
	if asset = strings.ToUpper(strings.TrimSpace(asset)); asset == "" {
		return "USDT"
	}
	return asset
}

// InsuranceFund returns the insurance fund of an asset (default USDT).
func (s *Service) InsuranceFund(ctx context.Context, p Principal, asset string) (InsuranceFund, error) {
	if err := p.require(domain.PermDerivativesRead); err != nil {
		return InsuranceFund{}, err
	}
	asset = assetOrUSDT(asset)
	list, err := s.Ledger.SystemBalances(ctx, asset)
	if err != nil {
		return InsuranceFund{}, err
	}
	out := InsuranceFund{Asset: asset, Balance: "0", PnLClearing: "0"}
	for _, b := range list {
		switch b.AccountType {
		case accountInsuranceFund:
			out.Balance = b.Available
		case accountPnLClearing:
			out.PnLClearing = b.Available
		}
	}
	return out, nil
}

// Liquidation step kinds of the read model.
var liquidationKinds = []string{"", "WARNING", "STARTED", "FILLED", "ADL"}

// Liquidations returns a page of the liquidation steps of the last days
// (default 7, at most 90), of one kind (WARNING, STARTED, FILLED, ADL)
// or all, newest first; limit defaults to 100, at most 500.
func (s *Service) Liquidations(ctx context.Context, p Principal, q ports.LiquidationQuery) ([]ports.LiquidationStep, string, error) {
	if err := p.require(domain.PermDerivativesRead); err != nil {
		return nil, "", err
	}
	q.Kind = strings.ToUpper(strings.TrimSpace(q.Kind))
	if !slices.Contains(liquidationKinds, q.Kind) {
		return nil, "", apperr.Invalid("kind must be WARNING, STARTED, FILLED or ADL")
	}
	if q.UserID != "" {
		if _, err := uuid.Parse(q.UserID); err != nil {
			return nil, "", apperr.Invalid("user_id must be a UUID")
		}
	}
	q.Symbol = strings.ToUpper(strings.TrimSpace(q.Symbol))
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	q.Days = reportDays(q.Days)
	return s.Reports.Liquidations(ctx, q)
}

// AuditLogs returns a page of the audit trail, newest first; limit
// defaults to 100, at most 500.
func (s *Service) AuditLogs(ctx context.Context, p Principal, q ports.AuditQuery) ([]ports.AuditEntry, string, error) {
	if err := p.require(domain.PermAuditRead); err != nil {
		return nil, "", err
	}
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	return s.AuditLog.Search(ctx, q)
}

// Intervals of the candle report, in seconds.
var intervals = map[string]uint32{"1m": 60, "5m": 300, "15m": 900, "1h": 3600, "4h": 14400, "1d": 86400}

func reportDays(days int) int {
	if days <= 0 {
		return 7
	}
	return min(days, 90)
}

// TradingReport returns trades and orders per symbol and day for the last
// days (default 7, at most 90), from the ClickHouse read models.
func (s *Service) TradingReport(ctx context.Context, p Principal, days int) ([]ports.TradingDay, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return nil, err
	}
	return s.Reports.Trading(ctx, reportDays(days))
}

// WalletReport returns deposits and withdrawals per asset and day.
func (s *Service) WalletReport(ctx context.Context, p Principal, days int) ([]ports.WalletDay, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return nil, err
	}
	return s.Reports.Wallet(ctx, reportDays(days))
}

// DerivativesReport returns each contract's fills, fees, results,
// funding and liquidations per day for the last days.
func (s *Service) DerivativesReport(ctx context.Context, p Principal, days int) ([]ports.DerivativesDay, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return nil, err
	}
	return s.Reports.Derivatives(ctx, reportDays(days))
}

// OpenInterest returns each contract's open positions from the read
// model.
func (s *Service) OpenInterest(ctx context.Context, p Principal) ([]ports.OpenInterest, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return nil, err
	}
	return s.Reports.OpenInterest(ctx)
}

// CandleReport returns the newest candles of a symbol (default 48, at most
// 500) at an interval of 1m, 5m, 15m, 1h, 4h or 1d.
func (s *Service) CandleReport(ctx context.Context, p Principal, symbol, interval string, limit int) ([]ports.Candle, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return nil, err
	}
	seconds, ok := intervals[interval]
	if !ok {
		return nil, apperr.Invalid("interval must be 1m, 5m, 15m, 1h, 4h or 1d")
	}
	if symbol = strings.ToUpper(strings.TrimSpace(symbol)); symbol == "" {
		return nil, apperr.Invalid("symbol is required")
	}
	if limit <= 0 || limit > 500 {
		limit = 48
	}
	return s.Reports.Candles(ctx, symbol, seconds, limit)
}

// NewAdmin creates an administrator (exchangectl admin create) with a
// password and an authenticator secret; nobody else sees either.
func NewAdmin(ctx context.Context, store ports.Store, hasher *password.Hasher, box *secretbox.Box, email, name, role, pw string,
	secret []byte, actor string, now time.Time,
) (domain.Admin, error) {
	return createAdmin(ctx, store, hasher, box, email, name, role, pw, secret, actor, "new administrator", now)
}

// createAdmin creates an administrator, audited as admin.created with the
// reason.
func createAdmin(ctx context.Context, store ports.Store, hasher *password.Hasher, box *secretbox.Box, email, name, role, pw string,
	secret []byte, actor, reason string, now time.Time,
) (domain.Admin, error) {
	if len(pw) < 12 {
		return domain.Admin{}, apperr.Invalid("the password needs at least 12 characters")
	}
	if len(secret) < 16 {
		return domain.Admin{}, apperr.Invalid("the authenticator secret needs at least 128 bits")
	}
	a, err := domain.NewAdmin(uuid.Must(uuid.NewV7()).String(), email, name, role, now)
	if err != nil {
		return domain.Admin{}, err
	}
	a.PasswordHash = hasher.Hash(pw)
	a.TOTPSealed = box.Seal(secret, []byte(a.ID))
	return a, store.Tx(ctx, func(r ports.Repos) error {
		if existing, err := r.Admins().ByEmail(ctx, a.Email); err != nil || existing != nil {
			if existing != nil {
				return apperr.New(apperr.KindConflict, "ADMIN_EXISTS", "an administrator with this email exists")
			}
			return err
		}
		if err := r.Admins().Insert(ctx, a); err != nil {
			return err
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "admin:" + a.ID, Action: "admin.created", Actor: actor, Reason: reason,
			Details: fmt.Sprintf(`{"email":%q,"role":%q}`, a.Email, a.Role),
		}, actor)
	})
}

// DisableAdmin disables an administrator and ends their sessions.
func DisableAdmin(ctx context.Context, store ports.Store, email, actor, reason string, now time.Time) error {
	if err := needReason(reason); err != nil {
		return err
	}
	return store.Tx(ctx, func(r ports.Repos) error {
		a, err := r.Admins().ByEmailForUpdate(ctx, strings.ToLower(strings.TrimSpace(email)))
		if err != nil {
			return err
		}
		if a == nil {
			return apperr.NotFound("no such administrator")
		}
		a.Status = domain.StatusDisabled
		if err := r.Admins().Update(ctx, *a); err != nil {
			return err
		}
		if err := r.Sessions().RevokeAll(ctx, a.ID, now); err != nil {
			return err
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "admin:" + a.ID, Action: "admin.disabled", Actor: actor, Reason: reason, Details: "{}",
		}, actor)
	})
}
