// Package apppkgtest builds minimal packages of the kinds the console takes
// (design 2026-10-07, App download page): an .apk whose compiled
// AndroidManifest.xml names its package, versions and minimum SDK, an .ipa
// with Payload/E2E.app/Info.plist, a .mobileconfig. Nothing in them runs;
// they are for tests and the e2e (scripts/e2e/appfixture).
package apppkgtest

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"unicode/utf16"
)

// APK is an .apk of package pkg.
func APK(pkg, versionName string, versionCode, minSdk uint32) []byte {
	return zipOf(map[string][]byte{
		"AndroidManifest.xml": manifest(pkg, versionName, versionCode, minSdk), "classes.dex": []byte("dex\n035\x00"),
	})
}

// APKPadded is APK with an entry of pad bytes that do not compress (stored
// as they are): an .apk of about that size, for uploads in several parts.
func APKPadded(pkg, versionName string, versionCode, minSdk uint32, pad int) []byte {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	add := func(h *zip.FileHeader, data []byte) {
		f, err := w.CreateHeader(h)
		if err == nil {
			_, err = f.Write(data)
		}
		if err != nil {
			panic(err)
		}
	}
	add(&zip.FileHeader{Name: "AndroidManifest.xml", Method: zip.Deflate}, manifest(pkg, versionName, versionCode, minSdk))
	noise := make([]byte, pad)
	x := uint64(0x9e3779b97f4a7c15)
	for i := range noise {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		noise[i] = byte(x) //nolint:gosec // the low byte of the generator, meant to wrap
	}
	add(&zip.FileHeader{Name: "assets/pad.bin", Method: zip.Store}, noise)
	if err := w.Close(); err != nil {
		panic(err)
	}
	return b.Bytes()
}

// IPA is an .ipa of bundle with its versions and minimum iOS.
func IPA(bundle, version, build, minOS string) []byte {
	return zipOf(map[string][]byte{"Payload/E2E.app/Info.plist": infoPlist(bundle, version, build, minOS), "Payload/E2E.app/E2E": []byte("e2e")})
}

// Mobileconfig is an empty configuration profile.
func Mobileconfig() []byte { return mobileconfig() }

func zipOf(entries map[string][]byte) []byte {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, name := range []string{"AndroidManifest.xml", "classes.dex", "Payload/E2E.app/Info.plist", "Payload/E2E.app/E2E"} {
		data, ok := entries[name]
		if !ok {
			continue
		}
		f, err := w.Create(name)
		if err == nil {
			_, err = f.Write(data)
		}
		if err != nil {
			panic(err)
		}
	}
	if err := w.Close(); err != nil {
		panic(err)
	}
	return b.Bytes()
}

// The compiled XML's chunks and value types (Android's ResXMLTree).
const (
	chunkXML          = 0x0003
	chunkStringPool   = 0x0001
	chunkStartElement = 0x0102
	noString          = 0xffffffff
	typeString        = 0x03
	typeIntDec        = 0x10
)

// manifest compiles <manifest package versionCode versionName><uses-sdk
// minSdkVersion/></manifest> as aapt would, without the end elements and
// namespaces a reader of these four attributes does not need.
func manifest(pkg, versionName string, versionCode, minSdk uint32) []byte {
	strs := []string{"manifest", "package", "versionCode", "versionName", "uses-sdk", "minSdkVersion", pkg, versionName}
	idx := func(s string) uint32 {
		for i, x := range strs {
			if x == s {
				return uint32(i) //nolint:gosec // a handful of strings
			}
		}
		panic(s)
	}
	body := stringPool(strs)
	body = append(body, element(idx("manifest"), []attr{
		{idx("package"), idx(pkg), typeString, idx(pkg)},
		{idx("versionCode"), noString, typeIntDec, versionCode},
		{idx("versionName"), idx(versionName), typeString, idx(versionName)},
	})...)
	body = append(body, element(idx("uses-sdk"), []attr{{idx("minSdkVersion"), noString, typeIntDec, minSdk}})...)
	return append(header(chunkXML, 8, 8+len(body)), body...)
}

