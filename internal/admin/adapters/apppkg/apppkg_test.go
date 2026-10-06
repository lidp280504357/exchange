package apppkg

import (
	"archive/zip"
	"bytes"
	"encoding/asn1"
	"encoding/binary"
	"errors"
	"testing"
	"unicode/utf16"
)

// axmlAttr is an attribute of a compiled manifest built for a test: a raw
// string value, or a typed one; name "" with resID is a stripped name.
type axmlAttr struct {
	name  string
	resID uint32
	raw   string
	typ   uint8
	data  uint32
}

type axmlElem struct {
	name  string
	attrs []axmlAttr
}

// axml builds a compiled AndroidManifest.xml as aapt2 writes one: the
// string pool (the attributes with resource IDs first, as the resource map
// lists them), the resource map, then the elements.
func axml(utf8Pool bool, elems []axmlElem) []byte {
	var strs []string
	var resIDs []uint32
	index := map[string]uint32{}
	add := func(s string, dedupe bool) uint32 {
		if i, ok := index[s]; ok && dedupe {
			return i
		}
		strs = append(strs, s)
		i := u32(len(strs) - 1)
		if dedupe {
			index[s] = i
		}
		return i
	}
	nameIdx := map[*axmlAttr]uint32{}
	for ei := range elems {
		attrs := elems[ei].attrs
		for ai := range attrs {
			a := &attrs[ai]
			if a.resID != 0 {
				nameIdx[a] = add(a.name, a.name != "")
				resIDs = append(resIDs, a.resID)
			}
		}
	}
	le := binary.LittleEndian
	var body bytes.Buffer
	chunk := func(typ, hsize uint16, payload []byte) {
		h := make([]byte, 8)
		le.PutUint16(h, typ)
		le.PutUint16(h[2:], hsize)
		le.PutUint32(h[4:], u32(8+len(payload)))
		body.Write(h)
		body.Write(payload)
	}
	// The elements' strings, then the elements (written after the pool).
	var elemChunks [][]byte
	for ei := range elems {
		e := &elems[ei]
		name := add(e.name, true)
		attrs := make([]byte, 0, 20*len(e.attrs))
		for ai := range e.attrs {
			a := &e.attrs[ai]
			n, ok := nameIdx[a]
			if !ok {
				n = add(a.name, true)
			}
			raw, typ, data := uint32(0xffffffff), a.typ, a.data
			if a.raw != "" {
				raw = add(a.raw, true)
				typ, data = typeString, raw
			}
			at := make([]byte, 20)
			le.PutUint32(at, 0xffffffff)
			le.PutUint32(at[4:], n)
			le.PutUint32(at[8:], raw)
			le.PutUint16(at[12:], 8)
			at[15] = typ
			le.PutUint32(at[16:], data)
			attrs = append(attrs, at...)
		}
		p := make([]byte, 8+20)
		le.PutUint32(p[4:], 0xffffffff) // comment
		le.PutUint32(p[8:], 0xffffffff) // namespace
		le.PutUint32(p[12:], name)
		le.PutUint16(p[16:], 20) // attributeStart
		le.PutUint16(p[18:], 20) // attributeSize
		le.PutUint16(p[20:], u16(len(e.attrs)))
		elemChunks = append(elemChunks, append(p, attrs...))
	}
	// The string pool.
	var data bytes.Buffer
	offsets := make([]byte, 4*len(strs))
	for i, s := range strs {
		le.PutUint32(offsets[4*i:], u32(data.Len()))
		if utf8Pool {
			data.WriteByte(u8(len([]rune(s))))
			data.WriteByte(u8(len(s)))
			data.WriteString(s)
			data.WriteByte(0)
		} else {
			units := utf16.Encode([]rune(s))
			_ = binary.Write(&data, le, u16(len(units)))
			_ = binary.Write(&data, le, units)
			_ = binary.Write(&data, le, uint16(0))
		}
	}
	for data.Len()%4 != 0 {
		data.WriteByte(0)
	}
	pool := make([]byte, 20)
	le.PutUint32(pool, u32(len(strs)))
	if utf8Pool {
		le.PutUint32(pool[8:], stringPoolUTF8)
	}
	le.PutUint32(pool[12:], u32(28+len(offsets)))
	pool = append(append(pool, offsets...), data.Bytes()...)
	chunk(chunkStringPool, 28, pool)
	ids := make([]byte, 4*len(resIDs))
	for i, id := range resIDs {
		le.PutUint32(ids[4*i:], id)
	}
	chunk(chunkResourceMap, 8, ids)
	for _, c := range elemChunks {
		chunk(chunkStartElement, 16, c)
	}
	out := make([]byte, 8)
	le.PutUint16(out, chunkXML)
	le.PutUint16(out[2:], 8)
	le.PutUint32(out[4:], u32(8+body.Len()))
	return append(out, body.Bytes()...)
}

// u8, u16 and u32 narrow a test's sizes and counts, all small.
func u8(n int) byte    { return byte(n) }   //nolint:gosec // small
func u16(n int) uint16 { return uint16(n) } //nolint:gosec // small
func u32(n int) uint32 { return uint32(n) } //nolint:gosec // small

