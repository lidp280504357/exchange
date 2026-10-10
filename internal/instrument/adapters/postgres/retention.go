package postgres

import (
	"context"

	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/retention"
)

// Retention deletes instrument-service's history older than the window
// (M1): the record of reference-data changes, but each entity's and key's
// latest, and its latest edit - LastEdit's, the file's or the console's,
// which a later status or profile change does not replace (B200, B201:
// instruments apply keeps what the console changed last by its source, so
// the next deploy would put test.json back over a fee or a limit changed
// in the console). Assets, networks, pairs, contracts, fee schedules and
// the platform's profile are state and stay.
type Retention struct{}

// Schema is instrument-service's.
func (Retention) Schema() string { return "instrument" }

// Run applies the rules.
func (Retention) Run(ctx context.Context, db *pg.DB, w retention.Window) ([]retention.Result, error) {
	return retention.ApplyAll(ctx, db, w, []retention.Rule{
		{
			Table: "config_history", Name: "changes before the window but each entity's and key's latest and latest edit", Cutoff: w.History(),
			Where: `created_at < $1 AND id NOT IN (
				SELECT DISTINCT ON (entity, key) id FROM config_history ORDER BY entity, key, version DESC, id DESC)
				AND NOT (source IN ('', 'FILE', 'CONSOLE') AND NOT EXISTS (
					SELECT 1 FROM config_history later WHERE later.entity = config_history.entity AND later.key = config_history.key
						AND later.source IN ('', 'FILE', 'CONSOLE') AND later.version > config_history.version))`,
		},
	})
}