func header(typ uint16, hsize, size int) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint16(b, typ)
	binary.LittleEndian.PutUint16(b[2:], uint16(hsize)) //nolint:gosec // a chunk header's size
	binary.LittleEndian.PutUint32(b[4:], uint32(size))  //nolint:gosec // a small manifest
	return b
}

// stringPool is a UTF-16 string pool of strs.
func stringPool(strs []string) []byte {
	var data []byte
	offsets := make([]byte, 4*len(strs))
	for i, s := range strs {
		binary.LittleEndian.PutUint32(offsets[4*i:], uint32(len(data))) //nolint:gosec // a small pool
		u := utf16.Encode([]rune(s))
		data = binary.LittleEndian.AppendUint16(data, uint16(len(u))) //nolint:gosec // short strings
		for _, c := range u {
			data = binary.LittleEndian.AppendUint16(data, c)
		}
		data = binary.LittleEndian.AppendUint16(data, 0)
	}
	for len(data)%4 != 0 {
		data = append(data, 0)
	}
	const hsize = 28
	size := hsize + len(offsets) + len(data)
	b := header(chunkStringPool, hsize, size)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(strs)))          //nolint:gosec // a small pool
	b = binary.LittleEndian.AppendUint32(b, 0)                          // styles
	b = binary.LittleEndian.AppendUint32(b, 0)                          // flags: UTF-16
	b = binary.LittleEndian.AppendUint32(b, uint32(hsize+len(offsets))) //nolint:gosec // strings start
	b = binary.LittleEndian.AppendUint32(b, 0)                          // styles start
	return append(append(b, offsets...), data...)
}

// attr is an attribute: its name, its raw value (a string, or noString),
// and its typed value.
type attr struct {
	name, raw uint32
	typ       byte
	data      uint32
}

// element is a start element named name with its attributes.
func element(name uint32, attrs []attr) []byte {
	const hsize, ext, attrSize = 16, 20, 20
	size := hsize + ext + attrSize*len(attrs)
	b := header(chunkStartElement, hsize, size)
	b = binary.LittleEndian.AppendUint32(b, 1)        // line
	b = binary.LittleEndian.AppendUint32(b, noString) // comment
	b = binary.LittleEndian.AppendUint32(b, noString) // namespace
	b = binary.LittleEndian.AppendUint32(b, name)
	b = binary.LittleEndian.AppendUint16(b, ext)
	b = binary.LittleEndian.AppendUint16(b, attrSize)
	b = binary.LittleEndian.AppendUint16(b, uint16(len(attrs))) //nolint:gosec // a few attributes
	b = append(b, 0, 0, 0, 0, 0, 0)                             // id, class and style indexes
	for _, a := range attrs {
		b = binary.LittleEndian.AppendUint32(b, noString) // namespace
		b = binary.LittleEndian.AppendUint32(b, a.name)
		b = binary.LittleEndian.AppendUint32(b, a.raw)
		b = binary.LittleEndian.AppendUint16(b, 8)
		b = append(b, 0, a.typ)
		b = binary.LittleEndian.AppendUint32(b, a.data)
	}
	return b
}

func infoPlist(bundle, version, build, minOS string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key><string>` + bundle + `</string>
	<key>CFBundleShortVersionString</key><string>` + version + `</string>
	<key>CFBundleVersion</key><string>` + build + `</string>
	<key>MinimumOSVersion</key><string>` + minOS + `</string>
	<key>CFBundleName</key><string>E2E</string>
</dict>
</plist>
`)
}

func mobileconfig() []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key><array/>
	<key>PayloadDisplayName</key><string>Astras e2e</string>
	<key>PayloadIdentifier</key><string>vip.astras.e2e.profile</string>
	<key>PayloadType</key><string>Configuration</string>
	<key>PayloadUUID</key><string>0192A000-0000-7000-8000-0000000000E2</string>
	<key>PayloadVersion</key><integer>1</integer>
</dict>
</plist>
`)
}
