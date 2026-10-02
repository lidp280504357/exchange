package application

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/lidp280504357/exchange/internal/notification/domain"
	"github.com/lidp280504357/exchange/internal/notification/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Content serves the announcements and help articles (design 2026-10-02
// §4.5): the sites read what is published, the admin console writes them
// (it audits each change; its administrator is the actor here).
type Content struct {
	Store ports.ContentStore
	Now   func() time.Time
}

// Published returns a section's articles the sites show now.
func (c *Content) Published(ctx context.Context, section string) ([]domain.Article, error) {
	if !domain.ValidSection(section) {
		return nil, apperr.NotFound("no such section")
	}
	return c.Store.Articles(ctx, section, c.Now())
}

// Withdrawn returns the slugs of a section's articles taken off: the
// sites hide their own file of such a slug too.
func (c *Content) Withdrawn(ctx context.Context, section string) ([]string, error) {
	if !domain.ValidSection(section) {
		return nil, apperr.NotFound("no such section")
	}
	return c.Store.Withdrawn(ctx, section)
}

// PublishedArticle returns one the sites show now; one taken off is
// domain.ErrArticleWithdrawn.
func (c *Content) PublishedArticle(ctx context.Context, section, slug string) (domain.Article, error) {
	a, err := c.Store.Article(ctx, section, strings.ToLower(slug))
	if err != nil {
		return domain.Article{}, err
	}
	if a != nil && a.Status == domain.ArticleArchived {
		return domain.Article{}, domain.ErrArticleWithdrawn
	}
	if a == nil || !a.Visible(c.Now()) {
		return domain.Article{}, apperr.NotFound("no such article")
	}
	return *a, nil
}

// All returns a section's articles in every status (the console).
func (c *Content) All(ctx context.Context, section string) ([]domain.Article, error) {
	if !domain.ValidSection(section) {
		return nil, apperr.Invalid("section must be ANNOUNCEMENT or HELP")
	}
	return c.Store.Articles(ctx, section, time.Time{})
}

// Get returns an article by its ID.
func (c *Content) Get(ctx context.Context, id string) (domain.Article, error) {
	a, err := c.Store.ArticleByID(ctx, id)
	if err != nil {
		return domain.Article{}, err
	}
	if a == nil {
		return domain.Article{}, apperr.NotFound("no such article")
	}
	return *a, nil
}

// ArticleInput is what the console writes of an article.
type ArticleInput struct {
	Section  string
	Slug     string
	Category string
	Pinned   bool
	Order    int
	Texts    []domain.ArticleText
}

// Create writes a draft.
func (c *Content) Create(ctx context.Context, in ArticleInput, actor string) (domain.Article, error) {
	now := c.Now()
	a := domain.Article{
		ID: uuid.Must(uuid.NewV7()).String(), Section: strings.ToUpper(in.Section), Slug: strings.ToLower(strings.TrimSpace(in.Slug)),
		Category: in.Category, Pinned: in.Pinned, Order: in.Order, Status: domain.ArticleDraft, Version: 1, UpdatedBy: actor,
		CreatedAt: now, UpdatedAt: now, Texts: in.Texts,
	}
	if err := a.Validate(); err != nil {
		return domain.Article{}, err
	}
	if err := c.Store.CreateArticle(ctx, a); err != nil {
		return domain.Article{}, err
	}
	return a, nil
}

// Update rewrites an article at version: its slug, category, pin, order
// and texts; its status stays.
func (c *Content) Update(ctx context.Context, id string, version int, in ArticleInput, actor string) (domain.Article, error) {
	a, err := c.Get(ctx, id)
	if err != nil {
		return domain.Article{}, err
	}
	a.Slug, a.Category, a.Pinned, a.Order, a.Texts = strings.ToLower(strings.TrimSpace(in.Slug)), in.Category, in.Pinned, in.Order, in.Texts
	return c.save(ctx, a, version, actor)
}

// Publish shows an article from at on (now when zero; a time ahead
// schedules it).
func (c *Content) Publish(ctx context.Context, id string, version int, at time.Time, actor string) (domain.Article, error) {
	a, err := c.Get(ctx, id)
	if err != nil {
		return domain.Article{}, err
	}
	if at.IsZero() || at.Before(c.Now()) {
		at = c.Now()
	}
	a.Status, a.PublishAt = domain.ArticlePublished, at.UTC()
	return c.save(ctx, a, version, actor)
}

// Archive takes an article off the sites (it may be published again).
func (c *Content) Archive(ctx context.Context, id string, version int, actor string) (domain.Article, error) {
	a, err := c.Get(ctx, id)
	if err != nil {
		return domain.Article{}, err
	}
	a.Status = domain.ArticleArchived
	return c.save(ctx, a, version, actor)
}

func (c *Content) save(ctx context.Context, a domain.Article, version int, actor string) (domain.Article, error) {
	if err := a.Validate(); err != nil {
		return domain.Article{}, err
	}
	a.Version, a.UpdatedBy, a.UpdatedAt = version+1, actor, c.Now()
	if err := c.Store.UpdateArticle(ctx, a, version); err != nil {
		return domain.Article{}, err
	}
	return a, nil
}

