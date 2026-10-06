package apppkg

import (
	"archive/zip"
	"bytes"
	"errors"
	"testing"
)

// Whatever a reader is given, it returns without panicking either what
// the package says of itself or an ErrInvalid.

func FuzzReadManifest(f *testing.F) {
	for _, utf8Pool := range []bool{false, true} {
		f.Add(axml(utf8Pool, manifestOf()))
		f.Add(axml(utf8Pool, strippedManifest()))
		f.Add(poolBomb(utf8Pool, 4, 8))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		info, err := readManifest(b)
		checkRead(t, info, err)
	})
}

func FuzzPlistDict(f *testing.F) {
	for _, b := range [][]byte{
		[]byte(infoPlist), []byte(profile), binaryInfoPlist(), binaryProfile(), dictBomb(false, 3, 5), dictBomb(true, 3, 5),
	} {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		dict, err := plistDict(b)
		if err != nil && !errors.Is(err, ErrInvalid) || (err == nil) != (dict != nil) {
			t.Fatalf("%q, %v", dict, err)
		}
	})
}

func FuzzCheckMobileconfig(f *testing.F) {
	for _, b := range [][]byte{
		[]byte(profile), binaryProfile(), signed(f, []byte(profile)), signed(f, binaryProfile()), signed(f, nil),
		append([]byte{0x30, 0x80, 0x06, 0x09}, profile...),
	} {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if err := CheckMobileconfig(b); err != nil && !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	})
}

// FuzzReadIPA reads archives of two files, names and contents fuzzed: a
// fuzzed archive would rarely get past its checksums to the app in it.
func FuzzReadIPA(f *testing.F) {
	other := `<plist><dict><key>CFBundleIdentifier</key><string>x</string></dict></plist>`
	for _, files := range [][2][2]string{
		{{"Payload/Astras.app/Info.plist", infoPlist}, {"Payload/Astras.app/Frameworks/X.framework/Info.plist", other}},
		{{"Payload/Astras.app/", ""}, {"Payload/Astras.app/Info.plist", string(binaryInfoPlist())}},
		{{"Payload/A.app/Info.plist", infoPlist}, {"Payload/B.app/Info.plist", infoPlist}},
	} {
		f.Add(files[0][0], []byte(files[0][1]), files[1][0], []byte(files[1][1]))
	}
	f.Fuzz(func(t *testing.T, name1 string, content1 []byte, name2 string, content2 []byte) {
		var buf bytes.Buffer
		w := zip.NewWriter(&buf)
		for _, file := range []struct {
			name    string
			content []byte
		}{{name1, content1}, {name2, content2}} {
			fw, err := w.CreateHeader(&zip.FileHeader{Name: file.name, Method: zip.Store})
			if err != nil {
				t.Skip(err)
			}
			if _, err := fw.Write(file.content); err != nil {
				t.Skip(err) // content for a directory
			}
		}
		if err := w.Close(); err != nil {
			t.Skip(err)
		}
		info, err := ReadIPA(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		checkRead(t, info, err)
	})
}

// checkRead fails t unless a reader returned an Info that names its
// package, every value text, or no Info and an ErrInvalid.
func checkRead(t *testing.T, info Info, err error) {
	t.Helper()
	if err != nil {
		if !errors.Is(err, ErrInvalid) || info != (Info{}) {
			t.Fatalf("%+q, %v", info, err)
		}
		return
	}
	if info.Package == "" {
		t.Fatalf("no package: %+q", info)
	}
	for _, s := range []string{info.Package, info.Version, info.Build, info.MinOS, info.Name} {
		if !text(s) {
			t.Fatalf("not text: %+q", info)
		}
	}
}
