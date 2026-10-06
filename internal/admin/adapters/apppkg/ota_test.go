package apppkg

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestOTAManifest(t *testing.T) {
	m := OTAManifest("https://astras.vip/downloads/ios/0192a000-0000-7000-8000-000000000001.ipa?a=1&b=2",
		Info{Package: "vip.astras.app", Version: "1.2.0", Build: "42", Name: "Astras <beta>"}, "Astras")
	// A well-formed property list: items[0] with the package's address and
	// the app's identity, escaped.
	var doc struct {
		Items []struct {
			Strings []string `xml:"dict>array>dict>string"`
		} `xml:"dict>array>dict"`
	}
	if err := xml.Unmarshal(m, &doc); err != nil {
		t.Fatalf("not XML: %v\n%s", err, m)
	}
	s := string(m)
	for _, want := range []string{
		"<key>kind</key><string>software-package</string>",
		"<key>url</key><string>https://astras.vip/downloads/ios/0192a000-0000-7000-8000-000000000001.ipa?a=1&amp;b=2</string>",
		"<key>bundle-identifier</key><string>vip.astras.app</string>",
		"<key>bundle-version</key><string>1.2.0</string>",
		"<key>kind</key><string>software</string>",
		"<key>title</key><string>Astras &lt;beta&gt;</string>",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("no %s in\n%s", want, s)
		}
	}
	// Without a short version or a name: the build and the title given.
	m = OTAManifest("https://astras.vip/x.ipa", Info{Package: "a.b", Build: "7"}, "Astras")
	if s := string(m); !strings.Contains(s, "<key>bundle-version</key><string>7</string>") || !strings.Contains(s, "<key>title</key><string>Astras</string>") {
		t.Fatalf("fallbacks:\n%s", s)
	}
}
