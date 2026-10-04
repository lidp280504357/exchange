package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/skill/exchange/internal/notification/domain"
	"github.com/skill/exchange/internal/notification/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// The announcements, help articles and operators' in-app messages
// (ports.ContentStore, ports.BroadcastStore).

const articleColumns = `id, section, slug, category, pinned, sort_order, modes, status, publish_at, version, updated_by, created_at, updated_at`

func scanArticle(row pgx.Row) (domain.Article, error) {
	var a domain.Article
	var id uuid.UUID
	var publishAt *time.Time
	err := row.Scan(&id, &a.Section, &a.Slug, &a.Category, &a.Pinned, &a.Order, &a.Modes, &a.Status, &publishAt, &a.Version, &a.UpdatedBy,
		&a.CreatedAt, &a.UpdatedAt)
	a.ID = id.String()
	if publishAt != nil {
		a.PublishAt = *publishAt
	}
	return a, err
}

// withTexts reads the texts of articles; head > 0 reads that many first
// characters of each body only.
func (s *Store) withTexts(ctx context.Context, q pgxQuerier, list []domain.Article, head int) ([]domain.Article, error) {
	if len(list) == 0 {
		return list, nil
	}
	ids := make([]string, len(list))
	at := map[string]int{}
	for i, a := range list {
		ids[i], at[a.ID] = a.ID, i
	}
	rows, err := q.Query(ctx, `SELECT article_id, locale, title, summary, CASE WHEN $2 > 0 THEN left(body, $2) ELSE body END
		FROM article_texts WHERE article_id = ANY($1::uuid[]) ORDER BY article_id, locale DESC`, ids, head)
	if err != nil {
		return nil, fmt.Errorf("article texts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var t domain.ArticleText
		if err := rows.Scan(&id, &t.Locale, &t.Title, &t.Summary, &t.Body); err != nil {
			return nil, fmt.Errorf("article texts: %w", err)
		}
		i := at[id.String()]
		list[i].Texts = append(list[i].Texts, t)
	}
	return list, rows.Err()
}

type pgxQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Articles returns a section's articles; with visibleAt only those shown
// then: announcements pinned first and newest first, help by category and
// order.
func (s *Store) Articles(ctx context.Context, section string, visibleAt time.Time) ([]domain.Article, error) {
	var visible *time.Time
	if !visibleAt.IsZero() {
		visible = &visibleAt
	}
	rows, err := s.db.Query(ctx, `SELECT `+articleColumns+` FROM articles
		WHERE section = $1 AND ($2::timestamptz IS NULL OR (status = 'PUBLISHED' AND publish_at <= $2))
		ORDER BY pinned DESC, coalesce(publish_at, created_at) DESC, category, sort_order, slug`, section, visible)
	if err != nil {
		return nil, fmt.Errorf("articles: %w", err)
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Article, error) { return scanArticle(row) })
	if err != nil {
		return nil, fmt.Errorf("articles: %w", err)
	}
	return s.withTexts(ctx, s.db, list, 0)
}

// publishedOrder is the sites' order of a section's articles, a total one
// so that pages do not overlap: announcements pinned first and newest
// first, help by category and order, the fixed pages by slug.
var publishedOrder = map[string]string{
	domain.SectionAnnouncement: `pinned DESC, publish_at DESC, id DESC`,
	domain.SectionHelp:         `category, sort_order, slug`,
	domain.SectionLegal:        `slug`,
	domain.SectionHome:         `slug`,
}

// modeOf is the mode of an article shown in test mode or live.
func modeOf(test bool) string {
	if test {
		return domain.ModeTest
	}
	return domain.ModeFormal
}

// PublishedPage returns limit of a section's articles published by at and
// shown in the mode, from offset, with the first ports.HeadLength
// characters of each body.
func (s *Store) PublishedPage(ctx context.Context, section string, test bool, at time.Time, offset, limit int) ([]domain.Article, error) {
	rows, err := s.db.Query(ctx, `SELECT `+articleColumns+` FROM articles
		WHERE section = $1 AND status = 'PUBLISHED' AND publish_at <= $2 AND modes IN ($5, 'BOTH')
		ORDER BY `+publishedOrder[section]+` LIMIT $3 OFFSET $4`, section, at, limit, offset, modeOf(test))
	if err != nil {
		return nil, fmt.Errorf("published articles: %w", err)
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Article, error) { return scanArticle(row) })
	if err != nil {
		return nil, fmt.Errorf("published articles: %w", err)
	}
	return s.withTexts(ctx, s.db, list, ports.HeadLength)
}

