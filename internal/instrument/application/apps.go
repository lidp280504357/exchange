package application

import (
	"context"
	"sync"
	"time"

	"github.com/skill/exchange/internal/instrument/domain"
	"github.com/skill/exchange/internal/instrument/ports"
)

// Apps keeps the apps to download (design 2026-10-07, App download page):
// each platform's mode, link, notes and switch as the console sets them,
// the files admin-service uploaded and stored, and the switch for the
// sites' download entries (H5). The sites read them on their pages, so
// they are served from memory for profileTTL; a change made here drops
// them at once.
type Apps struct {
	Store ports.Store
	// Now is the clock; time.Now when nil.
	Now func() time.Time

	mu       sync.Mutex
	cached   []domain.PlatformApp
	cachedAt time.Time
	entry    *domain.AppEntry
	entryAt  time.Time
}

func (a *Apps) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// List returns both platforms, Android first, read at most profileTTL ago.
func (a *Apps) List(ctx context.Context) ([]domain.PlatformApp, error) {
	a.mu.Lock()
	if a.cached != nil && a.now().Sub(a.cachedAt) < profileTTL {
		defer a.mu.Unlock()
		return a.cached, nil
	}
	a.mu.Unlock()
	list, err := a.Store.Read().Apps().List(ctx)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.cached, a.cachedAt = list, a.now()
	a.mu.Unlock()
	return list, nil
}

// change applies fn to a platform's row, locked, on the version expected
// (any when expected is 0), saves it in actor's name and keeps the row
// before and after in the history.
func (a *Apps) change(ctx context.Context, platform string, expected int64, actor, reason string, fn func(*domain.PlatformApp) error) (domain.PlatformApp, error) {
	if !domain.ValidAppPlatform(platform) {
		return domain.PlatformApp{}, domain.ErrNoSuchApp
	}
	if err := validChange(actor, reason); err != nil {
		return domain.PlatformApp{}, err
	}
	var saved domain.PlatformApp
	err := a.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Apps().GetForUpdate(ctx, platform)
		if err != nil {
			return err
		}
		if expected != 0 && cur.Version != expected {
			return domain.ErrPlatformChanged
		}
		next := cur
		next.Files = append([]domain.AppFileInfo(nil), cur.Files...)
		if err := fn(&next); err != nil {
			return err
		}
		next.UpdatedBy, next.UpdatedAt = actor, a.now().UTC()
		if saved, err = r.Apps().Save(ctx, next); err != nil {
			return err
		}
		change := map[string]any{"old": appRecord(cur), "new": appRecord(saved)}
		return r.Record(ctx, "PLATFORM_APP", platform, saved.Version, change, actor, reason, SourceConsole)
	})
	if err != nil {
		return domain.PlatformApp{}, err
	}
	a.drop()
	return saved, nil
}

// drop forgets the cached platforms and entry after a change.
func (a *Apps) drop() {
	a.mu.Lock()
	a.cached, a.entry = nil, nil
	a.mu.Unlock()
}

// Entry returns the switch for the sites' download entries, read at most
// profileTTL ago.
func (a *Apps) Entry(ctx context.Context) (domain.AppEntry, error) {
	a.mu.Lock()
	if a.entry != nil && a.now().Sub(a.entryAt) < profileTTL {
		defer a.mu.Unlock()
		return *a.entry, nil
	}
	a.mu.Unlock()
	e, err := a.Store.Read().Apps().Entry(ctx)
	if err != nil {
		return domain.AppEntry{}, err
	}
	a.mu.Lock()
	a.entry, a.entryAt = &e, a.now()
	a.mu.Unlock()
	return e, nil
}

// SetEntry shows or hides the sites' download entries in actor's name,
// keeping the state before and after in the history, and returns it as
// saved and as it was under its row lock (the console audits from that,
// A89); switching it to the state it is in changes nothing (the two the
// same).
func (a *Apps) SetEntry(ctx context.Context, visible bool, actor, reason string) (saved, previous domain.AppEntry, err error) {
	if err := validChange(actor, reason); err != nil {
		return domain.AppEntry{}, domain.AppEntry{}, err
	}
	err = a.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Apps().EntryForUpdate(ctx)
		if err != nil {
			return err
		}
		previous = cur
		if cur.Visible == visible {
			saved = cur
			return nil
		}
		next := cur
		next.Visible, next.UpdatedBy, next.UpdatedAt = visible, actor, a.now().UTC()
		if saved, err = r.Apps().SaveEntry(ctx, next); err != nil {
			return err
		}
		change := map[string]any{"old": map[string]any{"visible": cur.Visible}, "new": map[string]any{"visible": saved.Visible}}
		return r.Record(ctx, "DOWNLOAD_ENTRY", "ENTRY", saved.Version, change, actor, reason, SourceConsole)
	})
	if err != nil {
		return domain.AppEntry{}, domain.AppEntry{}, err
	}
	a.drop()
	return saved, previous, nil
}

// Set changes a platform's mode, link, notes and switch as of the version
// expected (INSTRUMENT_PLATFORM_CHANGED on another).
func (a *Apps) Set(ctx context.Context, platform string, s domain.AppSetting, expected int64, actor, reason string) (domain.PlatformApp, error) {
	if expected <= 0 {
		return domain.PlatformApp{}, domain.ErrPlatformChanged
	}
	return a.change(ctx, platform, expected, actor, reason, func(p *domain.PlatformApp) error { return p.Apply(s) })
}

// AddFile keeps a file admin-service stored (domain.PlatformApp.AddFile)
// and returns the platform with the configuration profile it replaced,
// whose file is the caller's to delete.
func (a *Apps) AddFile(ctx context.Context, platform string, f domain.AppFileInfo, actor, reason string) (domain.PlatformApp, *domain.AppFileInfo, error) {
	var replaced *domain.AppFileInfo
	saved, err := a.change(ctx, platform, 0, actor, reason, func(p *domain.PlatformApp) error {
		var err error
		replaced, err = p.AddFile(f)
		return err
	})
	return saved, replaced, err
}

// DeleteFile forgets a file and returns the platform with it; deleting it
// from the disk is the caller's.
func (a *Apps) DeleteFile(ctx context.Context, platform, fileID, actor, reason string) (domain.PlatformApp, domain.AppFileInfo, error) {
	var gone domain.AppFileInfo
	saved, err := a.change(ctx, platform, 0, actor, reason, func(p *domain.PlatformApp) error {
		var err error
		gone, err = p.DeleteFile(fileID)
		return err
	})
	return saved, gone, err
}

// appRecord is a platform as the history keeps it: its settings and the
// files by ID.
func appRecord(a domain.PlatformApp) map[string]any {
	ids := func(f *domain.AppFileInfo) any {
		if f == nil {
			return nil
		}
		return f.FileID
	}
	files := make([]map[string]any, 0, len(a.Files))
	for _, f := range a.Files {
		files = append(files, map[string]any{
			"file_id": f.FileID, "kind": f.Kind, "name": f.Name, "size": f.Size, "sha256": f.SHA256, "package": f.Package, "version": f.Version,
			"build": f.Build,
		})
	}
	return map[string]any{
		"mode": a.Mode, "link_url": a.LinkURL, "enabled": a.Enabled, "notes": a.Notes, "current": ids(a.Current),
		"mobileconfig": ids(a.Mobileconfig), "files": files, "version": a.Version,
	}
}
