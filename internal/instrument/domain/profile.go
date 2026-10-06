package domain

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/skill/exchange/internal/platform/apperr"
)

// AssetProfile is what the shop front shows of an asset beyond its code
// (ASTRA design §5.3): a display name, an introduction per language, links
// and a logo. Operators change it (the asset code never changes); every
// change bumps Version, which logo URLs carry, so a new logo is never
// served from an old cache.
type AssetProfile struct {
	Code string `json:"asset_code"`
	// DisplayName replaces the asset's name on the sites when set.
	DisplayName string `json:"display_name"`
	// Description is the introduction by language ("zh-CN", "zh-TW", "en").
	Description map[string]string `json:"description"`
	// Links are https URLs by kind ("website", "explorer", "whitepaper").
	Links map[string]string `json:"links"`
	// LogoMIME is the logo's type, empty without a logo; LogoSize its bytes.
	LogoMIME string `json:"logo_mime"`
	LogoSize int    `json:"logo_size"`
	Version  int64  `json:"profile_version"`
}

// Logo is an asset's icon as uploaded.
type Logo struct {
	Data []byte
	MIME string
}

// Profile limits (§5.3).
const (
	MaxLogoBytes      = 200 << 10
	minDisplayNameLen = 2
	maxDisplayNameLen = 32
	maxDescriptionLen = 1000
	maxLinkLen        = 300
	maxSVGDepth       = 32
	maxSVGElements    = 5000
)

// Logo types.
const (
	LogoPNG  = "image/png"
	LogoSVG  = "image/svg+xml"
	LogoWebP = "image/webp"
)

// languages and linkKinds are the keys a profile's maps may use.
var (
	languages = map[string]bool{LocaleZH: true, LocaleTW: true, LocaleEN: true}
	linkKinds = map[string]bool{"website": true, "explorer": true, "whitepaper": true}
)

// Validate checks the profile's text: the display name (2-32 printable
// characters, or none), the introductions (zh-CN, zh-TW and en, up to
// 1,000 characters) and the links (website, explorer, whitepaper: https URLs).
func (p AssetProfile) Validate() error {
	if n := utf8.RuneCountInString(p.DisplayName); p.DisplayName != "" &&
		(n < minDisplayNameLen || n > maxDisplayNameLen || strings.TrimSpace(p.DisplayName) != p.DisplayName || !printable(p.DisplayName)) {
		return apperr.Invalid("display_name: 2-32 printable characters without leading or trailing spaces")
	}
	for lang, text := range p.Description {
		if !languages[lang] {
			return apperr.Invalid(fmt.Sprintf("description: language %q is not zh-CN, zh-TW or en", lang))
		}
		if utf8.RuneCountInString(text) > maxDescriptionLen || !utf8.ValidString(text) {
			return apperr.Invalid(fmt.Sprintf("description %s: at most %d characters", lang, maxDescriptionLen))
		}
	}
	for kind, link := range p.Links {
		if !linkKinds[kind] {
			return apperr.Invalid(fmt.Sprintf("links: kind %q is not website, explorer or whitepaper", kind))
		}
		u, err := url.Parse(link)
		if err != nil || u.Scheme != "https" || u.Host == "" || len(link) > maxLinkLen {
			return apperr.Invalid(fmt.Sprintf("links %s: an https URL of at most %d characters", kind, maxLinkLen))
		}
	}
	return nil
}

// ValidProfileReason checks the reason an operator gives for a change.
func ValidProfileReason(reason string) error {
	if !reasonRE.MatchString(reason) {
		return apperr.Invalid("a profile change needs a reason of 3-200 characters")
	}
	return nil
}

