package backends

import (
	"context"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/admin/ports"
)

// Who holds an asset, from the ledger's lines (ClickHouse ledger_entries:
// every line once, FINAL). A user's balance is the sum of its lines in
// all its accounts; a system account's the sum of its own.
const (
	// holdingsByKind sums the users' balances, the bots' apart, with how
	// many of each hold some.
	holdingsByKind = `SELECT has(?, owner_id) AS bot, countIf(balance > 0), sum(balance) FROM
		(SELECT owner_id, sum(amount) AS balance FROM ledger_entries FINAL WHERE asset = ? AND owner_type = 'USER' GROUP BY owner_id)
		GROUP BY bot`
	holdingsTop = `SELECT owner_id, sum(amount) AS balance FROM ledger_entries FINAL WHERE asset = ? AND owner_type = 'USER'
		GROUP BY owner_id HAVING balance > 0 ORDER BY balance DESC, owner_id LIMIT ?`
	holdingsSystem = `SELECT account_type, sum(amount) AS balance FROM ledger_entries FINAL WHERE asset = ? AND owner_type = 'SYSTEM'
		GROUP BY account_type HAVING balance != 0 ORDER BY account_type`
)

// Holdings sums who holds an asset: the bots apart from the other users,
// the system accounts and the top largest holders.
func (r Reports) Holdings(ctx context.Context, asset string, bots []string, top int) (ports.Holdings, error) {
	out := ports.Holdings{System: map[string]decimal.Decimal{}, Top: []ports.Holder{}}
	rows, err := r.Conn.Query(ctx, holdingsByKind, botList(bots), asset)
	if err != nil {
		return ports.Holdings{}, unavailable(err)
	}
	for rows.Next() {
		var bot uint8
		var holders uint64
		var amount decimal.Decimal
		if err := rows.Scan(&bot, &holders, &amount); err != nil {
			_ = rows.Close()
			return ports.Holdings{}, unavailable(err)
		}
		if bot == 1 {
			out.Bots, out.BotHolders = amount, holders
		} else {
			out.Users, out.UserHolders = amount, holders
		}
	}
	if err := closeRows(rows); err != nil {
		return ports.Holdings{}, err
	}
	if rows, err = r.Conn.Query(ctx, holdingsTop, asset, top); err != nil {
		return ports.Holdings{}, unavailable(err)
	}
	for rows.Next() {
		var h ports.Holder
		if err := rows.Scan(&h.UserID, &h.Amount); err != nil {
			_ = rows.Close()
			return ports.Holdings{}, unavailable(err)
		}
		out.Top = append(out.Top, h)
	}
	if err := closeRows(rows); err != nil {
		return ports.Holdings{}, err
	}
	if rows, err = r.Conn.Query(ctx, holdingsSystem, asset); err != nil {
		return ports.Holdings{}, unavailable(err)
	}
	for rows.Next() {
		var account string
		var amount decimal.Decimal
		if err := rows.Scan(&account, &amount); err != nil {
			_ = rows.Close()
			return ports.Holdings{}, unavailable(err)
		}
		out.System[account] = amount
	}
	if err := closeRows(rows); err != nil {
		return ports.Holdings{}, err
	}
	return out, nil
}

// closeRows ends a read, with the error that stopped it.
func closeRows(rows interface {
	Err() error
	Close() error
},
) error {
	err := rows.Err()
	if cerr := rows.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return unavailable(err)
	}
	return nil
}