// zipOf archives files (name, content).
func zipOf(t *testing.T, files ...[2]string) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range files {
		fw, err := w.Create(f[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(f[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(buf.Bytes())
}

func manifestOf() []axmlElem {
	return []axmlElem{
		{name: "manifest", attrs: []axmlAttr{
			{name: "versionCode", resID: attrVersionCode, typ: typeIntDec, data: 42},
			{name: "versionName", resID: attrVersionName, raw: "1.2.0"},
			{name: "package", raw: "vip.astras.app"},
		}},
		{name: "uses-sdk", attrs: []axmlAttr{{name: "minSdkVersion", resID: attrMinSdkVersion, typ: typeIntDec, data: 24}}},
	}
}

func TestReadAPK(t *testing.T) {
	want := Info{Package: "vip.astras.app", Version: "1.2.0", Build: "42", MinOS: "24"}
	for _, utf8Pool := range []bool{false, true} {
		r := zipOf(t, [2]string{"classes.dex", "dex"}, [2]string{"AndroidManifest.xml", string(axml(utf8Pool, manifestOf()))})
		got, err := ReadAPK(r, r.Size())
		if err != nil || got != want {
			t.Fatalf("utf8 %v: %+v %v", utf8Pool, got, err)
		}
	}
	// Names stripped from the string pool: read by their resource IDs.
	stripped := manifestOf()
	for i := range stripped[0].attrs {
		if stripped[0].attrs[i].resID != 0 {
			stripped[0].attrs[i].name = ""
		}
	}
	stripped[1].attrs[0].name = ""
	r := zipOf(t, [2]string{"AndroidManifest.xml", string(axml(true, stripped))})
	if got, err := ReadAPK(r, r.Size()); err != nil || got != want {
		t.Fatalf("stripped names: %+v %v", got, err)
	}

	noPackage := manifestOf()
	noPackage[0].attrs = noPackage[0].attrs[:2]
	truncated := axml(false, manifestOf())
	for name, r := range map[string]*bytes.Reader{
		"not a zip":         bytes.NewReader([]byte("PK? no")),
		"no manifest":       zipOf(t, [2]string{"classes.dex", "dex"}),
		"plain XML":         zipOf(t, [2]string{"AndroidManifest.xml", `<manifest package="a.b"/>`}),
		"no package":        zipOf(t, [2]string{"AndroidManifest.xml", string(axml(false, noPackage))}),
		"truncated":         zipOf(t, [2]string{"AndroidManifest.xml", string(truncated[:len(truncated)-30])}),
		"no manifest elem":  zipOf(t, [2]string{"AndroidManifest.xml", string(axml(false, []axmlElem{{name: "application"}}))}),
		"an empty manifest": zipOf(t, [2]string{"AndroidManifest.xml", ""}),
	} {
		if _, err := ReadAPK(r, r.Size()); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// bplistDict encodes a dictionary of string and integer values as a
// binary property list (one-byte references, two-byte offsets).
func bplistDict(entries [][2]any) []byte {
	var objs [][]byte
	str := func(s string) []byte {
		ascii := true
		for _, r := range s {
			if r > 0x7f {
				ascii = false
			}
		}
		head := func(marker byte, n int) []byte {
			if n < 15 {
				return []byte{marker | u8(n)}
			}
			return []byte{marker | 0x0f, 0x10, u8(n)}
		}
		if ascii {
			return append(head(0x50, len(s)), s...)
		}
		units := utf16.Encode([]rune(s))
		out := head(0x60, len(units))
		for _, u := range units {
			out = binary.BigEndian.AppendUint16(out, u)
		}
		return out
	}
	n := len(entries)
	dict := []byte{0xd0 | u8(n)}
	for i := range n {
		dict = append(dict, u8(1+i))
	}
	for i := range n {
		dict = append(dict, u8(1+n+i))
	}
	objs = append(objs, dict)
	for _, e := range entries {
		objs = append(objs, str(e[0].(string)))
	}
	for _, e := range entries {
		switch v := e[1].(type) {
		case string:
			objs = append(objs, str(v))
		case int:
			objs = append(objs, binary.BigEndian.AppendUint64([]byte{0x13}, uint64(u32(v))))
		}
	}
	out := []byte("bplist00")
	var offsets []byte
	for _, o := range objs {
		offsets = binary.BigEndian.AppendUint16(offsets, u16(len(out)))
		out = append(out, o...)
	}
	table := len(out)
	out = append(out, offsets...)
	trailer := make([]byte, 32)
	trailer[6], trailer[7] = 2, 1
	binary.BigEndian.PutUint64(trailer[8:], uint64(len(objs)))
	binary.BigEndian.PutUint64(trailer[24:], uint64(table))
	return append(out, trailer...)
}

const infoPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>vip.astras.app</string>
	<key>CFBundleIcons</key>
	<dict><key>CFBundlePrimaryIcon</key><dict><key>CFBundleIconName</key><string>AppIcon</string></dict></dict>
	<key>CFBundleShortVersionString</key>
	<string>1.2.0</string>
	<key>UIDeviceFamily</key>
	<array><integer>1</integer><integer>2</integer></array>
	<key>CFBundleVersion</key>
	<string>42</string>
	<key>LSRequiresIPhoneOS</key>
	<true/>
	<key>MinimumOSVersion</key>
	<string>15.0</string>
	<key>CFBundleName</key>
	<string>Astras</string>
</dict>
</plist>`

func TestReadIPA(t *testing.T) {
	r := zipOf(t, [2]string{"Payload/Astras.app/Frameworks/X.framework/Info.plist", `<plist><dict><key>CFBundleIdentifier</key><string>x</string></dict></plist>`},
		[2]string{"Payload/Astras.app/Info.plist", infoPlist})
	got, err := ReadIPA(r, r.Size())
	if err != nil || got != (Info{Package: "vip.astras.app", Version: "1.2.0", Build: "42", MinOS: "15.0", Name: "Astras"}) {
		t.Fatalf("XML: %+v %v", got, err)
	}
	bin := bplistDict([][2]any{
		{"CFBundleIdentifier", "vip.astras.app"},
		{"CFBundleShortVersionString", "1.2.0"},
		{"CFBundleVersion", 42},
		{"MinimumOSVersion", "15.0"},
		{"CFBundleDisplayName", "阿斯特拉交易所的应用程序"},
		{"CFBundleName", "Astras"},
	})
	r = zipOf(t, [2]string{"Payload/Astras.app/Info.plist", string(bin)})
	got, err = ReadIPA(r, r.Size())
	if err != nil || got != (Info{Package: "vip.astras.app", Version: "1.2.0", Build: "42", MinOS: "15.0", Name: "阿斯特拉交易所的应用程序"}) {
		t.Fatalf("binary: %+v %v", got, err)
	}
	for name, r := range map[string]*bytes.Reader{
		"not a zip":         bytes.NewReader([]byte("nope")),
		"a framework's":     zipOf(t, [2]string{"Payload/Astras.app/Frameworks/X.framework/Info.plist", infoPlist}),
		"no bundle id":      zipOf(t, [2]string{"Payload/A.app/Info.plist", `<plist><dict><key>CFBundleName</key><string>A</string></dict></plist>`}),
		"not a plist":       zipOf(t, [2]string{"Payload/A.app/Info.plist", `<html></html>`}),
		"a broken bplist":   zipOf(t, [2]string{"Payload/A.app/Info.plist", string(bin[:len(bin)-20])}),
		"an array at top":   zipOf(t, [2]string{"Payload/A.app/Info.plist", `<plist><array><string>a</string></array></plist>`}),
		"Payload elsewhere": zipOf(t, [2]string{"Other/A.app/Info.plist", infoPlist}),
	} {
		if _, err := ReadIPA(r, r.Size()); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

const profile = `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
	<key>PayloadContent</key><array><dict><key>PayloadType</key><string>com.apple.webClip.managed</string></dict></array>
	<key>PayloadDisplayName</key><string>Astras</string>
	<key>PayloadType</key><string>Configuration</string>
	<key>PayloadVersion</key><integer>1</integer>
</dict></plist>`

func TestCheckMobileconfig(t *testing.T) {
	if err := CheckMobileconfig([]byte(profile)); err != nil {
		t.Fatalf("a profile: %v", err)
	}
	// Signed: a CMS SignedData wrapping it (DER), and one the parser cannot
	// read whose property list is still in it.
	type encap struct {
		ContentType asn1.ObjectIdentifier
		Content     []byte `asn1:"explicit,tag:0"`
	}
	type sd struct {
		Version          int
		DigestAlgorithms asn1.RawValue
		EncapContent     encap
	}
	inner, err := asn1.Marshal(sd{
		Version: 1, DigestAlgorithms: asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSet, IsCompound: true},
		EncapContent: encap{ContentType: asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}, Content: []byte(profile)},
	})
	if err != nil {
		t.Fatal(err)
	}
	signed, err := asn1.Marshal(struct {
		ContentType asn1.ObjectIdentifier
		Content     asn1.RawValue `asn1:"explicit,tag:0"`
	}{oidSignedData, asn1.RawValue{FullBytes: inner}})
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckMobileconfig(signed); err != nil {
		t.Fatalf("signed: %v", err)
	}
	if err := CheckMobileconfig(append([]byte{0x30, 0x80, 0x06, 0x09}, profile...)); err != nil {
		t.Fatalf("signed in BER: %v", err)
	}
	for name, b := range map[string][]byte{
		"another type": []byte(`<plist><dict><key>PayloadType</key><string>com.apple.wifi.managed</string></dict></plist>`),
		"no type":      []byte(`<plist><dict></dict></plist>`),
		"not a plist":  []byte("PK\x03\x04"),
		"signed junk":  {0x30, 0x03, 0x02, 0x01, 0x01},
	} {
		if err := CheckMobileconfig(b); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
