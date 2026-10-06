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
	"errors"
	"fmt"
	"io"
	"strings"
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

// ReadAPK reads an Android package: a zip holding a compiled
// AndroidManifest.xml that names its package.
func ReadAPK(r io.ReaderAt, size int64) (Info, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return Info{}, invalid("not a zip archive")
	}
	for _, f := range zr.File {
		if f.Name == "AndroidManifest.xml" {
			data, err := entry(f)
			if err != nil {
				return Info{}, err
			}
			return readManifest(data)
		}
	}
	return Info{}, invalid("no AndroidManifest.xml")
}

// ReadIPA reads an iOS app: a zip holding Payload/<name>.app/Info.plist
// (the app's own, not one of its frameworks' or extensions') that names
// its bundle identifier.
func ReadIPA(r io.ReaderAt, size int64) (Info, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return Info{}, invalid("not a zip archive")
	}
	for _, f := range zr.File {
		parts := strings.Split(f.Name, "/")
		if len(parts) != 3 || parts[0] != "Payload" || !strings.HasSuffix(parts[1], ".app") || parts[2] != "Info.plist" {
			continue
		}
		data, err := entry(f)
		if err != nil {
			return Info{}, err
		}
		dict, err := plistDict(data)
		if err != nil {
			return Info{}, err
		}
		info := Info{
			Package: dict["CFBundleIdentifier"], Version: dict["CFBundleShortVersionString"], Build: dict["CFBundleVersion"],
			MinOS: dict["MinimumOSVersion"], Name: dict["CFBundleDisplayName"],
		}
		if info.Name == "" {
			info.Name = dict["CFBundleName"]
		}
		if info.Package == "" {
			return Info{}, invalid("Info.plist names no CFBundleIdentifier")
		}
		return info, nil
	}
	return Info{}, invalid("no Payload/<name>.app/Info.plist")
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