func printable(s string) bool {
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// PrepareLogo checks an uploaded logo and returns what to store: PNG, SVG
// or WebP of at most 200 KB, square; an SVG is rebuilt from an allow list
// of elements and attributes, so no script, event handler, external
// reference or stylesheet survives.
func PrepareLogo(l Logo) (Logo, error) {
	out, _, err := prepareSquare(l)
	return out, err
}

// prepareSquare is PrepareLogo, which also returns the image's width (an
// SVG's in its own units).
func prepareSquare(l Logo) (Logo, int, error) {
	if len(l.Data) == 0 {
		return Logo{}, 0, apperr.Invalid("logo: empty")
	}
	if len(l.Data) > MaxLogoBytes {
		return Logo{}, 0, apperr.Invalid(fmt.Sprintf("logo: at most %d KB", MaxLogoBytes>>10))
	}
	var w, h int
	switch l.MIME {
	case LogoPNG:
		cfg, err := png.DecodeConfig(bytes.NewReader(l.Data))
		if err != nil {
			return Logo{}, 0, apperr.Invalid("logo: not a PNG image")
		}
		w, h = cfg.Width, cfg.Height
	case LogoWebP:
		var err error
		if w, h, err = webpSize(l.Data); err != nil {
			return Logo{}, 0, apperr.Invalid("logo: not a WebP image")
		}
	case LogoSVG:
		clean, sw, sh, err := SanitizeSVG(l.Data)
		if err != nil {
			return Logo{}, 0, apperr.Invalid("logo: " + err.Error())
		}
		l.Data, w, h = clean, sw, sh
	default:
		return Logo{}, 0, apperr.Invalid("logo: PNG, SVG or WebP")
	}
	if w <= 0 || w != h {
		return Logo{}, 0, apperr.Invalid(fmt.Sprintf("logo: must be square, it is %dx%d", w, h))
	}
	return l, w, nil
}

// webpSize reads a WebP image's size from its first chunk (lossy VP8,
// lossless VP8L or the extended VP8X header).
func webpSize(b []byte) (int, int, error) {
	bad := errors.New("not WebP")
	if len(b) < 30 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return 0, 0, bad
	}
	chunk, data := string(b[12:16]), b[20:]
	switch chunk {
	case "VP8 ":
		// Frame tag (3), start code 9d 01 2a, then 14-bit width and height.
		if len(data) < 10 || data[3] != 0x9d || data[4] != 0x01 || data[5] != 0x2a {
			return 0, 0, bad
		}
		return int(binary.LittleEndian.Uint16(data[6:8]) & 0x3fff), int(binary.LittleEndian.Uint16(data[8:10]) & 0x3fff), nil
	case "VP8L":
		// Signature 0x2f, then 14 bits of width-1 and 14 of height-1.
		if len(data) < 5 || data[0] != 0x2f {
			return 0, 0, bad
		}
		v := binary.LittleEndian.Uint32(data[1:5])
		return int(v&0x3fff) + 1, int(v>>14&0x3fff) + 1, nil
	case "VP8X":
		// Flags (4), then 24-bit canvas width-1 and height-1.
		if len(data) < 10 {
			return 0, 0, bad
		}
		u24 := func(p []byte) int { return int(p[0]) | int(p[1])<<8 | int(p[2])<<16 }
		return u24(data[4:7]) + 1, u24(data[7:10]) + 1, nil
	}
	return 0, 0, bad
}

// svgElements are the elements a logo may use: shapes, groups, gradients,
// clipping and masks, and text; everything else goes with its content.
var svgElements = map[string]bool{
	"svg": true, "g": true, "defs": true, "title": true, "desc": true, "path": true, "circle": true, "ellipse": true,
	"rect": true, "line": true, "polyline": true, "polygon": true, "linearGradient": true, "radialGradient": true,
	"stop": true, "clipPath": true, "mask": true, "use": true, "symbol": true, "text": true, "tspan": true,
}

// svgAttributes are the attributes kept (presentation and geometry); href
// only within the document ("#id"), url() paint only to "#id".
var svgAttributes = map[string]bool{
	"xmlns": true, "version": true, "viewBox": true, "width": true, "height": true, "preserveAspectRatio": true,
	"id": true, "class": true, "d": true, "x": true, "y": true, "x1": true, "y1": true, "x2": true, "y2": true,
	"cx": true, "cy": true, "r": true, "rx": true, "ry": true, "fx": true, "fy": true, "points": true, "transform": true,
	"fill": true, "fill-opacity": true, "fill-rule": true, "stroke": true, "stroke-width": true, "stroke-opacity": true,
	"stroke-linecap": true, "stroke-linejoin": true, "stroke-miterlimit": true, "stroke-dasharray": true,
	"stroke-dashoffset": true, "opacity": true, "clip-path": true, "clip-rule": true, "mask": true, "offset": true,
	"stop-color": true, "stop-opacity": true, "gradientUnits": true, "gradientTransform": true, "spreadMethod": true,
	"clipPathUnits": true, "maskUnits": true, "maskContentUnits": true, "href": true, "font-family": true,
	"font-size": true, "font-weight": true, "text-anchor": true, "dominant-baseline": true, "letter-spacing": true,
	"visibility": true, "display": true,
}