// Broadcasts sends the operators' in-app messages: recorded at once, then
// delivered in rounds to every recipient (each user's notice once, in
// their language), with how many arrived and were read.
type Broadcasts struct {
	Store     ports.BroadcastStore
	Notices   *Notices
	Directory ports.Directory
	Log       *slog.Logger
	Now       func() time.Time
	// Batch is how many recipients a round reaches (100 when zero).
	Batch int
}

// BroadcastInput is a message as the console sends it.
type BroadcastInput struct {
	Audience string
	UserIDs  []string
	Title    map[string]string
	Body     map[string]string
	Link     string
	Email    bool
}

// Send records a broadcast; Run delivers it.
func (b *Broadcasts) Send(ctx context.Context, in BroadcastInput, actor string) (domain.Broadcast, error) {
	seen := map[string]bool{}
	var users []string
	for _, id := range in.UserIDs {
		if _, err := uuid.Parse(id); err != nil {
			return domain.Broadcast{}, apperr.Invalid("user_ids must be UUIDs")
		}
		if !seen[id] {
			seen[id] = true
			users = append(users, id)
		}
	}
	br := domain.Broadcast{
		ID: uuid.Must(uuid.NewV7()).String(), Audience: strings.ToUpper(in.Audience), UserIDs: users, Title: in.Title, Body: in.Body,
		Link: strings.TrimSpace(in.Link), Email: in.Email, Status: domain.BroadcastSending, CreatedBy: actor, CreatedAt: b.Now(),
	}
	if br.Audience == domain.AudienceAll {
		br.UserIDs = nil
	}
	if err := br.Validate(); err != nil {
		return domain.Broadcast{}, err
	}
	if err := b.Store.CreateBroadcast(ctx, br); err != nil {
		return domain.Broadcast{}, err
	}
	return br, nil
}

// Get returns a broadcast with its counts.
func (b *Broadcasts) Get(ctx context.Context, id string) (domain.Broadcast, error) {
	br, err := b.Store.Broadcast(ctx, id)
	if err != nil {
		return domain.Broadcast{}, err
	}
	if br == nil {
		return domain.Broadcast{}, apperr.NotFound("no such broadcast")
	}
	return *br, nil
}

// List pages through the broadcasts, newest first.
func (b *Broadcasts) List(ctx context.Context, cursor string, limit int) ([]domain.Broadcast, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if cursor != "" {
		if _, err := uuid.Parse(cursor); err != nil {
			return nil, "", apperr.Invalid("invalid cursor")
		}
	}
	list, err := b.Store.Broadcasts(ctx, cursor, limit+1)
	if err != nil || len(list) <= limit {
		return list, "", err
	}
	list = list[:limit]
	return list, list[limit-1].ID, nil
}

// Round delivers the next batch of every broadcast still sending; it
// returns how many notices it created.
func (b *Broadcasts) Round(ctx context.Context) (int, error) {
	sending, err := b.Store.Sending(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, br := range sending {
		n, err := b.round(ctx, br)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func (b *Broadcasts) batch() int {
	if b.Batch <= 0 {
		return 100
	}
	return b.Batch
}

// round reaches the next recipients of br and records where it is.
func (b *Broadcasts) round(ctx context.Context, br domain.Broadcast) (int, error) {
	var users []string
	next, done := "", false
	switch br.Audience {
	case domain.AudienceUsers:
		from, _ := strconv.Atoi(br.Cursor)
		to := min(from+b.batch(), len(br.UserIDs))
		users, next, done = br.UserIDs[from:to], strconv.Itoa(to), to >= len(br.UserIDs)
	default:
		var err error
		if users, next, err = b.Directory.UserIDs(ctx, br.Cursor, b.batch()); err != nil {
			return 0, err
		}
		done = next == ""
	}
	created := 0
	for _, user := range users {
		ok, err := b.deliver(ctx, br, user)
		if err != nil {
			return created, err
		}
		if ok {
			created++
		}
	}
	return created, b.Store.Advance(ctx, br.ID, next, br.Recipients+created, done, b.Now())
}

// deliver gives one user the message once, in their language, and mails
// it when asked; an unknown user is skipped.
func (b *Broadcasts) deliver(ctx context.Context, br domain.Broadcast, userID string) (bool, error) {
	r, err := b.Notices.Recipients.Recipient(ctx, userID)
	if apperr.Is(err, apperr.CodeNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	title, body := br.In(r.Language)
	data := map[string]string{"broadcast_id": br.ID}
	if br.Link != "" {
		data["link"] = br.Link
	}
	notice := domain.Notice{
		ID: uuid.Must(uuid.NewV7()).String(), UserID: userID, Type: domain.NoticeBroadcast, Title: title, Body: body, Data: data,
		CreatedAt: b.Now(),
	}
	// The inbox entry is the broadcast's and the user's: a round run twice
	// gives nobody the message twice.
	key := uuid.NewSHA1(uuid.MustParse(br.ID), []byte(userID)).String()
	created, err := b.Notices.Store.CreateNotice(ctx, BroadcastConsumer, key, notice)
	if err != nil {
		return false, err
	}
	if created && br.Email {
		b.Notices.mail(ctx, notice, r)
	}
	return true, nil // reached, now or by a round that was not recorded
}

// BroadcastConsumer names the inbox entries that make each broadcast
// reach a user once.
const BroadcastConsumer = "notification-service.broadcast"
