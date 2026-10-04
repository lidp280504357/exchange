package domain

import (
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
)

// PlatformProfile is the exchange's own profile (design 2026-10-04 §4.1):
// its name, images, colors, footer, contact, the learning-mode banner
// and whether sign-ups are open. Operators change it in the admin
// console; the sites read it at run time, so going live is a change of
// settings, not a build. Version goes up with every change, images
// included.
type PlatformProfile struct {
	Name      string
	ShortName string
	// Domain is the PC site's host name, empty until set.
	Domain        string
	ThemeColor    string
	BrandColor    string
	Footer        PlatformFooter
	Contact       PlatformContact
	Social        []SocialLink
	DefaultLocale string
	Learning      LearningMode
	Registration  Registration
	// Images are the uploaded images by kind (their bytes kept apart).
	Images    map[string]PlatformImage
	Version   int64
	UpdatedBy string
	UpdatedAt time.Time
}

// Texts is a text by language: zh-CN and en.
type Texts map[string]string

// Credit is an asset and an amount: what a new account gets, as the
// ledger grants it (design 2026-10-04 §4.2).
type Credit struct {
	Asset  string
	Amount decimal.Decimal
}

// PlatformFooter is the sites' footer.
type PlatformFooter struct {
	Copyright Texts
	// Compliance is the registration or compliance lines, empty for none.
	Compliance Texts
}

// PlatformContact is how users reach the exchange.
type PlatformContact struct {
	Email      string
	SupportURL string
}

// SocialLink is the exchange's page on a network.
type SocialLink struct {
	Kind string
	URL  string
}

// LearningMode is the banner the sites show while the exchange is a
// learning or simulated environment (off when live).
type LearningMode struct {
	Enabled bool
	Text    Texts
}

// Registration says whether sign-ups are open, and what the sign-up pages
// say while they are not.
type Registration struct {
	Status     string
	ClosedText Texts
}

// Registration statuses.
const (
	RegistrationOpen   = "OPEN"
	RegistrationClosed = "CLOSED"
)

// PlatformImage is an uploaded image's type, size and width (an SVG's in
// its own units).
type PlatformImage struct {
	MIME  string
	Size  int
	Width int
}

// The platform's image kinds.
const (
	ImageLogoLight      = "logo_light"
	ImageLogoDark       = "logo_dark"
	ImageFavicon        = "favicon"
	ImageAppleTouchIcon = "apple_touch_icon"
)

// ImageKinds are the platform's images, in the order the API lists them.
var ImageKinds = []string{ImageLogoLight, ImageLogoDark, ImageFavicon, ImageAppleTouchIcon}

