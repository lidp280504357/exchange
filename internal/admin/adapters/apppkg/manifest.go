package apppkg

import (
	"encoding/binary"
	"strconv"
)

// An .apk's AndroidManifest.xml is compiled XML (Android's ResXMLTree):
// little-endian chunks, each a header {type uint16, header size uint16,
// size uint32}, after the file's own XML header: a string pool, the
// attribute names' resource IDs, then the elements, each start element
// with its attributes. Only <manifest> (package, versionCode, versionName)
// and <uses-sdk> (minSdkVersion) are read.

const (
	chunkStringPool   = 0x0001
	chunkXML          = 0x0003
	chunkStartElement = 0x0102
	chunkResourceMap  = 0x0180

	stringPoolUTF8 = 1 << 8

	// The attributes' resource IDs (android.R.attr), for a manifest whose
	// attribute names were stripped from the string pool.
	attrVersionCode   = 0x0101021b
	attrVersionName   = 0x0101021c
	attrMinSdkVersion = 0x0101020c

	// noString is a string index that names no string.
	noString = 0xffffffff

	// Typed values: a string, an integer in decimal or hexadecimal.
	typeString = 0x03
	typeIntDec = 0x10
	typeIntHex = 0x11
)

var le = binary.LittleEndian

// readManifest reads a compiled AndroidManifest.xml.
func readManifest(b []byte) (Info, error) {
	if len(b) < 8 || le.Uint16(b) != chunkXML {
		return Info{}, invalid("AndroidManifest.xml is not compiled XML")
	}
	var (
		info    Info
		strs    []string
		resIDs  []uint32
		haveApp bool
	)
	off := int(le.Uint16(b[2:]))
	for off+8 <= len(b) {
		typ, size := le.Uint16(b[off:]), int(le.Uint32(b[off+4:]))
		if size < 8 || size > len(b)-off {
			return Info{}, invalid("AndroidManifest.xml: a chunk runs past its end")
		}
		chunk := b[off : off+size]
		switch typ {
		case chunkStringPool:
			if strs != nil {
				break
			}
			var err error
			if strs, err = stringPool(chunk); err != nil {
				return Info{}, err
			}
		case chunkResourceMap:
			hsize := int(le.Uint16(chunk[2:]))
			for i := hsize; i+4 <= len(chunk); i += 4 {
				resIDs = append(resIDs, le.Uint32(chunk[i:]))
			}
		case chunkStartElement:
			name, attrs, err := element(chunk, strs, resIDs)
			if err != nil {
				return Info{}, err
			}
			switch name {
			case "manifest":
				haveApp = true
				info.Package, info.Version, info.Build = attrs["package"], attrs["versionName"], attrs["versionCode"]
			case "uses-sdk":
				info.MinOS = attrs["minSdkVersion"]
			}
		}
		off += size
	}
	switch {
	case !haveApp:
		return Info{}, invalid("AndroidManifest.xml has no <manifest>")
	case info.Package == "":
		return Info{}, invalid("AndroidManifest.xml names no package")
	}
	if err := checkText("AndroidManifest.xml", [][2]string{
		{"package", info.Package}, {"versionName", info.Version}, {"versionCode", info.Build}, {"minSdkVersion", info.MinOS},
	}); err != nil {
		return Info{}, err
	}
	return info, nil
}

// stringPool reads a string pool chunk: UTF-16 or UTF-8 strings. A pool
// stores each string once, so its strings come to fewer bytes than it has
// (their offsets, lengths and terminators leave room for the odd string
// aapt indexes twice); more is offsets pointing at one string over and
// over, a bomb.
func stringPool(c []byte) ([]string, error) {
	if len(c) < 28 {
		return nil, invalid("AndroidManifest.xml: a short string pool")
	}
	hsize, count, flags, start := int(le.Uint16(c[2:])), int(le.Uint32(c[8:])), le.Uint32(c[16:]), int(le.Uint32(c[20:]))
	if count > len(c)/4 || hsize+4*count > len(c) || start > len(c) {
		return nil, invalid("AndroidManifest.xml: a string pool past its chunk")
	}
	isUTF8 := flags&stringPoolUTF8 != 0
	out := make([]string, count)
	budget := len(c)
	for i := range count {
		o := start + int(le.Uint32(c[hsize+4*i:]))
		var (
			s  []byte
			ok bool
		)
		if isUTF8 {
			s, ok = utf8At(c, o)
		} else {
			s, ok = utf16At(c, o)
		}
		if !ok {
			return nil, invalid("AndroidManifest.xml: string %d past its pool", i)
		}
		if budget -= len(s); budget < 0 {
			return nil, invalid("AndroidManifest.xml: a string pool that reads more than it holds")
		}
		if isUTF8 {
			out[i] = string(s)
		} else {
			out[i] = utf16String(s, le)
		}
	}
	return out, nil
}