// SanitizeSVG rebuilds an SVG document from the allow lists and returns it
// with its size (width and height, or the viewBox's).
func SanitizeSVG(in []byte) ([]byte, int, int, error) {
	dec := xml.NewDecoder(bytes.NewReader(in))
	dec.Strict = true
	var out bytes.Buffer
	enc := xml.NewEncoder(&out)
	depth, skip, elements := 0, 0, 0
	var w, h float64
	root := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, 0, 0, errors.New("not well-formed XML")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth > maxSVGDepth {
				return nil, 0, 0, errors.New("nested too deep")
			}
			if elements++; elements > maxSVGElements {
				return nil, 0, 0, errors.New("too many elements")
			}
			if skip > 0 || !svgElements[t.Name.Local] || (t.Name.Space != "" && t.Name.Space != "http://www.w3.org/2000/svg") {
				skip++
				continue
			}
			if !root {
				if t.Name.Local != "svg" {
					return nil, 0, 0, errors.New("the root element is not svg")
				}
				root = true
				w, h = svgSize(t.Attr)
			}
			out := xml.StartElement{Name: xml.Name{Local: t.Name.Local}}
			for _, a := range t.Attr {
				if keep, ok := svgAttr(a); ok {
					out.Attr = append(out.Attr, keep)
				}
			}
			if t.Name.Local == "svg" && depth == 1 {
				out.Attr = append(out.Attr, xml.Attr{Name: xml.Name{Local: "xmlns"}, Value: "http://www.w3.org/2000/svg"})
			}
			if err := enc.EncodeToken(out); err != nil {
				return nil, 0, 0, err
			}
		case xml.EndElement:
			depth--
			if skip > 0 {
				skip--
				continue
			}
			if err := enc.EncodeToken(xml.EndElement{Name: xml.Name{Local: t.Name.Local}}); err != nil {
				return nil, 0, 0, err
			}
		case xml.CharData:
			if skip == 0 && depth > 0 {
				if err := enc.EncodeToken(t.Copy()); err != nil {
					return nil, 0, 0, err
				}
			}
		}
		// Comments, processing instructions and directives (DOCTYPE,
		// entities) are dropped.
	}
	if err := enc.Flush(); err != nil {
		return nil, 0, 0, err
	}
	if !root {
		return nil, 0, 0, errors.New("no svg element")
	}
	return out.Bytes(), int(w), int(h), nil
}

// svgAttr keeps an allowed attribute without a namespace (xlink:href is
// read as href) whose value cannot reach outside the document.
func svgAttr(a xml.Attr) (xml.Attr, bool) {
	name := a.Name.Local
	if a.Name.Space != "" && (a.Name.Space != "http://www.w3.org/1999/xlink" || name != "href") {
		return xml.Attr{}, false
	}
	if name == "xmlns" || !svgAttributes[name] {
		return xml.Attr{}, false
	}
	v := strings.TrimSpace(a.Value)
	lower := strings.ToLower(v)
	switch {
	case name == "href" && !strings.HasPrefix(v, "#"):
		return xml.Attr{}, false
	case strings.Contains(lower, "url(") && !strings.HasPrefix(strings.ReplaceAll(lower, " ", ""), "url(#"):
		return xml.Attr{}, false
	case strings.Contains(lower, "javascript:") || strings.Contains(lower, "expression("):
		return xml.Attr{}, false
	}
	return xml.Attr{Name: xml.Name{Local: name}, Value: v}, true
}

// svgSize reads the root's width and height (in user units or px), else
// its viewBox's.
func svgSize(attrs []xml.Attr) (float64, float64) {
	var w, h float64
	var box []string
	for _, a := range attrs {
		switch a.Name.Local {
		case "width":
			w = length(a.Value)
		case "height":
			h = length(a.Value)
		case "viewBox":
			box = strings.Fields(strings.ReplaceAll(a.Value, ",", " "))
		}
	}
	if (w <= 0 || h <= 0) && len(box) == 4 {
		w, h = length(box[2]), length(box[3])
	}
	return w, h
}

func length(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(s), "px"), 64)
	if err != nil {
		return 0
	}
	return v
}
