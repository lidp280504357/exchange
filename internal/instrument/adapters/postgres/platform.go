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

type platform repos

func (r repos) Platform() ports.PlatformRepo { return platform(r) }

// platformTexts is the texts column: what is not a column of its own.
type platformTexts struct {
	Footer struct {
		Copyright  domain.Texts `json:"copyright"`
		Compliance domain.Texts `json:"compliance"`
	} `json:"footer"`
	Contact struct {
		Email      string `json:"email"`
		SupportURL string `json:"support_url"`
	} `json:"contact"`
	Social                 []socialJSON `json:"social"`
	LearningText           domain.Texts `json:"learning_text"`
	RegistrationClosedText domain.Texts `json:"registration_closed_text"`
}

type socialJSON struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

const platformColumns = `name, short_name, domain, theme_color, brand_color, default_locale, learning_enabled, registration_status, texts,
	version, updated_by, updated_at`

func scanPlatform(row pgx.Row) (domain.PlatformProfile, error) {
	var p domain.PlatformProfile
	var raw []byte
	if err := row.Scan(&p.Name, &p.ShortName, &p.Domain, &p.ThemeColor, &p.BrandColor, &p.DefaultLocale, &p.Learning.Enabled,
		&p.Registration.Status, &raw, &p.Version, &p.UpdatedBy, &p.UpdatedAt); err != nil {
		return domain.PlatformProfile{}, fmt.Errorf("read the platform profile: %w", err)
	}
	var t platformTexts
	if err := json.Unmarshal(raw, &t); err != nil {
		return domain.PlatformProfile{}, fmt.Errorf("decode the platform profile: %w", err)
	}
	p.Footer = domain.PlatformFooter{Copyright: t.Footer.Copyright, Compliance: t.Footer.Compliance}
	p.Contact = domain.PlatformContact{Email: t.Contact.Email, SupportURL: t.Contact.SupportURL}
	for _, s := range t.Social {
		p.Social = append(p.Social, domain.SocialLink{Kind: s.Kind, URL: s.URL})
	}
	p.Learning.Text, p.Registration.ClosedText = t.LearningText, t.RegistrationClosedText
	return p, nil
}

func (r platform) get(ctx context.Context, lock string) (domain.PlatformProfile, error) {
	p, err := scanPlatform(r.q.QueryRow(ctx, `SELECT `+platformColumns+` FROM platform_profile WHERE id = 1`+lock))
	if err != nil {
		return domain.PlatformProfile{}, err
	}
	rows, err := r.q.Query(ctx, `SELECT kind, mime, octet_length(data), width FROM platform_images`)
	if err != nil {
		return domain.PlatformProfile{}, fmt.Errorf("list the platform images: %w", err)
	}
	defer rows.Close()
	p.Images = map[string]domain.PlatformImage{}
	for rows.Next() {
		var kind string
		var img domain.PlatformImage
		if err := rows.Scan(&kind, &img.MIME, &img.Size, &img.Width); err != nil {
			return domain.PlatformProfile{}, err
		}
		p.Images[kind] = img
	}
	if err := rows.Err(); err != nil {
		return domain.PlatformProfile{}, err
	}
	p.Normalize()
	return p, nil
}

func (r platform) Get(ctx context.Context) (domain.PlatformProfile, error) { return r.get(ctx, "") }

func (r platform) GetForUpdate(ctx context.Context) (domain.PlatformProfile, error) {
	return r.get(ctx, " FOR UPDATE")
}

func (r platform) Save(ctx context.Context, p domain.PlatformProfile) (domain.PlatformProfile, error) {
	p.Normalize()
	var t platformTexts
	t.Footer.Copyright, t.Footer.Compliance = p.Footer.Copyright, p.Footer.Compliance
	t.Contact.Email, t.Contact.SupportURL = p.Contact.Email, p.Contact.SupportURL
	t.Social = make([]socialJSON, 0, len(p.Social))
	for _, s := range p.Social {
		t.Social = append(t.Social, socialJSON{Kind: s.Kind, URL: s.URL})
	}
	t.LearningText, t.RegistrationClosedText = p.Learning.Text, p.Registration.ClosedText
	raw, err := json.Marshal(t)
	if err != nil {
		return domain.PlatformProfile{}, err
	}
	tag, err := r.q.Exec(ctx, `UPDATE platform_profile SET name = $1, short_name = $2, domain = $3, theme_color = $4, brand_color = $5,
		default_locale = $6, learning_enabled = $7, registration_status = $8, texts = $9, version = version + 1, updated_by = $10,
		updated_at = $11 WHERE id = 1`,
		p.Name, p.ShortName, p.Domain, p.ThemeColor, p.BrandColor, p.DefaultLocale, p.Learning.Enabled, p.Registration.Status, raw,
		p.UpdatedBy, p.UpdatedAt)
	if err != nil {
		return domain.PlatformProfile{}, fmt.Errorf("save the platform profile: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.PlatformProfile{}, errors.New("save the platform profile: no row")
	}
	return r.Get(ctx)
}

func (r platform) Image(ctx context.Context, kind string) (*domain.Logo, int64, error) {
	var l domain.Logo
	var version int64
	err := r.q.QueryRow(ctx, `SELECT i.data, i.mime, p.version FROM platform_images i, platform_profile p WHERE i.kind = $1`, kind).
		Scan(&l.Data, &l.MIME, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("read the platform image %s: %w", kind, err)
	}
	return &l, version, nil
}

func (r platform) SaveImage(ctx context.Context, kind string, img *domain.Logo, info domain.PlatformImage, at time.Time) error {
	var err error
	if img == nil {
		_, err = r.q.Exec(ctx, `DELETE FROM platform_images WHERE kind = $1`, kind)
	} else {
		_, err = r.q.Exec(ctx, `INSERT INTO platform_images (kind, data, mime, width, updated_at) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (kind) DO UPDATE SET data = EXCLUDED.data, mime = EXCLUDED.mime, width = EXCLUDED.width,
				updated_at = EXCLUDED.updated_at`, kind, img.Data, img.MIME, info.Width, at)
	}
	if err != nil {
		return fmt.Errorf("save the platform image %s: %w", kind, err)
	}
	return nil
}
