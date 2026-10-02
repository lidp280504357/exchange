package domain

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"strings"
	"testing"
)

func TestSanitizeSVGKeepsShapesAndDropsWhatCouldRun(t *testing.T) {
	in := `<?xml version="1.0"?>
<!DOCTYPE svg [<!ENTITY x "boom">]>
<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 64 64" onload="alert(1)">
  <script>alert(2)</script>
  <style>circle { fill: url(https://evil.example/x.svg) }</style>
  <defs><linearGradient id="g"><stop offset="0" stop-color="#f60"/></linearGradient></defs>
  <circle cx="32" cy="32" r="30" fill="url(#g)" onclick="alert(3)" style="fill:red"/>
  <path d="M10 10 L54 54" stroke="url(https://evil.example/p)"/>
  <use xlink:href="#g"/><use href="https://evil.example/s.svg#a"/>
  <foreignObject><div xmlns="http://www.w3.org/1999/xhtml">hi</div></foreignObject>
  <a href="javascript:alert(4)"><text x="1" y="9">A</text></a>
  <text x="2" y="20">Astra</text>
</svg>`
	out, w, h, err := SanitizeSVG([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if w != 64 || h != 64 {
		t.Fatalf("size %dx%d", w, h)
	}
	for _, gone := range []string{"script", "alert", "style", "evil.example", "foreignObject", "onload", "onclick", "javascript", "ENTITY", "<a"} {
		if strings.Contains(s, gone) {
			t.Errorf("%q survived: %s", gone, s)
		}
	}
	for _, kept := range []string{`<circle cx="32" cy="32" r="30" fill="url(#g)">`, `<stop offset="0" stop-color="#f60">`, `href="#g"`, `<text x="2" y="20">Astra</text>`, `xmlns="http://www.w3.org/2000/svg"`} {
		if !strings.Contains(s, kept) {
			t.Errorf("%q lost: %s", kept, s)
		}
	}
}

func TestSanitizeSVGRefusesWhatIsNotAnSVG(t *testing.T) {
	for name, in := range map[string]string{
		"not XML":         "<svg><circle></svg>",
		"another root":    `<html xmlns="http://www.w3.org/1999/xhtml"></html>`,
		"nothing":         "   ",
		"nested too deep": "<svg>" + strings.Repeat("<g>", 40) + strings.Repeat("</g>", 40) + "</svg>",
	} {
		if _, _, _, err := SanitizeSVG([]byte(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestLogosAreSquareSmallImages(t *testing.T) {
	if _, err := PrepareLogo(Logo{Data: pngOf(t, 64, 64), MIME: LogoPNG}); err != nil {
		t.Fatalf("a square PNG: %v", err)
	}
	// A lossless WebP header of 128x128: signature, then (w-1) | (h-1) << 14.
	webp := []byte("RIFF\x00\x00\x00\x00WEBPVP8L\x00\x00\x00\x00\x2f\x7f\xc0\x1f\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00")
	if w, h, err := webpSize(webp); err != nil || w != 128 || h != 128 {
		t.Fatalf("webp %dx%d %v", w, h, err)
	}
	for name, l := range map[string]Logo{
		"oblong PNG":     {Data: pngOf(t, 64, 32), MIME: LogoPNG},
		"PNG as WebP":    {Data: pngOf(t, 64, 64), MIME: LogoWebP},
		"GIF":            {Data: []byte("GIF89a"), MIME: "image/gif"},
		"empty":          {MIME: LogoPNG},
		"too big":        {Data: make([]byte, MaxLogoBytes+1), MIME: LogoPNG},
		"oblong SVG":     {Data: []byte(`<svg viewBox="0 0 64 32"/>`), MIME: LogoSVG},
		"SVG of no size": {Data: []byte(`<svg/>`), MIME: LogoSVG},
	} {
		if _, err := PrepareLogo(l); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	clean, err := PrepareLogo(Logo{Data: []byte(`<svg width="40" height="40"><script>x</script><rect width="40" height="40"/></svg>`), MIME: LogoSVG})
	if err != nil || strings.Contains(string(clean.Data), "script") {
		t.Fatalf("svg %s %v", clean.Data, err)
	}
}

func TestProfileText(t *testing.T) {
	ok := AssetProfile{
		Code: "ASTRA", DisplayName: "Astra 星辰", Description: map[string]string{"zh-CN": "平台币", "en": "The platform coin"},
		Links: map[string]string{"website": "https://astras.vip"},
	}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]AssetProfile{
		"one letter":       {DisplayName: "A"},
		"padded":           {DisplayName: " Astra"},
		"too long":         {DisplayName: strings.Repeat("A", 33)},
		"control":          {DisplayName: "As\ntra"},
		"other language":   {Description: map[string]string{"fr": "x"}},
		"long description": {Description: map[string]string{"en": strings.Repeat("x", 1001)}},
		"http link":        {Links: map[string]string{"website": "http://astras.vip"}},
		"script link":      {Links: map[string]string{"website": "javascript:alert(1)"}},
		"other link":       {Links: map[string]string{"twitter": "https://x.com/astras"}},
	} {
		if err := p.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// The default ASTRA logo (scripts/ops/astra.sh profile) passes the upload
// checks unchanged in substance.
func TestTheDefaultASTRALogo(t *testing.T) {
	raw, err := os.ReadFile("../../../deploy/instruments/astra.svg")
	if err != nil {
		t.Fatal(err)
	}
	l, err := PrepareLogo(Logo{Data: raw, MIME: LogoSVG})
	if err != nil {
		t.Fatal(err)
	}
	for _, kept := range []string{`<circle cx="32" cy="32" r="32" fill="url(#night)">`, `<stop offset="1" stop-color="#e3a90a">`, `fill="#ffe680"`} {
		if !strings.Contains(string(l.Data), kept) {
			t.Errorf("%q lost: %s", kept, l.Data)
		}
	}
}
