package apppkg

import (
	"archive/zip"
	"bytes"
	"encoding/asn1"
	"encoding/binary"
	"errors"
	"runtime"
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

// strippedManifest is manifestOf with the names of the attributes that
// have a resource ID stripped from the string pool.
func strippedManifest() []axmlElem {
	m := manifestOf()
	for i := range m {
		for j := range m[i].attrs {
			if m[i].attrs[j].resID != 0 {
				m[i].attrs[j].name = ""
			}
		}
	}
	return m
}

// manifestWith is manifestOf with attribute key's value the raw string v.
func manifestWith(key, v string) []axmlElem {
	m := manifestOf()
	for i := range m {
		for j := range m[i].attrs {
			if m[i].attrs[j].name == key {
				m[i].attrs[j].raw = v
			}
		}
	}
	return m
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
	r := zipOf(t, [2]string{"AndroidManifest.xml", string(axml(true, strippedManifest()))})
	if got, err := ReadAPK(r, r.Size()); err != nil || got != want {
		t.Fatalf("stripped names: %+v %v", got, err)
	}

	manifest := func(m []byte) [2]string { return [2]string{"AndroidManifest.xml", string(m)} }
	noPackage := manifestOf()
	noPackage[0].attrs = noPackage[0].attrs[:2]
	truncated := axml(false, manifestOf())
	// An unpaired surrogate in a UTF-16 pool: U+E0E0 (E0 E0) made U+D800.
	surrogate := axml(false, manifestWith("versionName", "1.2.\ue0e0"))
	if bytes.Count(surrogate, []byte{0xe0, 0xe0}) != 1 {
		t.Fatal("no single U+E0E0 to replace")
	}
	surrogate = bytes.Replace(surrogate, []byte{0xe0, 0xe0}, []byte{0x00, 0xd8}, 1)
	for name, r := range map[string]*bytes.Reader{
		"not a zip":         bytes.NewReader([]byte("PK? no")),
		"no manifest":       zipOf(t, [2]string{"classes.dex", "dex"}),
		"two manifests":     zipOf(t, manifest(axml(false, manifestOf())), manifest(axml(false, manifestWith("package", "other.app")))),
		"plain XML":         zipOf(t, [2]string{"AndroidManifest.xml", `<manifest package="a.b"/>`}),
		"no package":        zipOf(t, manifest(axml(false, noPackage))),
		"truncated":         zipOf(t, manifest(truncated[:len(truncated)-30])),
		"no manifest elem":  zipOf(t, manifest(axml(false, []axmlElem{{name: "application"}}))),
		"an empty manifest": zipOf(t, [2]string{"AndroidManifest.xml", ""}),
		// Values that are not text.
		"a package with a newline":      zipOf(t, manifest(axml(true, manifestWith("package", "vip.astras\napp")))),
		"a versionName not UTF-8":       zipOf(t, manifest(axml(true, manifestWith("versionName", "1.2\xff")))),
		"a versionName not UTF-16":      zipOf(t, manifest(surrogate)),
		"a versionCode with a NUL":      zipOf(t, manifest(axml(false, manifestWith("versionCode", "42\x00")))),
		"a minSdkVersion with a C1 NEL": zipOf(t, manifest(axml(true, manifestWith("minSdkVersion", "2\u00854")))),
	} {
		if _, err := ReadAPK(r, r.Size()); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// allocated is how many bytes fn allocates.
func allocated(fn func()) uint64 {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// poolBomb is a compiled manifest with nothing but a string pool of n
// strings, all at one offset: one string of size bytes (UTF-8, size below
// 0x8000) or units (UTF-16).
func poolBomb(utf8Pool bool, n, size int) []byte {
	le := binary.LittleEndian
	var data []byte
	if utf8Pool {
		length := []byte{u8(0x80 | size>>8), u8(size)}
		data = append(append(append(data, length...), length...), bytes.Repeat([]byte("a"), size)...)
		data = append(data, 0)
	} else {
		data = le.AppendUint16(le.AppendUint16(data, u16(0x8000|size>>16)), u16(size))
		data = append(data, bytes.Repeat([]byte{'a', 0}, size)...)
		data = append(data, 0, 0)
	}
	for len(data)%4 != 0 {
		data = append(data, 0)
	}
	pool := make([]byte, 28+4*n) // every offset 0
	le.PutUint16(pool, chunkStringPool)
	le.PutUint16(pool[2:], 28)
	le.PutUint32(pool[4:], u32(len(pool)+len(data)))
	le.PutUint32(pool[8:], u32(n))
	if utf8Pool {
		le.PutUint32(pool[16:], stringPoolUTF8)
	}
	le.PutUint32(pool[20:], u32(len(pool)))
	out := make([]byte, 8)
	le.PutUint16(out, chunkXML)
	le.PutUint16(out[2:], 8)
	le.PutUint32(out[4:], u32(8+len(pool)+len(data)))
	return append(append(out, pool...), data...)
}

func TestStringPoolBomb(t *testing.T) {
	// One string at two offsets, as aapt pools an attribute name it uses
	// both with and without a resource ID: read.
	for _, utf8Pool := range []bool{false, true} {
		strs, err := stringPool(poolBomb(utf8Pool, 2, 5)[8:])
		if err != nil || len(strs) != 2 || strs[0] != "aaaaa" || strs[1] != "aaaaa" {
			t.Fatalf("utf8 %v: %q %v", utf8Pool, strs, err)
		}
	}
	// 4,096 offsets at one string of 32 Ki characters (128 MiB read from
	// 49 KiB before), and 2^18 at one of 1 Mi units (3 MiB, near the most
	// read from an archive): refused, little read.
	for _, c := range []struct {
		utf8Pool bool
		n, size  int
	}{{true, 4096, 0x7fff}, {false, 4096, 0x7fff}, {false, 1 << 18, 1 << 20}} {
		b := poolBomb(c.utf8Pool, c.n, c.size)
		var err error
		if n := allocated(func() { _, err = readManifest(b) }); !errors.Is(err, ErrInvalid) || n > 16<<20 {
			t.Errorf("%+v: %d bytes allocated reading %d: %v", c, n, len(b), err)
		}
	}
}

// bplistDict encodes a dictionary of string and integer values as a
// binary property list (one-byte references, two-byte offsets); a []byte
// value is the bytes of an ASCII string object, a []uint16 the units of a
// UTF-16 one, as they are.
func bplistDict(entries [][2]any) []byte {
	head := func(marker byte, n int) []byte {
		if n < 15 {
			return []byte{marker | u8(n)}
		}
		return []byte{marker | 0x0f, 0x10, u8(n)}
	}
	wide := func(units []uint16) []byte {
		out := head(0x60, len(units))
		for _, u := range units {
			out = binary.BigEndian.AppendUint16(out, u)
		}
		return out
	}
	str := func(s string) []byte {
		for _, r := range s {
			if r > 0x7f {
				return wide(utf16.Encode([]rune(s)))
			}
		}
		return append(head(0x50, len(s)), s...)
	}
	n := len(entries)
	dict := []byte{0xd0 | u8(n)}
	for i := range n {
		dict = append(dict, u8(1+i))
	}
	for i := range n {
		dict = append(dict, u8(1+n+i))
	}
	objs := [][]byte{dict}
	for _, e := range entries {
		objs = append(objs, str(e[0].(string)))
	}
	for _, e := range entries {
		switch v := e[1].(type) {
		case string:
			objs = append(objs, str(v))
		case int:
			objs = append(objs, binary.BigEndian.AppendUint64([]byte{0x13}, uint64(u32(v))))
		case []byte:
			objs = append(objs, append(head(0x50, len(v)), v...))
		case []uint16:
			objs = append(objs, wide(v))
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

// dictBomb is a binary property list whose dictionary has n entries, every
// key the one string "k" and every value the one string of size
// characters, ASCII or (wide) UTF-16.
func dictBomb(wide bool, n, size int) []byte {
	be := binary.BigEndian
	out := []byte("bplist00")
	var offsets []byte
	object := func(b []byte) {
		offsets = be.AppendUint32(offsets, u32(len(out)))
		out = append(out, b...)
	}
	dict := be.AppendUint32([]byte{0xdf, 0x12}, u32(n)) // a four-byte count
	dict = append(dict, bytes.Repeat([]byte{1}, n)...)  // the keys: object 1
	dict = append(dict, bytes.Repeat([]byte{2}, n)...)  // the values: object 2
	object(dict)
	object([]byte{0x51, 'k'})
	if wide {
		object(append(be.AppendUint32([]byte{0x6f, 0x12}, u32(size)), bytes.Repeat([]byte{0, 'v'}, size)...))
	} else {
		object(append(be.AppendUint32([]byte{0x5f, 0x12}, u32(size)), bytes.Repeat([]byte("v"), size)...))
	}
	table := len(out)
	out = append(out, offsets...)
	trailer := make([]byte, 32)
	trailer[6], trailer[7] = 4, 1
	be.PutUint64(trailer[8:], 3)
	be.PutUint64(trailer[24:], uint64(table))
	return append(out, trailer...)
}

func TestBinaryDictBomb(t *testing.T) {
	// One string referenced a few times, as a writer that stores equal
	// strings once does: read.
	for _, wide := range []bool{false, true} {
		dict, err := plistDict(dictBomb(wide, 3, 5))
		if err != nil || len(dict) != 1 || dict["k"] != "vvvvv" {
			t.Fatalf("wide %v: %q %v", wide, dict, err)
		}
	}
	// 4,096 references to one string of 32 Ki characters (128 MiB read
	// from 41 KiB before), and to one of 1 Mi UTF-16 units: refused,
	// little read.
	for _, c := range []struct {
		wide    bool
		n, size int
	}{{false, 4096, 32 << 10}, {true, 4096, 32 << 10}, {true, 4096, 1 << 20}} {
		b := dictBomb(c.wide, c.n, c.size)
		var err error
		if n := allocated(func() { _, err = plistDict(b) }); !errors.Is(err, ErrInvalid) || n > 16<<20 {
			t.Errorf("%+v: %d bytes allocated reading %d: %v", c, n, len(b), err)
		}
	}
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

// binaryInfoPlist is an app's Info.plist in binary, its display name in
// Chinese.
func binaryInfoPlist() []byte {
	return bplistDict([][2]any{
		{"CFBundleIdentifier", "vip.astras.app"},
		{"CFBundleShortVersionString", "1.2.0"},
		{"CFBundleVersion", 42},
		{"MinimumOSVersion", "15.0"},
		{"CFBundleDisplayName", "阿斯特拉交易所的应用程序"},
		{"CFBundleName", "Astras"},
	})
}

func TestReadIPA(t *testing.T) {
	// The app's own Info.plist, not its frameworks', extensions' or watch
	// app's; directories, files beside the app (an AppleDouble file of
	// ditto's) and beside Payload/ left alone.
	other := `<plist><dict><key>CFBundleIdentifier</key><string>x</string></dict></plist>`
	r := zipOf(t, [2]string{"Payload/", ""}, [2]string{"Payload/Astras.app/", ""}, [2]string{"Payload/._Astras.app", "\x00\x05\x16\x07"},
		[2]string{"Payload/Astras.app/Frameworks/X.framework/Info.plist", other},
		[2]string{"Payload/Astras.app/PlugIns/W.appex/Info.plist", other},
		[2]string{"Payload/Astras.app/Watch/W.app/Info.plist", other},
		[2]string{"Payload/Astras.app/Info.plist", infoPlist},
		[2]string{"SwiftSupport/iphoneos/libswiftCore.dylib", "dylib"})
	got, err := ReadIPA(r, r.Size())
	if err != nil || got != (Info{Package: "vip.astras.app", Version: "1.2.0", Build: "42", MinOS: "15.0", Name: "Astras"}) {
		t.Fatalf("XML: %+v %v", got, err)
	}
	bin := binaryInfoPlist()
	r = zipOf(t, [2]string{"Payload/Astras.app/Info.plist", string(bin)})
	got, err = ReadIPA(r, r.Size())
	if err != nil || got != (Info{Package: "vip.astras.app", Version: "1.2.0", Build: "42", MinOS: "15.0", Name: "阿斯特拉交易所的应用程序"}) {
		t.Fatalf("binary: %+v %v", got, err)
	}

	plist := func(s string) [2]string { return [2]string{"Payload/A.app/Info.plist", s} }
	binPlist := func(entries ...[2]any) [2]string { return plist(string(bplistDict(entries))) }
	for name, r := range map[string]*bytes.Reader{
		"not a zip":         bytes.NewReader([]byte("nope")),
		"a framework's":     zipOf(t, [2]string{"Payload/Astras.app/Frameworks/X.framework/Info.plist", infoPlist}),
		"no bundle id":      zipOf(t, plist(`<plist><dict><key>CFBundleName</key><string>A</string></dict></plist>`)),
		"not a plist":       zipOf(t, plist(`<html></html>`)),
		"a broken bplist":   zipOf(t, plist(string(bin[:len(bin)-20]))),
		"an array at top":   zipOf(t, plist(`<plist><array><string>a</string></array></plist>`)),
		"Payload elsewhere": zipOf(t, [2]string{"Other/A.app/Info.plist", infoPlist}),
		// One app, named, with one Info.plist.
		"two apps":              zipOf(t, plist(infoPlist), [2]string{"Payload/B.app/Info.plist", infoPlist}),
		"another app beside":    zipOf(t, plist(infoPlist), [2]string{"Payload/B.app/B", "exe"}),
		"an app with no name":   zipOf(t, [2]string{"Payload/.app/Info.plist", infoPlist}),
		"two of its Info.plist": zipOf(t, plist(infoPlist), plist(infoPlist)),
		// Values that are not text.
		"a bundle ID with a C1 NEL": zipOf(t, plist(`<plist><dict><key>CFBundleIdentifier</key><string>vip.astras&#x85;app</string></dict></plist>`)),
		"a version with a tab": zipOf(t, plist(`<plist><dict><key>CFBundleIdentifier</key><string>a.b</string>`+
			`<key>CFBundleShortVersionString</key><string>1.2&#9;0</string></dict></plist>`)),
		"a minimum OS with a NUL": zipOf(t, binPlist([2]any{"CFBundleIdentifier", "a.b"}, [2]any{"MinimumOSVersion", "15\x00"})),
		"a build not UTF-8":       zipOf(t, binPlist([2]any{"CFBundleIdentifier", "a.b"}, [2]any{"CFBundleVersion", []byte("4\xff2")})),
		"a name not UTF-16":       zipOf(t, binPlist([2]any{"CFBundleIdentifier", "a.b"}, [2]any{"CFBundleDisplayName", []uint16{'A', 0xd800}})),
		"a name with a bell":      zipOf(t, binPlist([2]any{"CFBundleIdentifier", "a.b"}, [2]any{"CFBundleName", "Astras\a"})),
	} {
		if _, err := ReadIPA(r, r.Size()); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestText(t *testing.T) {
	for s, want := range map[string]bool{
		"":                           true,
		"vip.astras.app":             true,
		"1.2.0 (42)":                 true,
		"阿斯特拉":                       true,
		"\U0001f469\u200d\U0001f4bb": true, // U+200D joins, a format character
		"a\x00b":                     false,
		"1.2\n":                      false,
		"a\tb":                       false,
		"a\x7fb":                     false,
		"a\u0085b":                   false,
		"a\xffb":                     false,
		"a\ufffdb":                   false,
	} {
		if text(s) != want {
			t.Errorf("text(%q) = %v", s, !want)
		}
	}
}

// TestUTF16String decodes as utf16.Decode does every sequence of up to
// three units of a character, a surrogate of either half or the edges
// between them.
func TestUTF16String(t *testing.T) {
	edges := []uint16{'a', 0x7ff, 0x800, 0xd7ff, 0xd800, 0xdbff, 0xdc00, 0xdfff, 0xe000, 0xffff}
	seqs, last := [][]uint16{nil}, [][]uint16{nil}
	for range 3 {
		var longer [][]uint16
		for _, s := range last {
			for _, u := range edges {
				longer = append(longer, append(append([]uint16(nil), s...), u))
			}
		}
		seqs, last = append(seqs, longer...), longer
	}
	if len(seqs) != 1+10+100+1000 {
		t.Fatalf("%d sequences", len(seqs))
	}
	for _, units := range seqs {
		want := string(utf16.Decode(units))
		var le, be []byte
		for _, u := range units {
			le, be = binary.LittleEndian.AppendUint16(le, u), binary.BigEndian.AppendUint16(be, u)
		}
		if got := utf16String(le, binary.LittleEndian); got != want {
			t.Errorf("%04x little-endian: %+q, not %+q", units, got, want)
		}
		if got := utf16String(be, binary.BigEndian); got != want {
			t.Errorf("%04x big-endian: %+q, not %+q", units, got, want)
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

// binaryProfile is a configuration profile as a binary property list.
func binaryProfile() []byte {
	return bplistDict([][2]any{{"PayloadDisplayName", "Astras"}, {"PayloadType", "Configuration"}, {"PayloadVersion", 1}})
}

// signed wraps content in a CMS SignedData as a signed profile is, in DER:
// a ContentInfo whose [0] EXPLICIT holds the SignedData, whose
// encapsulated content holds content in its own [0] EXPLICIT (none for
// nil content: a detached signature), then stand-ins for the certificates
// and the signer's info.
func signed(tb testing.TB, content []byte) []byte {
	tb.Helper()
	type encapsulated struct {
		ContentType asn1.ObjectIdentifier
		Content     []byte `asn1:"explicit,optional,tag:0"`
	}
	type signedData struct {
		Version          int
		DigestAlgorithms asn1.RawValue
		EncapContent     encapsulated
		Certificates     asn1.RawValue
		SignerInfos      asn1.RawValue
	}
	sha256, err := asn1.Marshal(struct {
		Algorithm  asn1.ObjectIdentifier
		Parameters asn1.RawValue
	}{asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}, asn1.NullRawValue})
	if err != nil {
		tb.Fatal(err)
	}
	standIn, err := asn1.Marshal(struct{ Serial int }{1})
	if err != nil {
		tb.Fatal(err)
	}
	sd, err := asn1.Marshal(signedData{
		Version:          1,
		DigestAlgorithms: asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSet, IsCompound: true, Bytes: sha256},
		EncapContent:     encapsulated{ContentType: asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}, Content: content},
		Certificates:     asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: standIn},
		SignerInfos:      asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSet, IsCompound: true, Bytes: standIn},
	})
	if err != nil {
		tb.Fatal(err)
	}
	ci, err := asn1.Marshal(struct {
		ContentType asn1.ObjectIdentifier
		Content     asn1.RawValue
	}{oidSignedData, asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: sd}})
	if err != nil {
		tb.Fatal(err)
	}
	return ci
}

func TestCheckMobileconfig(t *testing.T) {
	for name, b := range map[string][]byte{"XML": []byte(profile), "binary": binaryProfile()} {
		if err := CheckMobileconfig(b); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// Signed in DER: the encapsulated content as it is, with the newline
	// after </plist> that finding the XML would drop, or binary.
	for name, content := range map[string][]byte{"XML": []byte(profile + "\n"), "binary": binaryProfile()} {
		b := signed(t, content)
		if got, err := cmsContent(b); err != nil || !bytes.Equal(got, content) {
			t.Fatalf("signed %s: %q %v", name, got, err)
		}
		if err := CheckMobileconfig(b); err != nil {
			t.Fatalf("signed %s: %v", name, err)
		}
	}
	// Signed in BER, which encoding/asn1 does not read: its XML property
	// list found in it.
	if err := CheckMobileconfig(append([]byte{0x30, 0x80, 0x06, 0x09}, profile...)); err != nil {
		t.Fatalf("signed in BER: %v", err)
	}
	for name, b := range map[string][]byte{
		"another type":         []byte(`<plist><dict><key>PayloadType</key><string>com.apple.wifi.managed</string></dict></plist>`),
		"no type":              []byte(`<plist><dict></dict></plist>`),
		"not a plist":          []byte("PK\x03\x04"),
		"signed junk":          {0x30, 0x03, 0x02, 0x01, 0x01},
		"signed, another type": signed(t, bplistDict([][2]any{{"PayloadType", "com.apple.wifi.managed"}})),
		"signed, detached":     signed(t, nil),
	} {
		if err := CheckMobileconfig(b); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
