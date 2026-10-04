package domain

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/skill/exchange/internal/platform/apperr"
)

// Operations content (design 2026-10-02 §4.5): announcements and help
// articles in Chinese and English, drafted, published at once or from a
// time, archived; the sites read the published ones.

// Content sections: LEGAL holds the fixed legal and information pages and
// HOME the home page's blocks (design 2026-10-04 §4.4), each on its own
// slugs.
const (
	SectionAnnouncement = "ANNOUNCEMENT"
	SectionHelp         = "HELP"
	SectionLegal        = "LEGAL"
	SectionHome         = "HOME"
)

// fixedSlugs are the only slugs of the sections that have a fixed set;
// the sites bundle a draft of each.
var fixedSlugs = map[string][]string{
	SectionLegal: {"terms", "privacy", "risk", "fees", "about", "contact"},
	SectionHome:  {"home-hero"},
}

// FixedSlugs returns a section's fixed slugs, nil when any slug goes.
func FixedSlugs(section string) []string { return fixedSlugs[section] }

// Article statuses.
const (
	ArticleDraft     = "DRAFT"
	ArticlePublished = "PUBLISHED"
	ArticleArchived  = "ARCHIVED"
)

// Content locales: Chinese is required, English optional (the sites fall
// back to Chinese).
const (
	LocaleZH = "zh-CN"
	LocaleEN = "en"
)

var (
	slugRE     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	categoryRE = regexp.MustCompile(`^[a-z0-9_-]{0,32}$`)
)

// ErrArticleExists refuses a second article with a section's slug.
var ErrArticleExists = apperr.New(apperr.KindConflict, "NOTIFY_ARTICLE_EXISTS", "an article with this slug exists in the section")

// ErrArticleWithdrawn is an article taken off the sites: unlike one never
// published, the sites do not fall back to their own file of the slug.
var ErrArticleWithdrawn = apperr.New(apperr.KindNotFound, "NOTIFY_ARTICLE_WITHDRAWN", "the article was taken off")

// ArticleText is an article in one language.
type ArticleText struct {
	Locale  string
	Title   string
	Summary string
	// Body is Markdown.
	Body string
}

// Article is an announcement or a help article.
type Article struct {
	ID       string
	Section  string
	Slug     string
	Category string
	Pinned   bool
	// Order sorts help articles within their category.
	Order  int
	Status string
	// PublishAt is when a published article shows; later than now it is
	// scheduled.
	PublishAt time.Time
	Version   int
	UpdatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
	Texts     []ArticleText
}

// Text returns the article in locale, else in Chinese; ok is false when
// it fell back.
func (a Article) Text(locale string) (ArticleText, bool) {
	var zh ArticleText
	for _, t := range a.Texts {
		if t.Locale == locale {
			return t, true
		}
		if t.Locale == LocaleZH {
			zh = t
		}
	}
	return zh, false
}

// The Markdown a summary drops: the markers of a block that is not a
// paragraph, links and images (their text stays) and emphasis.
var (
	notParagraph = regexp.MustCompile("^(#|>|\\||```|~~~|[-*+] |\\d+[.)] |(-{3,}|\\*{3,}|_{3,})$)")
	mdLink       = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	mdEmphasis   = regexp.MustCompile("\\*\\*|__|~~|`|\\*")
	spaces       = regexp.MustCompile(`\s+`)
)

// excerptMax bounds a summary taken from the body, as the sites do.
const excerptMax = 140

// Excerpt is the first paragraph of a Markdown body as plain text, at most
// 140 characters: an article's summary when none was written (the sites
// take the same from a body they have).
func Excerpt(body string) string {
	for _, block := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" || notParagraph.MatchString(block) {
			continue
		}
		text := strings.TrimSpace(spaces.ReplaceAllString(mdEmphasis.ReplaceAllString(mdLink.ReplaceAllString(block, "$1"), ""), " "))
		if text == "" {
			continue
		}
		if r := []rune(text); len(r) > excerptMax {
			text = strings.TrimSpace(string(r[:excerptMax-1])) + "…"
		}
		return text
	}
	return ""
}

// Visible reports whether the sites show the article at now.
func (a Article) Visible(now time.Time) bool {
	return a.Status == ArticlePublished && !a.PublishAt.After(now)
}

// ValidSection reports whether s is a content section.
func ValidSection(s string) bool {
	return s == SectionAnnouncement || s == SectionHelp || s == SectionLegal || s == SectionHome
}

// validSlug reports whether a section takes slug: any for announcements
// and help, one of the fixed ones for the others.
func validSlug(section, slug string) bool {
	fixed, ok := fixedSlugs[section]
	return !ok || slices.Contains(fixed, slug)
}