// utf8At finds a UTF-8 string of a pool: its length in UTF-16 units and
// its length in bytes (each one or two bytes), then the bytes.
func utf8At(c []byte, o int) ([]byte, bool) {
	_, o, ok := poolLength8(c, o)
	if !ok {
		return nil, false
	}
	n, o, ok := poolLength8(c, o)
	if !ok || o+n > len(c) {
		return nil, false
	}
	return c[o : o+n], true
}

func poolLength8(c []byte, o int) (n, next int, ok bool) {
	if o < 0 || o >= len(c) {
		return 0, 0, false
	}
	if c[o]&0x80 == 0 {
		return int(c[o]), o + 1, true
	}
	if o+1 >= len(c) {
		return 0, 0, false
	}
	return int(c[o]&0x7f)<<8 | int(c[o+1]), o + 2, true
}

// utf16At finds a UTF-16 string of a pool: its length in units (one or
// two uint16), then the units.
func utf16At(c []byte, o int) ([]byte, bool) {
	if o < 0 || o+2 > len(c) {
		return nil, false
	}
	n := int(le.Uint16(c[o:]))
	o += 2
	if n&0x8000 != 0 {
		if o+2 > len(c) {
			return nil, false
		}
		n = (n&0x7fff)<<16 | int(le.Uint16(c[o:]))
		o += 2
	}
	if n > (len(c)-o)/2 {
		return nil, false
	}
	return c[o : o+2*n], true
}

// element reads a start element: its name and its attributes' values by
// name (a stripped name by its resource ID).
func element(c []byte, strs []string, resIDs []uint32) (string, map[string]string, error) {
	hsize := int(le.Uint16(c[2:]))
	if hsize+20 > len(c) {
		return "", nil, invalid("AndroidManifest.xml: a short element")
	}
	ext := c[hsize:]
	name := str(strs, le.Uint32(ext[4:]))
	attrStart, attrSize, attrCount := int(le.Uint16(ext[8:])), int(le.Uint16(ext[10:])), int(le.Uint16(ext[12:]))
	if attrSize < 20 || attrStart+attrSize*attrCount > len(ext) {
		return "", nil, invalid("AndroidManifest.xml: attributes past their element")
	}
	attrs := map[string]string{}
	for i := range attrCount {
		a := ext[attrStart+attrSize*i:]
		nameIdx, raw, dataType, data := le.Uint32(a[4:]), le.Uint32(a[8:]), a[15], le.Uint32(a[16:])
		key := str(strs, nameIdx)
		if key == "" && int(nameIdx) < len(resIDs) {
			switch resIDs[nameIdx] {
			case attrVersionCode:
				key = "versionCode"
			case attrVersionName:
				key = "versionName"
			case attrMinSdkVersion:
				key = "minSdkVersion"
			}
		}
		if key == "" {
			continue
		}
		attrs[key] = value(strs, raw, dataType, data)
	}
	return name, attrs, nil
}

// value is an attribute's value: its raw string, else its typed string or
// integer; empty for anything else (a reference to a resource).
func value(strs []string, raw uint32, dataType uint8, data uint32) string {
	if raw != noString {
		return str(strs, raw)
	}
	switch dataType {
	case typeString:
		return str(strs, data)
	case typeIntDec, typeIntHex:
		if data&0x80000000 != 0 { // negative, in two's complement
			return "-" + strconv.FormatUint(uint64(^data)+1, 10)
		}
		return strconv.FormatUint(uint64(data), 10)
	}
	return ""
}

func str(strs []string, i uint32) string {
	if int(i) < len(strs) && i != noString {
		return strs[i]
	}
	return ""
}
