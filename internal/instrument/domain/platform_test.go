package domain

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/skill/exchange/internal/platform/apperr"
)

// defaultPlatform is the profile the migration seeds.
func defaultPlatform() PlatformProfile {
	p := PlatformProfile{
		Name: "Astras", ShortName: "Astras", ThemeColor: "#0b0e11", BrandColor: "#f0b90b", DefaultLocale: LocaleZH,
		Footer:       PlatformFooter{Copyright: Texts{LocaleZH: "© 2026 Astras", LocaleEN: "© 2026 Astras"}},
		Test:         TestMode{Enabled: true, Banner: true, Text: Texts{LocaleZH: "测试模式", LocaleEN: "Test mode"}},
		Registration: Registration{Status: RegistrationOpen, ClosedText: Texts{LocaleZH: "暂未开放注册"}},
	}
	p.Normalize()
	return p
}

func TestPlatformProfileValidate(t *testing.T) {
	if err := defaultPlatform().Validate(); err != nil {
		t.Fatalf("the default: %v", err)
	}
	live := defaultPlatform()
	live.Name, live.ShortName, live.Domain = "Example Exchange", "Example", "example.com"
	live.Contact = PlatformContact{Email: "support@example.com", SupportURL: "https://help.example.com"}
	live.Social = []SocialLink{{Kind: "x", URL: "https://x.com/example"}, {Kind: "telegram", URL: "https://t.me/example"}}
	live.Test = TestMode{Enabled: false}
	live.Registration = Registration{Status: RegistrationClosed, ClosedText: Texts{LocaleZH: "邀请制", LocaleEN: "By invitation"}}
	if err := live.Validate(); err != nil {
		t.Fatalf("a live profile: %v", err)
	}
	quiet := defaultPlatform()
	quiet.Test = TestMode{Enabled: true}
	if err := quiet.Validate(); err != nil {
		t.Fatalf("test mode with its banner hidden needs no text: %v", err)
	}

	bad := map[string]func(p *PlatformProfile){
		"a one-letter name":      func(p *PlatformProfile) { p.Name = "A" },
		"a long short name":      func(p *PlatformProfile) { p.ShortName = "Thirteen Char" },
		"a padded name":          func(p *PlatformProfile) { p.Name = " Astras" },
		"an upper-case domain":   func(p *PlatformProfile) { p.Domain = "Astras.vip" },
		"a domain with a scheme": func(p *PlatformProfile) { p.Domain = "https://astras.vip" },
		"a bare label":           func(p *PlatformProfile) { p.Domain = "localhost" },
		"an upper-case color":    func(p *PlatformProfile) { p.ThemeColor = "#0B0E11" },
		"a short color":          func(p *PlatformProfile) { p.BrandColor = "#fff" },
		"a long copyright":       func(p *PlatformProfile) { p.Footer.Copyright = Texts{LocaleZH: strings.Repeat("版", 201)} },
		"another language":       func(p *PlatformProfile) { p.Footer.Compliance = Texts{"fr": "x"} },
		"a banner without text": func(p *PlatformProfile) {
			p.Test = TestMode{Enabled: true, Banner: true, Text: Texts{LocaleEN: "en only"}}
		},
		"closed without text":      func(p *PlatformProfile) { p.Registration = Registration{Status: RegistrationClosed} },
		"an unknown registration":  func(p *PlatformProfile) { p.Registration.Status = "INVITE" },
		"a named e-mail":           func(p *PlatformProfile) { p.Contact.Email = "Support <support@example.com>" },
		"an http support URL":      func(p *PlatformProfile) { p.Contact.SupportURL = "http://help.example.com" },
		"an unknown network":       func(p *PlatformProfile) { p.Social = []SocialLink{{Kind: "myspace", URL: "https://myspace.com/x"}} },
		"a network twice":          func(p *PlatformProfile) { p.Social = []SocialLink{{"x", "https://x.com/a"}, {"x", "https://x.com/b"}} },
		"a social link over http":  func(p *PlatformProfile) { p.Social = []SocialLink{{Kind: "x", URL: "http://x.com/a"}} },
		"another default language": func(p *PlatformProfile) { p.DefaultLocale = "ja" },
	}
	for name, change := range bad {
		p := defaultPlatform()
		change(&p)
		if err := p.Validate(); apperr.From(err).Code != apperr.CodeInvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestNormalizeAndText(t *testing.T) {
	var p PlatformProfile
	p.Normalize()
	if p.Footer.Copyright == nil || len(p.Footer.Copyright) != 3 || p.Social == nil || p.Images == nil {
		t.Fatalf("normalized %+v", p)
	}
	tx := Texts{LocaleZH: "中文", LocaleEN: ""}
	if tx.Text(LocaleEN) != "中文" || tx.Text(LocaleZH) != "中文" || tx.Text(LocaleTW) != "中文" {
		t.Fatalf("fallback %q", tx.Text(LocaleEN))
	}
	// Traditional Chinese (design 2026-10-06 繁体中文 §2.1): kept, empty
	// until written, falling back to the Simplified.
	tw := Texts{LocaleZH: "测试模式", LocaleTW: "測試模式", "fr": "x"}.normalized()
	if len(tw) != 3 || tw.Text(LocaleTW) != "測試模式" || tw[LocaleEN] != "" {
		t.Fatalf("normalized %v", tw)
	}
	q := defaultPlatform()
	q.DefaultLocale = LocaleTW
	q.Test.Text = Texts{LocaleZH: "测试模式", LocaleTW: "測試模式", LocaleEN: "Test mode"}
	if err := q.Validate(); err != nil {
		t.Fatalf("a Traditional default and texts: %v", err)
	}
	if err := (AssetProfile{Description: map[string]string{LocaleTW: "比特幣"}}).Validate(); err != nil {
		t.Fatalf("a Traditional introduction: %v", err)
	}
}

func squarePNG(t *testing.T, size int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, size, size))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestPreparePlatformImage(t *testing.T) {
	svg := []byte(`<svg viewBox="0 0 24 24"><script>alert(1)</script><path d="M0 0h24v24H0z"/></svg>`)
	img, info, err := PreparePlatformImage(ImageLogoDark, Logo{Data: svg, MIME: LogoSVG})
	if err != nil || bytes.Contains(img.Data, []byte("script")) || info.MIME != LogoSVG || info.Width != 24 || info.Size != len(img.Data) {
		t.Fatalf("an SVG logo: %s %+v %v", img.Data, info, err)
	}
	if _, info, err := PreparePlatformImage(ImageAppleTouchIcon, Logo{Data: squarePNG(t, 180), MIME: LogoPNG}); err != nil || info.Width != 180 {
		t.Fatalf("an apple-touch-icon: %+v %v", info, err)
	}
	if _, _, err := PreparePlatformImage(ImageFavicon, Logo{Data: squarePNG(t, 32), MIME: LogoPNG}); err != nil {
		t.Fatalf("a PNG favicon: %v", err)
	}
	for name, c := range map[string]struct {
		kind string
		img  Logo
	}{
		"an apple-touch-icon in SVG":  {ImageAppleTouchIcon, Logo{Data: svg, MIME: LogoSVG}},
		"a small apple-touch-icon":    {ImageAppleTouchIcon, Logo{Data: squarePNG(t, 120), MIME: LogoPNG}},
		"a WebP favicon":              {ImageFavicon, Logo{Data: []byte("RIFF\x00\x00\x00\x00WEBPVP8L"), MIME: LogoWebP}},
		"an oblong logo":              {ImageLogoLight, Logo{Data: []byte(`<svg viewBox="0 0 48 24"/>`), MIME: LogoSVG}},
		"a logo of an unknown format": {ImageLogoLight, Logo{Data: []byte("GIF89a"), MIME: "image/gif"}},
	} {
		if _, _, err := PreparePlatformImage(c.kind, c.img); apperr.From(err).Code != apperr.CodeInvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, _, err := PreparePlatformImage("banner", Logo{Data: svg, MIME: LogoSVG}); apperr.From(err).Code != apperr.CodeNotFound {
		t.Fatalf("an unknown kind: %v", err)
	}
}