// ValidImageKind reports whether kind is one of the platform's images.
func ValidImageKind(kind string) bool {
	for _, k := range ImageKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// minAppleTouchIcon is the size iOS shows a home-screen icon at.
const minAppleTouchIcon = 180

// SocialKinds are the networks a social link may name.
var SocialKinds = []string{"x", "telegram", "discord", "youtube", "facebook", "instagram", "linkedin", "reddit", "medium", "github", "tiktok", "weibo"}

// Profile limits.
const (
	maxPlatformName  = 32
	maxShortName     = 12
	maxCopyright     = 200
	maxCompliance    = 500
	maxBannerText    = 300
	maxSocialLinks   = 10
	maxHostName      = 253
	maxEmail         = 254
	minPlatformNames = 2
)

var (
	colorRE = regexp.MustCompile(`^#[0-9a-f]{6}$`)
	labelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

// ErrPlatformChanged refuses a change made on a version that is no longer
// the current one: someone saved in between.
var ErrPlatformChanged = apperr.New(apperr.KindConflict, "INSTRUMENT_PLATFORM_CHANGED", "the platform profile changed since it was read")

// Normalize gives every text both languages and the lists their empty
// values, as the API shows them.
func (p *PlatformProfile) Normalize() {
	for _, t := range []*Texts{&p.Footer.Copyright, &p.Footer.Compliance, &p.Learning.Text, &p.Registration.ClosedText} {
		*t = t.normalized()
	}
	if p.Social == nil {
		p.Social = []SocialLink{}
	}
	if p.Images == nil {
		p.Images = map[string]PlatformImage{}
	}
}

func (t Texts) normalized() Texts { return Texts{LocaleZH: t[LocaleZH], LocaleEN: t[LocaleEN]} }

// Text returns the text in locale, else the Chinese one.
func (t Texts) Text(locale string) string {
	if s := t[locale]; s != "" {
		return s
	}
	return t[LocaleZH]
}

// Content locales.
const (
	LocaleZH = "zh-CN"
	LocaleEN = "en"
)

// Validate checks what an operator writes: the names, the domain, the
// colors, the texts, the contact and the links.
func (p PlatformProfile) Validate() error {
	if err := validName("name", p.Name, maxPlatformName); err != nil {
		return err
	}
	if err := validName("short_name", p.ShortName, maxShortName); err != nil {
		return err
	}
	if err := ValidHostName(p.Domain); err != nil {
		return err
	}
	if !colorRE.MatchString(p.ThemeColor) || !colorRE.MatchString(p.BrandColor) {
		return apperr.Invalid("theme_color and brand_color: #rrggbb in lower case")
	}
	for name, t := range map[string]struct {
		texts    Texts
		max      int
		required bool
	}{
		"footer.copyright":         {p.Footer.Copyright, maxCopyright, false},
		"footer.compliance":        {p.Footer.Compliance, maxCompliance, false},
		"learning_mode.text":       {p.Learning.Text, maxBannerText, p.Learning.Enabled},
		"registration.closed_text": {p.Registration.ClosedText, maxBannerText, p.Registration.Status == RegistrationClosed},
	} {
		if err := validTexts(name, t.texts, t.max, t.required); err != nil {
			return err
		}
	}
	if p.Contact.Email != "" {
		a, err := mail.ParseAddress(p.Contact.Email)
		if err != nil || a.Address != p.Contact.Email || a.Name != "" || len(p.Contact.Email) > maxEmail {
			return apperr.Invalid("contact.email: an e-mail address, or empty")
		}
	}
	if p.Contact.SupportURL != "" && !httpsURL(p.Contact.SupportURL) {
		return apperr.Invalid(fmt.Sprintf("contact.support_url: an https URL of at most %d characters", maxLinkLen))
	}
	if len(p.Social) > maxSocialLinks {
		return apperr.Invalid(fmt.Sprintf("social: at most %d links", maxSocialLinks))
	}
	seen := map[string]bool{}
	for _, s := range p.Social {
		switch {
		case !validSocialKind(s.Kind):
			return apperr.Invalid(fmt.Sprintf("social: %q is not one of %s", s.Kind, strings.Join(SocialKinds, ", ")))
		case seen[s.Kind]:
			return apperr.Invalid(fmt.Sprintf("social: %s twice", s.Kind))
		case !httpsURL(s.URL):
			return apperr.Invalid(fmt.Sprintf("social %s: an https URL of at most %d characters", s.Kind, maxLinkLen))
		}
		seen[s.Kind] = true
	}
	if p.DefaultLocale != LocaleZH && p.DefaultLocale != LocaleEN {
		return apperr.Invalid("default_locale: zh-CN or en")
	}
	if p.Registration.Status != RegistrationOpen && p.Registration.Status != RegistrationClosed {
		return apperr.Invalid("registration.status: OPEN or CLOSED")
	}
	return nil
}

func validName(field, s string, most int) error {
	if n := utf8.RuneCountInString(s); n < minPlatformNames || n > most || strings.TrimSpace(s) != s || !printable(s) {
		return apperr.Invalid(fmt.Sprintf("%s: %d-%d printable characters without leading or trailing spaces", field, minPlatformNames, most))
	}
	return nil
}

func validTexts(field string, t Texts, most int, required bool) error {
	for lang, s := range t {
		if !languages[lang] {
			return apperr.Invalid(fmt.Sprintf("%s: language %q is not zh-CN or en", field, lang))
		}
		if utf8.RuneCountInString(s) > most || !utf8.ValidString(s) {
			return apperr.Invalid(fmt.Sprintf("%s %s: at most %d characters", field, lang, most))
		}
	}
	if required && strings.TrimSpace(t[LocaleZH]) == "" {
		return apperr.Invalid(fmt.Sprintf("%s: the Chinese (zh-CN) text is required", field))
	}
	return nil
}

func validSocialKind(kind string) bool {
	for _, k := range SocialKinds {
		if k == kind {
			return true
		}
	}
	return false
}

func httpsURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != "" && len(s) <= maxLinkLen
}

// ValidHostName checks a host name such as astras.vip: lower case, two
// labels or more; empty is allowed (not set).
func ValidHostName(h string) error {
	if h == "" {
		return nil
	}
	labels := strings.Split(h, ".")
	ok := len(h) <= maxHostName && len(labels) >= 2
	for _, l := range labels {
		ok = ok && labelRE.MatchString(l)
	}
	if !ok {
		return apperr.Invalid("domain: a host name such as example.com in lower case, or empty")
	}
	return nil
}

// PreparePlatformImage checks an uploaded image for its kind and returns
// it as stored with its width: square, at most 200 KB, an SVG rebuilt from
// the allow list (PrepareLogo); a favicon PNG or SVG; an apple-touch-icon
// PNG of 180 px or more.
func PreparePlatformImage(kind string, l Logo) (Logo, PlatformImage, error) {
	if !ValidImageKind(kind) {
		return Logo{}, PlatformImage{}, apperr.NotFound("no such image")
	}
	out, width, err := prepareSquare(l)
	if err != nil {
		return Logo{}, PlatformImage{}, err
	}
	switch {
	case kind == ImageFavicon && out.MIME == LogoWebP:
		return Logo{}, PlatformImage{}, apperr.Invalid("favicon: PNG or SVG")
	case kind == ImageAppleTouchIcon && out.MIME != LogoPNG:
		return Logo{}, PlatformImage{}, apperr.Invalid("apple_touch_icon: PNG")
	case kind == ImageAppleTouchIcon && width < minAppleTouchIcon:
		return Logo{}, PlatformImage{}, apperr.Invalid(fmt.Sprintf("apple_touch_icon: at least %d px, it is %d", minAppleTouchIcon, width))
	}
	return out, PlatformImage{MIME: out.MIME, Size: len(out.Data), Width: width}, nil
}
