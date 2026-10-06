package apppkg

import (
	"bytes"
	"encoding/asn1"
	"encoding/binary"
	"encoding/xml"
	"strconv"
	"strings"
)

// most bounds every size and position read from a binary property list:
// the lists read are megabytes at most.
const most = 1 << 30

// plistDict reads a property list's top dictionary: its keys with a
// string, integer, real or boolean value, as text (the other values left
// out). A binary list begins with "bplist00"; anything else is read as
// XML.
func plistDict(b []byte) (map[string]string, error) {
	if bytes.HasPrefix(b, []byte("bplist00")) {
		return binaryDict(b)
	}
	return xmlDict(b)
}

// xmlDict reads an XML property list: <plist><dict><key>…</key><string>…
func xmlDict(b []byte) (map[string]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.Strict = true
	out := map[string]string{}
	depth, key := 0, ""
	inDict := false
	for {
		tok, err := dec.Token()
		if err != nil {
			if inDict {
				return out, nil
			}
			return nil, invalid("not a property list")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch {
			case depth == 1 && t.Name.Local != "plist":
				return nil, invalid("not a property list (<%s>)", t.Name.Local)
			case depth == 2 && t.Name.Local == "dict":
				inDict = true
			case depth == 2:
				return nil, invalid("the property list is no dictionary (<%s>)", t.Name.Local)
			case depth == 3 && t.Name.Local == "key":
				var k string
				if err := dec.DecodeElement(&k, &t); err != nil {
					return nil, invalid("a key that cannot be read")
				}
				depth--
				key = strings.TrimSpace(k)
			case depth == 3:
				v, ok, err := xmlValue(dec, t)
				if err != nil {
					return nil, err
				}
				depth--
				if ok && key != "" {
					out[key] = v
				}
				key = ""
			}
		case xml.EndElement:
			depth--
			if depth == 1 && inDict {
				return out, nil
			}
		}
	}
}

// xmlValue reads the value of a key at the top: text for a string,
// integer or real, true or false for a boolean; ok false for anything
// else, which is skipped.
func xmlValue(dec *xml.Decoder, t xml.StartElement) (string, bool, error) {
	switch t.Name.Local {
	case "string", "integer", "real":
		var v string
		if err := dec.DecodeElement(&v, &t); err != nil {
			return "", false, invalid("a value that cannot be read")
		}
		return strings.TrimSpace(v), true, nil
	case "true", "false":
		if err := dec.Skip(); err != nil {
			return "", false, invalid("a value that cannot be read")
		}
		return t.Name.Local, true, nil
	}
	if err := dec.Skip(); err != nil {
		return "", false, invalid("a value that cannot be read")
	}
	return "", false, nil
}

var be = binary.BigEndian

// binaryDict reads a binary property list (bplist00): a trailer at the end
// gives the size of offsets and object references, the number of objects,
// the top object and where the offset table is; each object starts with a
// marker byte (type in the high nibble, size in the low one, 0xf: an
// integer object with the size follows).
func binaryDict(b []byte) (map[string]string, error) {
	if len(b) < 8+32 {
		return nil, invalid("a short binary property list")
	}
	t := b[len(b)-32:]
	end := len(b) - 32
	offsetSize, refSize := int(t[6]), int(t[7])
	numObjects, ok1 := bounded(t[8:16])
	top, ok2 := bounded(t[16:24])
	table, ok3 := bounded(t[24:32])
	if !ok1 || !ok2 || !ok3 || offsetSize < 1 || offsetSize > 4 || refSize < 1 || refSize > 4 || numObjects == 0 ||
		numObjects > end || top >= numObjects || table > end || numObjects*offsetSize > end-table {
		return nil, invalid("a binary property list with a bad trailer")
	}
	p := &bplist{b: b[:end], offsetSize: offsetSize, refSize: refSize, numObjects: numObjects, table: table, budget: len(b)}
	o, ok := p.object(top)
	if !ok || b[o]>>4 != 0xd {
		return nil, invalid("the property list is no dictionary")
	}
	n, start, ok := p.count(o)
	if !ok || start+2*n*refSize > len(p.b) {
		return nil, invalid("a dictionary past the property list")
	}
	out := map[string]string{}
	for i := range n {
		k, okK := p.ref(start + i*refSize)
		v, okV := p.ref(start + (n+i)*refSize)
		if !okK || !okV {
			return nil, invalid("a dictionary entry past the property list")
		}
		key, isStr := p.scalar(k)
		if !isStr || key == "" {
			continue
		}
		if val, ok := p.scalar(v); ok {
			out[key] = val
		}
	}
	if p.budget < 0 {
		return nil, invalid("a binary property list that reads more than it holds")
	}
	return out, nil
}

type bplist struct {
	b          []byte
	offsetSize int
	refSize    int
	numObjects int
	table      int
	// budget is what is left of the bytes the strings read may take. A
	// list stores each string once, however often it is referenced: the
	// top dictionary's strings come to fewer bytes than the list has,
	// even with a value shared by a few keys; more is one string
	// referenced over and over, a bomb.
	budget int
}

