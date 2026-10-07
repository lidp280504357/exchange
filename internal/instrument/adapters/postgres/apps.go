package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/skill/exchange/internal/instrument/domain"
	"github.com/skill/exchange/internal/instrument/ports"
)

type apps repos

func (r repos) Apps() ports.AppRepo { return apps(r) }

// appFileJSON is a file as the current, mobileconfig and files columns
// keep it.
type appFileJSON struct {
	FileID     string    `json:"file_id"`
	Kind       string    `json:"kind"`
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	SHA256     string    `json:"sha256"`
	StoredAs   string    `json:"stored_as"`
	Manifest   string    `json:"manifest"`
	Origin     string    `json:"origin"`
	Package    string    `json:"package"`
	Version    string    `json:"version"`
	Build      string    `json:"build"`
	MinOS      string    `json:"min_os"`
	UploadedAt time.Time `json:"uploaded_at"`
	UploadedBy string    `json:"uploaded_by"`
}

func appFileOf(f domain.AppFileInfo) appFileJSON {
	return appFileJSON{
		FileID: f.FileID, Kind: f.Kind, Name: f.Name, Size: f.Size, SHA256: f.SHA256, StoredAs: f.StoredAs, Manifest: f.Manifest,
		Origin: f.Origin, Package: f.Package, Version: f.Version, Build: f.Build, MinOS: f.MinOS, UploadedAt: f.UploadedAt.UTC(),
		UploadedBy: f.UploadedBy,
	}
}

func (j appFileJSON) info() domain.AppFileInfo {
	return domain.AppFileInfo{
		FileID: j.FileID, Kind: j.Kind, Name: j.Name, Size: j.Size, SHA256: j.SHA256, StoredAs: j.StoredAs, Manifest: j.Manifest,
		Origin: j.Origin, Package: j.Package, Version: j.Version, Build: j.Build, MinOS: j.MinOS, UploadedAt: j.UploadedAt,
		UploadedBy: j.UploadedBy,
	}
}

// fileColumn is a file as its column keeps it, NULL for none.
func fileColumn(f *domain.AppFileInfo) ([]byte, error) {
	if f == nil {
		return nil, nil
	}
	return json.Marshal(appFileOf(*f))
}

func fileOfColumn(raw []byte) (*domain.AppFileInfo, error) {
	if raw == nil {
		return nil, nil
	}
	var j appFileJSON
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, err
	}
	f := j.info()
	return &f, nil
}

const appColumns = `platform, mode, link_url, enabled, current, mobileconfig, files, notes, version, updated_by, updated_at`

func scanApp(row pgx.Row) (domain.PlatformApp, error) {
	var a domain.PlatformApp
	var current, mobileconfig, files, notes []byte
	if err := row.Scan(&a.Platform, &a.Mode, &a.LinkURL, &a.Enabled, &current, &mobileconfig, &files, &notes, &a.Version, &a.UpdatedBy,
		&a.UpdatedAt); err != nil {
		return domain.PlatformApp{}, err
	}
	var err error
	if a.Current, err = fileOfColumn(current); err != nil {
		return domain.PlatformApp{}, fmt.Errorf("decode %s's app: %w", a.Platform, err)
	}
	if a.Mobileconfig, err = fileOfColumn(mobileconfig); err != nil {
		return domain.PlatformApp{}, fmt.Errorf("decode %s's configuration profile: %w", a.Platform, err)
	}
	var list []appFileJSON
	if err := json.Unmarshal(files, &list); err != nil {
		return domain.PlatformApp{}, fmt.Errorf("decode %s's files: %w", a.Platform, err)
	}
	for _, f := range list {
		a.Files = append(a.Files, f.info())
	}
	if err := json.Unmarshal(notes, &a.Notes); err != nil {
		return domain.PlatformApp{}, fmt.Errorf("decode %s's notes: %w", a.Platform, err)
	}
	a.Normalize()
	return a, nil
}

