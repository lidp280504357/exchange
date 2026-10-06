// Package apppkg reads what the download page shows of an uploaded app
// (design 2026-10-07, App download page §7 #6): an .apk's package,
// versionName, versionCode and minSdkVersion from its compiled
// AndroidManifest.xml; an .ipa's bundle identifier, versions, minimum iOS
// and name from Payload/<name>.app/Info.plist (binary or XML property
// list); whether a .mobileconfig is a configuration profile (signed or
// not). Nothing is executed or scanned (§1.3).
package apppkg

import (
	"archive/zip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// Info is what a package says of itself.
type Info struct {
	// Package is the Android package or the iOS bundle identifier.
	Package string
	// Version is versionName or CFBundleShortVersionString.
	Version string
	// Build is versionCode or CFBundleVersion.
	Build string
	// MinOS is minSdkVersion (an API level) or MinimumOSVersion; empty
	// when the package does not say.
	MinOS string
	// Name is an iOS app's CFBundleDisplayName, else its CFBundleName.
	Name string
}

// ErrInvalid wraps every reason a file is not the package it claims to be.
var ErrInvalid = errors.New("not a valid package")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// maxEntry bounds a manifest or property list read from an archive: a
// real one is kilobytes, a bigger one is a decompression bomb.
const maxEntry = 4 << 20

// ReadAPK reads an Android package: a zip holding one compiled
// AndroidManifest.xml that names its package.
func ReadAPK(r io.ReaderAt, size int64) (Info, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return Info{}, invalid("not a zip archive")
	}
	var manifest *zip.File
	for _, f := range zr.File {
		if f.Name != "AndroidManifest.xml" {
			continue
		}
		if manifest != nil {
			return Info{}, invalid("two AndroidManifest.xml")
		}
		manifest = f
	}
	if manifest == nil {
		return Info{}, invalid("no AndroidManifest.xml")
	}
	data, err := entry(manifest)
	if err != nil {
		return Info{}, err
	}
	return readManifest(data)
}

// ReadIPA reads an iOS app: a zip holding one app, the directory
// Payload/<name>.app/, whose own Info.plist (not one of its frameworks' or
// extensions') names its bundle identifier.
func ReadIPA(r io.ReaderAt, size int64) (Info, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return Info{}, invalid("not a zip archive")
	}
	var (
		app   string
		plist *zip.File
	)
	for _, f := range zr.File {
		rest, ok := strings.CutPrefix(f.Name, "Payload/")
		bundle, inside, dir := strings.Cut(rest, "/")
		if !ok || !dir || !strings.HasSuffix(bundle, ".app") {
			continue
		}
		if bundle == ".app" {
			return Info{}, invalid("an app with no name (Payload/.app)")
		}
		if app != "" && bundle != app {
			return Info{}, invalid("two apps: %q and %q", "Payload/"+app, "Payload/"+bundle)
		}
		app = bundle
		if inside == "Info.plist" {
			if plist != nil {
				return Info{}, invalid("two %q", f.Name)
			}
			plist = f
		}
	}
	if plist == nil {
		return Info{}, invalid("no Payload/<name>.app/Info.plist")
	}
	data, err := entry(plist)
	if err != nil {
		return Info{}, err
	}
	dict, err := plistDict(data)
	if err != nil {
		return Info{}, err
	}
	nameKey := "CFBundleDisplayName"
	if dict[nameKey] == "" {
		nameKey = "CFBundleName"
	}
	info := Info{
		Package: dict["CFBundleIdentifier"], Version: dict["CFBundleShortVersionString"], Build: dict["CFBundleVersion"],
		MinOS: dict["MinimumOSVersion"], Name: dict[nameKey],
	}
	if info.Package == "" {
		return Info{}, invalid("Info.plist names no CFBundleIdentifier")
	}
	if err := checkText("Info.plist", [][2]string{
		{"CFBundleIdentifier", info.Package},
		{"CFBundleShortVersionString", info.Version},
		{"CFBundleVersion", info.Build},
		{"MinimumOSVersion", info.MinOS},
		{nameKey, info.Name},
	}); err != nil {
		return Info{}, err
	}
	return info, nil
}

// CheckMobileconfig reports whether b is a configuration profile: a
// property list of PayloadType Configuration, as it is or signed in a CMS
// (PKCS #7) envelope.
func CheckMobileconfig(b []byte) error {
	if len(b) > 0 && b[0] == 0x30 {
		inner, err := cmsContent(b)
		if err != nil {
			return err
		}
		b = inner
	}
	dict, err := plistDict(b)
	if err != nil {
		return err
	}
	if t := dict["PayloadType"]; t != "Configuration" {
		return invalid("PayloadType is %q, not Configuration", t)
	}
	return nil
}

// entry reads one file of an archive, at most maxEntry bytes.
func entry(f *zip.File) ([]byte, error) {
	if f.UncompressedSize64 > maxEntry {
		return nil, invalid("%s is too big (%d bytes)", f.Name, f.UncompressedSize64)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, invalid("%s cannot be read: %v", f.Name, err)
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, maxEntry+1))
	if err != nil {
		return nil, invalid("%s cannot be read: %v", f.Name, err)
	}
	if len(data) > maxEntry {
		return nil, invalid("%s is too big", f.Name)
	}
	return data, nil
}

// checkText checks that each value read, named by its key, is text.
func checkText(file string, values [][2]string) error {
	for _, kv := range values {
		if !text(kv[1]) {
			return invalid("%s: %s is not text (invalid UTF-8 or UTF-16, or a control character)", file, kv[0])
		}
	}
	return nil
}

// text reports whether s is valid UTF-8 without a control character or
// U+FFFD, the replacement utf16String writes for invalid UTF-16.
func text(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r == utf8.RuneError || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// utf16String decodes UTF-16 in the given byte order, an unpaired
// surrogate as U+FFFD, into one allocation: a unit is at most three bytes
// of UTF-8.
func utf16String(b []byte, order binary.ByteOrder) string {
	var s strings.Builder
	s.Grow(len(b) / 2 * 3)
	for i := 0; i+2 <= len(b); i += 2 {
		r := rune(order.Uint16(b[i:]))
		if utf16.IsSurrogate(r) {
			var low rune
			if i+4 <= len(b) {
				low = rune(order.Uint16(b[i+2:]))
			}
			if r = utf16.DecodeRune(r, low); r != utf8.RuneError {
				i += 2
			}
		}
		s.WriteRune(r)
	}
	return s.String()
}