// Withdrawn returns the slugs of a section's articles of the mode taken
// off the sites.
func (s *Store) Withdrawn(ctx context.Context, section string, test bool) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT slug FROM articles WHERE section = $1 AND status = 'ARCHIVED' AND modes IN ($2, 'BOTH') ORDER BY slug`,
		section, modeOf(test))
	if err != nil {
		return nil, fmt.Errorf("withdrawn articles: %w", err)
	}
	slugs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("withdrawn articles: %w", err)
	}
	return slugs, nil
}

func (s *Store) article(ctx context.Context, where string, args ...any) (*domain.Article, error) {
	a, err := scanArticle(s.db.QueryRow(ctx, `SELECT `+articleColumns+` FROM articles WHERE `+where, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("article: %w", err)
	}
	list, err := s.withTexts(ctx, s.db, []domain.Article{a}, 0)
	if err != nil {
		return nil, err
	}
	return &list[0], nil
}

// Article reads a section's article of the mode by its slug (one at most:
// a slug has one article per mode).
func (s *Store) Article(ctx context.Context, section, slug string, test bool) (*domain.Article, error) {
	return s.article(ctx, `section = $1 AND slug = $2 AND modes IN ($3, 'BOTH')`, section, slug, modeOf(test))
}

// ArticleByID reads an article by its ID.
func (s *Store) ArticleByID(ctx context.Context, id string) (*domain.Article, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, nil
	}
	return s.article(ctx, `id = $1`, id)
}

func publishAt(a domain.Article) *time.Time {
	if a.PublishAt.IsZero() {
		return nil
	}
	return &a.PublishAt
}

func insertTexts(ctx context.Context, tx pgx.Tx, a domain.Article) error {
	for _, t := range a.Texts {
		if _, err := tx.Exec(ctx, `INSERT INTO article_texts (article_id, locale, title, summary, body) VALUES ($1, $2, $3, $4, $5)`,
			a.ID, t.Locale, t.Title, t.Summary, t.Body); err != nil {
			return fmt.Errorf("article text: %w", err)
		}
	}
	return nil
}

// CreateArticle inserts an article with its texts.
func (s *Store) CreateArticle(ctx context.Context, a domain.Article) error {
	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO articles (`+articleColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
			a.ID, a.Section, a.Slug, a.Category, a.Pinned, a.Order, a.Modes, a.Status, publishAt(a), a.Version, a.UpdatedBy, a.CreatedAt,
			a.UpdatedAt)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.ErrArticleExists
		}
		if err != nil {
			return fmt.Errorf("create article: %w", err)
		}
		return insertTexts(ctx, tx, a)
	})
}

// UpdateArticle writes a over the article at version.
func (s *Store) UpdateArticle(ctx context.Context, a domain.Article, version int) error {
	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE articles SET slug = $3, category = $4, pinned = $5, sort_order = $6, status = $7, publish_at = $8,
			version = $9, updated_by = $10, updated_at = $11, modes = $12 WHERE id = $1 AND version = $2`,
			a.ID, version, a.Slug, a.Category, a.Pinned, a.Order, a.Status, publishAt(a), a.Version, a.UpdatedBy, a.UpdatedAt, a.Modes)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.ErrArticleExists
		}
		if err != nil {
			return fmt.Errorf("update article: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return apperr.New(apperr.KindConflict, apperr.CodeConflict, "the article changed meanwhile; reload it")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM article_texts WHERE article_id = $1`, a.ID); err != nil {
			return fmt.Errorf("article texts: %w", err)
		}
		return insertTexts(ctx, tx, a)
	})
}

const broadcastColumns = `b.id, b.audience, b.user_ids, b.title, b.body, b.link, b.email, b.status, b.cursor, b.recipients, b.created_by,
	b.created_at, b.finished_at, b.failures, b.last_error, b.retry_at`

func scanBroadcast(row pgx.Row, withRead bool) (domain.Broadcast, error) {
	var b domain.Broadcast
	var id uuid.UUID
	var users []uuid.UUID
	var title, body []byte
	var finished, retry *time.Time
	dest := []any{
		&id, &b.Audience, &users, &title, &body, &b.Link, &b.Email, &b.Status, &b.Cursor, &b.Recipients, &b.CreatedBy,
		&b.CreatedAt, &finished, &b.Failures, &b.LastError, &retry,
	}
	if withRead {
		dest = append(dest, &b.Read)
	}
	if err := row.Scan(dest...); err != nil {
		return b, err
	}
	b.ID = id.String()
	for _, u := range users {
		b.UserIDs = append(b.UserIDs, u.String())
	}
	if err := json.Unmarshal(title, &b.Title); err != nil {
		return b, fmt.Errorf("broadcast title: %w", err)
	}
	if err := json.Unmarshal(body, &b.Body); err != nil {
		return b, fmt.Errorf("broadcast body: %w", err)
	}
	if finished != nil {
		b.FinishedAt = *finished
	}
	if retry != nil {
		b.RetryAt = *retry
	}
	return b, nil
}