func (r apps) List(ctx context.Context) ([]domain.PlatformApp, error) {
	rows, err := r.q.Query(ctx, `SELECT `+appColumns+` FROM platform_apps ORDER BY platform = 'IOS', platform`)
	if err != nil {
		return nil, fmt.Errorf("list the platform apps: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.PlatformApp, error) { return scanApp(row) })
}

func (r apps) GetForUpdate(ctx context.Context, platform string) (domain.PlatformApp, error) {
	a, err := scanApp(r.q.QueryRow(ctx, `SELECT `+appColumns+` FROM platform_apps WHERE platform = $1 FOR UPDATE`, platform))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PlatformApp{}, domain.ErrNoSuchApp
	}
	if err != nil {
		return domain.PlatformApp{}, fmt.Errorf("read the platform app %s: %w", platform, err)
	}
	return a, nil
}

func (r apps) Save(ctx context.Context, a domain.PlatformApp) (domain.PlatformApp, error) {
	a.Normalize()
	current, err := fileColumn(a.Current)
	if err != nil {
		return domain.PlatformApp{}, err
	}
	mobileconfig, err := fileColumn(a.Mobileconfig)
	if err != nil {
		return domain.PlatformApp{}, err
	}
	list := make([]appFileJSON, 0, len(a.Files))
	for _, f := range a.Files {
		list = append(list, appFileOf(f))
	}
	files, err := json.Marshal(list)
	if err != nil {
		return domain.PlatformApp{}, err
	}
	notes, err := json.Marshal(a.Notes)
	if err != nil {
		return domain.PlatformApp{}, err
	}
	saved, err := scanApp(r.q.QueryRow(ctx, `UPDATE platform_apps SET mode = $2, link_url = $3, enabled = $4, current = $5, mobileconfig = $6,
		files = $7, notes = $8, version = version + 1, updated_by = $9, updated_at = $10 WHERE platform = $1 RETURNING `+appColumns,
		a.Platform, a.Mode, a.LinkURL, a.Enabled, current, mobileconfig, files, notes, a.UpdatedBy, a.UpdatedAt))
	if err != nil {
		return domain.PlatformApp{}, fmt.Errorf("save the platform app %s: %w", a.Platform, err)
	}
	return saved, nil
}

const entryColumns = `visible, version, updated_by, updated_at`

func scanEntry(row pgx.Row) (domain.AppEntry, error) {
	var e domain.AppEntry
	if err := row.Scan(&e.Visible, &e.Version, &e.UpdatedBy, &e.UpdatedAt); err != nil {
		return domain.AppEntry{}, err
	}
	return e, nil
}

func (r apps) Entry(ctx context.Context) (domain.AppEntry, error) {
	e, err := scanEntry(r.q.QueryRow(ctx, `SELECT `+entryColumns+` FROM platform_download_entry`))
	if err != nil {
		return domain.AppEntry{}, fmt.Errorf("read the download entry: %w", err)
	}
	return e, nil
}

func (r apps) EntryForUpdate(ctx context.Context) (domain.AppEntry, error) {
	e, err := scanEntry(r.q.QueryRow(ctx, `SELECT `+entryColumns+` FROM platform_download_entry FOR UPDATE`))
	if err != nil {
		return domain.AppEntry{}, fmt.Errorf("read the download entry: %w", err)
	}
	return e, nil
}

func (r apps) SaveEntry(ctx context.Context, e domain.AppEntry) (domain.AppEntry, error) {
	saved, err := scanEntry(r.q.QueryRow(ctx, `UPDATE platform_download_entry SET visible = $1, version = version + 1, updated_by = $2,
		updated_at = $3 RETURNING `+entryColumns, e.Visible, e.UpdatedBy, e.UpdatedAt))
	if err != nil {
		return domain.AppEntry{}, fmt.Errorf("save the download entry: %w", err)
	}
	return saved, nil
}