// bounded reads a big-endian unsigned integer, ok false when it is more
// than most.
func bounded(b []byte) (int, bool) {
	v := 0
	for _, c := range b {
		if v > most>>8 {
			return 0, false
		}
		v = v<<8 | int(c)
	}
	return v, v <= most
}

// uint reads a big-endian unsigned integer of n bytes at o (at most most).
func (p *bplist) uint(o, n int) (int, bool) {
	if o < 0 || n < 1 || n > 8 || o+n > len(p.b) {
		return 0, false
	}
	return bounded(p.b[o : o+n])
}

// object is where object i begins.
func (p *bplist) object(i int) (int, bool) {
	if i < 0 || i >= p.numObjects {
		return 0, false
	}
	o, ok := p.uint(p.table+i*p.offsetSize, p.offsetSize)
	if !ok || o >= p.table {
		return 0, false
	}
	return o, true
}

// ref reads the object reference at o and where that object begins.
func (p *bplist) ref(o int) (int, bool) {
	i, ok := p.uint(o, p.refSize)
	if !ok {
		return 0, false
	}
	return p.object(i)
}

// count is an object's size from its marker, and where its content
// begins.
func (p *bplist) count(o int) (n, start int, ok bool) {
	low := int(p.b[o] & 0x0f)
	if low != 0x0f {
		return low, o + 1, true
	}
	if o+1 >= len(p.b) || p.b[o+1]>>4 != 0x1 {
		return 0, 0, false
	}
	size := 1 << (p.b[o+1] & 0x0f)
	v, ok := p.uint(o+2, size)
	if !ok || v > len(p.b) {
		return 0, 0, false
	}
	return v, o + 2 + size, true
}

// scalar reads a string, an integer, a real or a boolean as text; ok false
// for anything else, and for a string past the budget.
func (p *bplist) scalar(o int) (string, bool) {
	marker := p.b[o]
	switch marker >> 4 {
	case 0x0:
		switch marker {
		case 0x08:
			return "false", true
		case 0x09:
			return "true", true
		}
	case 0x1:
		size := 1 << (marker & 0x0f)
		if size > 8 || o+1+size > len(p.b) {
			break
		}
		if size == 8 { // signed
			var v int64
			if binary.Read(bytes.NewReader(p.b[o+1:o+9]), be, &v) == nil {
				return strconv.FormatInt(v, 10), true
			}
			break
		}
		var v uint64
		for _, c := range p.b[o+1 : o+1+size] {
			v = v<<8 | uint64(c)
		}
		return strconv.FormatUint(v, 10), true
	case 0x5:
		if n, start, ok := p.count(o); ok && start+n <= len(p.b) && p.charge(n) {
			return string(p.b[start : start+n]), true
		}
	case 0x6:
		if n, start, ok := p.count(o); ok && start+2*n <= len(p.b) && p.charge(2*n) {
			return utf16String(p.b[start:start+2*n], be), true
		}
	}
	return "", false
}

// charge takes n bytes from the budget, false when fewer are left.
func (p *bplist) charge(n int) bool {
	p.budget -= n
	return p.budget >= 0
}

// A signed configuration profile is a CMS (PKCS #7) SignedData whose
// encapsulated content is the property list.
var oidSignedData = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}

type contentInfo struct {
	ContentType asn1.ObjectIdentifier
	// Content is the [0] EXPLICIT wrapping the SignedData: encoding/asn1
	// does not unwrap an explicit tag into a RawValue, so its Bytes are
	// the SignedData (its FullBytes the tag and all).
	Content asn1.RawValue `asn1:"explicit,tag:0"`
}

// signedData is a SignedData's first fields: encoding/asn1 leaves the
// certificates and signer infos after them alone.
type signedData struct {
	Version          int
	DigestAlgorithms asn1.RawValue
	EncapContent     struct {
		ContentType asn1.ObjectIdentifier
		Content     []byte `asn1:"explicit,optional,tag:0"`
	}
}

// cmsContent is a signed profile's content. A profile signed in BER
// (indefinite lengths) is read by finding its XML property list.
func cmsContent(b []byte) ([]byte, error) {
	var ci contentInfo
	if _, err := asn1.Unmarshal(b, &ci); err == nil && ci.ContentType.Equal(oidSignedData) {
		var sd signedData
		if _, err := asn1.Unmarshal(ci.Content.Bytes, &sd); err == nil && len(sd.EncapContent.Content) > 0 {
			return sd.EncapContent.Content, nil
		}
	}
	start, end := bytes.Index(b, []byte("<?xml")), bytes.LastIndex(b, []byte("</plist>"))
	if start < 0 || end < start {
		return nil, invalid("a signed profile whose content cannot be found")
	}
	return b[start : end+len("</plist>")], nil
}