// readCount counts a broadcast's notices read.
const readCount = `(SELECT count(*) FROM notifications n WHERE n.data ? 'broadcast_id' AND n.data ->> 'broadcast_id' = b.id::text
	AND n.read_at IS NOT NULL)`

// CreateBroadcast inserts a broadcast, SENDING.
func (s *Store) CreateBroadcast(ctx context.Context, b domain.Broadcast) error {
	title, _ := json.Marshal(b.Title)
	body, _ := json.Marshal(b.Body)
	users := b.UserIDs
	if users == nil {
		users = []string{}
	}
	_, err := s.db.Exec(ctx, `INSERT INTO broadcasts (id, audience, user_ids, title, body, link, email, status, created_by, created_at)
		VALUES ($1, $2, $3::uuid[], $4, $5, $6, $7, $8, $9, $10)`, b.ID, b.Audience, users, title, body, b.Link, b.Email, b.Status, b.CreatedBy, b.CreatedAt)
	if err != nil {
		return fmt.Errorf("create broadcast: %w", err)
	}
	return nil
}

// Broadcast reads one with its read count.
func (s *Store) Broadcast(ctx context.Context, id string) (*domain.Broadcast, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, nil
	}
	b, err := scanBroadcast(s.db.QueryRow(ctx, `SELECT `+broadcastColumns+`, `+readCount+` FROM broadcasts b WHERE b.id = $1`, id), true)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("broadcast: %w", err)
	}
	return &b, nil
}

// Broadcasts pages through them, newest first.
func (s *Store) Broadcasts(ctx context.Context, beforeID string, limit int) ([]domain.Broadcast, error) {
	var before any
	if beforeID != "" {
		before = beforeID
	}
	rows, err := s.db.Query(ctx, `SELECT `+broadcastColumns+`, `+readCount+` FROM broadcasts b
		WHERE ($1::uuid IS NULL OR b.id < $1::uuid) ORDER BY b.id DESC LIMIT $2`, before, limit)
	if err != nil {
		return nil, fmt.Errorf("broadcasts: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Broadcast, error) { return scanBroadcast(row, true) })
	if err != nil {
		return nil, fmt.Errorf("broadcasts: %w", err)
	}
	return out, nil
}

// Sending returns up to ten broadcasts still sending whose next round is
// due at now, oldest first.
func (s *Store) Sending(ctx context.Context, now time.Time) ([]domain.Broadcast, error) {
	rows, err := s.db.Query(ctx, `SELECT `+broadcastColumns+` FROM broadcasts b
		WHERE b.status = 'SENDING' AND (b.retry_at IS NULL OR b.retry_at <= $1) ORDER BY b.created_at LIMIT 10`, now)
	if err != nil {
		return nil, fmt.Errorf("sending broadcasts: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Broadcast, error) { return scanBroadcast(row, false) })
	if err != nil {
		return nil, fmt.Errorf("sending broadcasts: %w", err)
	}
	return out, nil
}

// Advance records a round of a broadcast.
func (s *Store) Advance(ctx context.Context, id, cursor string, recipients int, done bool, at time.Time) error {
	status, finished := domain.BroadcastSending, (*time.Time)(nil)
	if done {
		status, finished = domain.BroadcastSent, &at
	}
	_, err := s.db.Exec(ctx, `UPDATE broadcasts SET cursor = $2, recipients = $3, status = $4, finished_at = $5, failures = 0, last_error = '',
		retry_at = NULL WHERE id = $1`, id, cursor, recipients, status, finished)
	if err != nil {
		return fmt.Errorf("advance broadcast: %w", err)
	}
	return nil
}

// FailRound records a failed round of a broadcast still sending.
func (s *Store) FailRound(ctx context.Context, id string, failures int, lastError string, retryAt time.Time, failed bool) error {
	_, err := s.db.Exec(ctx, `UPDATE broadcasts SET failures = $2, last_error = $3, retry_at = $4,
		status = CASE WHEN $5 THEN 'FAILED' ELSE status END WHERE id = $1 AND status = 'SENDING'`, id, failures, lastError, retryAt, failed)
	if err != nil {
		return fmt.Errorf("fail broadcast round: %w", err)
	}
	return nil
}

// Resume sends a FAILED broadcast again from its cursor.
func (s *Store) Resume(ctx context.Context, id string) (bool, error) {
	if _, err := uuid.Parse(id); err != nil {
		return false, nil
	}
	tag, err := s.db.Exec(ctx, `UPDATE broadcasts SET status = 'SENDING', failures = 0, last_error = '', retry_at = NULL
		WHERE id = $1 AND status = 'FAILED'`, id)
	if err != nil {
		return false, fmt.Errorf("resume broadcast: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