// Validate checks an article as written: its slug and category, Chinese
// text, at most one text per locale, the lengths.
func (a Article) Validate() error {
	switch {
	case !ValidSection(a.Section):
		return apperr.Invalid("section must be ANNOUNCEMENT, HELP, LEGAL or HOME")
	case !slugRE.MatchString(a.Slug):
		return apperr.Invalid("slug must be 1-64 lower-case letters, digits and dashes, starting with a letter or digit")
	case !validSlug(a.Section, a.Slug):
		return apperr.Invalid(fmt.Sprintf("a %s slug is one of %s", a.Section, strings.Join(fixedSlugs[a.Section], ", ")))
	case !categoryRE.MatchString(a.Category):
		return apperr.Invalid("category must be at most 32 lower-case letters, digits, dashes and underscores")
	case a.Order < -1000 || a.Order > 1000:
		return apperr.Invalid("sort_order must be -1000 to 1000")
	}
	seen := map[string]bool{}
	for _, t := range a.Texts {
		switch {
		case t.Locale != LocaleZH && t.Locale != LocaleEN:
			return apperr.Invalid("locale must be zh-CN or en")
		case seen[t.Locale]:
			return apperr.Invalid("one text per locale")
		case strings.TrimSpace(t.Title) == "" || utf8.RuneCountInString(t.Title) > 200:
			return apperr.Invalid("a title of 1 to 200 characters is required")
		case utf8.RuneCountInString(t.Summary) > 500:
			return apperr.Invalid("a summary has at most 500 characters")
		case strings.TrimSpace(t.Body) == "" || len(t.Body) > 100_000:
			return apperr.Invalid("a body of at most 100,000 bytes is required")
		}
		seen[t.Locale] = true
	}
	if !seen[LocaleZH] {
		return apperr.Invalid("the Chinese (zh-CN) text is required")
	}
	return nil
}

// Broadcast audiences.
const (
	AudienceAll   = "ALL"
	AudienceUsers = "USERS"
)

// Broadcast statuses.
const (
	BroadcastSending = "SENDING"
	BroadcastSent    = "SENT"
	// BroadcastFailed is a broadcast whose rounds failed MaxRoundFailures
	// times in a row: it waits for an operator to resume it (C5.5 ⑫).
	BroadcastFailed = "FAILED"
)

// MaxRoundFailures is how many rounds of a broadcast fail in a row before
// it is FAILED: with RoundBackoff, about half an hour.
const MaxRoundFailures = 10

// RoundBackoff is how long a broadcast waits after its n-th failed round
// in a row: 3 seconds, doubling, at most 10 minutes. The other broadcasts
// go on meanwhile.
func RoundBackoff(n int) time.Duration {
	return min(3*time.Second<<min(max(n-1, 0), 8), 10*time.Minute)
}

// NoticeBroadcast is the type of an operator's in-app message.
const NoticeBroadcast = "BROADCAST"

// MaxBroadcastUsers bounds a message to named users.
const MaxBroadcastUsers = 10_000

// Broadcast is an in-app message an operator sends to some users or all.
type Broadcast struct {
	ID       string
	Audience string
	UserIDs  []string
	// Title and Body by locale (zh-CN required); each user reads theirs.
	Title map[string]string
	Body  map[string]string
	// Link is a site path the message leads to ("" for none).
	Link  string
	Email bool
	// Status is SENDING until every recipient has the message; Cursor is
	// where the sender is.
	Status     string
	Cursor     string
	Recipients int
	// Failures counts the rounds failed in a row, the last one's error in
	// LastError; RetryAt is when the next round may run (zero: now).
	Failures  int
	LastError string
	RetryAt   time.Time
	// Read counts the recipients who read it (computed).
	Read       int
	CreatedBy  string
	CreatedAt  time.Time
	FinishedAt time.Time
}

// linkRE is a path on the sites: "/" and a segment, never "//" (another
// host).
var linkRE = regexp.MustCompile(`^/([A-Za-z0-9_\-.][A-Za-z0-9/_\-?=&.%]{0,199})?$`)

// Validate checks a broadcast as written.
func (b Broadcast) Validate() error {
	switch {
	case b.Audience != AudienceAll && b.Audience != AudienceUsers:
		return apperr.Invalid("audience must be ALL or USERS")
	case b.Audience == AudienceUsers && (len(b.UserIDs) == 0 || len(b.UserIDs) > MaxBroadcastUsers):
		return apperr.Invalid("1 to 10,000 users are required")
	case b.Link != "" && !linkRE.MatchString(b.Link):
		return apperr.Invalid("link must be a path on the sites, such as /assets")
	}
	for _, l := range []string{LocaleZH, LocaleEN} {
		title, body := strings.TrimSpace(b.Title[l]), strings.TrimSpace(b.Body[l])
		if l == LocaleZH && (title == "" || body == "") {
			return apperr.Invalid("the Chinese (zh-CN) title and body are required")
		}
		if utf8.RuneCountInString(title) > 100 || utf8.RuneCountInString(body) > 2000 {
			return apperr.Invalid("a title has at most 100 characters, a body 2,000")
		}
	}
	for _, texts := range []map[string]string{b.Title, b.Body} {
		for l := range texts {
			if l != LocaleZH && l != LocaleEN {
				return apperr.Invalid("locale must be zh-CN or en")
			}
		}
	}
	return nil
}

// In returns the message in a user's language, Chinese without English.
func (b Broadcast) In(language string) (title, body string) {
	if strings.HasPrefix(language, "en") && strings.TrimSpace(b.Title[LocaleEN]) != "" {
		return b.Title[LocaleEN], b.Body[LocaleEN]
	}
	return b.Title[LocaleZH], b.Body[LocaleZH]
}
