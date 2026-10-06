package apppkg

import (
	"bytes"
	"encoding/xml"
)

// OTAManifest is the manifest.plist an iOS device reads to install an
// .ipa over the air (itms-services://?action=download-manifest&url=…): the
// package's HTTPS address, its bundle identifier, version (the short
// version, else the build) and title (its name, else title).
func OTAManifest(ipaURL string, info Info, title string) []byte {
	version := info.Version
	if version == "" {
		version = info.Build
	}
	if info.Name != "" {
		title = info.Name
	}
	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n\t<key>items</key>\n\t<array>\n\t\t<dict>\n")
	b.WriteString("\t\t\t<key>assets</key>\n\t\t\t<array>\n\t\t\t\t<dict>\n")
	plistEntry(&b, "\t\t\t\t\t", "kind", "software-package")
	plistEntry(&b, "\t\t\t\t\t", "url", ipaURL)
	b.WriteString("\t\t\t\t</dict>\n\t\t\t</array>\n\t\t\t<key>metadata</key>\n\t\t\t<dict>\n")
	plistEntry(&b, "\t\t\t\t", "bundle-identifier", info.Package)
	plistEntry(&b, "\t\t\t\t", "bundle-version", version)
	plistEntry(&b, "\t\t\t\t", "kind", "software")
	plistEntry(&b, "\t\t\t\t", "title", title)
	b.WriteString("\t\t\t</dict>\n\t\t</dict>\n\t</array>\n</dict>\n</plist>\n")
	return b.Bytes()
}

// plistEntry writes a <key> and its <string>, escaped.
func plistEntry(b *bytes.Buffer, indent, key, value string) {
	b.WriteString(indent + "<key>")
	_ = xml.EscapeText(b, []byte(key))
	b.WriteString("</key><string>")
	_ = xml.EscapeText(b, []byte(value))
	b.WriteString("</string>\n")
}
