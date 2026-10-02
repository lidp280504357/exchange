// Package ports declares what instrument-service's application layer needs.
package ports

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/lidp280504357/exchange/internal/instrument/domain"
)

// Store is the unit of work over the instrument schema.
type Store interface {
	// Tx runs fn in one transaction with the events it emits.
	Tx(ctx context.Context, fn func(Repos) error) error
	// Read returns repositories outside any transaction.
	Read() Repos
}

// Repos groups the repositories of one transaction. Save methods insert
// or update a row and return it with its new version; Get methods return
// nil when the row does not exist.
type Repos interface {
	FeeSchedules() FeeRepo
	Assets() AssetRepo
	Networks() NetworkRepo
	Pairs() PairRepo
	Contracts() ContractRepo
	Profiles() ProfileRepo
	// Record appends a version to the configuration history; source says
	// where the change came from (FILE, CONSOLE, STATUS, PROFILE).
	Record(ctx context.Context, entity, key string, version int64, value any, actor, reason, source string) error
	// LastEdit returns an item's last edit in the history (status and
	// profile changes left out); nil when it has none.
	LastEdit(ctx context.Context, entity, key string) (*Edit, error)
	// Emit queues an instrument.events event keyed by aggregateID.
	Emit(ctx context.Context, msg proto.Message, aggregateType, aggregateID string) error
}

// Edit is a change of an item in the configuration history: where it came
// from, who made it and when.
type Edit struct {
	Source string
	Actor  string
	At     time.Time
}

// FeeRepo stores fee schedules.
type FeeRepo interface {
	Get(ctx context.Context, tier string) (*domain.FeeSchedule, error)
	List(ctx context.Context) ([]domain.FeeSchedule, error)
	Save(ctx context.Context, f domain.FeeSchedule) (domain.FeeSchedule, error)
}

// AssetRepo stores assets.
type AssetRepo interface {
	Get(ctx context.Context, code string) (*domain.Asset, error)
	List(ctx context.Context) ([]domain.Asset, error)
	Save(ctx context.Context, a domain.Asset) (domain.Asset, error)
}

// NetworkRepo stores networks.
type NetworkRepo interface {
	Get(ctx context.Context, asset, network string) (*domain.Network, error)
	List(ctx context.Context) ([]domain.Network, error)
	Save(ctx context.Context, n domain.Network) (domain.Network, error)
}

// PairRepo stores trading pairs.
type PairRepo interface {
	Get(ctx context.Context, symbol string) (*domain.TradingPair, error)
	// GetForUpdate is Get with a row lock.
	GetForUpdate(ctx context.Context, symbol string) (*domain.TradingPair, error)
	List(ctx context.Context) ([]domain.TradingPair, error)
	Save(ctx context.Context, p domain.TradingPair) (domain.TradingPair, error)
}

// ContractRepo stores perpetual contracts.
type ContractRepo interface {
	Get(ctx context.Context, symbol string) (*domain.Contract, error)
	// GetForUpdate is Get with a row lock.
	GetForUpdate(ctx context.Context, symbol string) (*domain.Contract, error)
	List(ctx context.Context) ([]domain.Contract, error)
	Save(ctx context.Context, c domain.Contract) (domain.Contract, error)
}

// ProfileRepo stores the assets' profiles (ASTRA design §5.3) on the asset
// rows; Get and List leave the logo itself out.
type ProfileRepo interface {
	// GetForUpdate returns an asset's profile with a row lock; nil
	// without the asset.
	GetForUpdate(ctx context.Context, code string) (*domain.AssetProfile, error)
	List(ctx context.Context) ([]domain.AssetProfile, error)
	// Logo returns an asset's logo, nil without one, and the profile
	// version.
	Logo(ctx context.Context, code string) (*domain.Logo, int64, error)
	// Save writes the profile's text and, unless logo is nil, its logo
	// (no data clears it), raising the profile version by one.
	Save(ctx context.Context, p domain.AssetProfile, logo *domain.Logo) (domain.AssetProfile, error)
}
