package appfiles

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/skill/exchange/internal/admin/adapters/apppkg/apppkgtest"
	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

const (
	uploadID = "0192a000-0000-7000-8000-0000000000aa"
	fileID   = "0192a000-0000-7000-8000-0000000000bb"
)

func shaOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// send stores data as an upload's parts of partSize.
func send(t *testing.T, d Disk, u domain.AppUpload, data []byte) {
	t.Helper()
	for n := 1; n <= u.Parts(); n++ {
		part := data[(n-1)*int(u.PartSize) : min(n*int(u.PartSize), len(data))]
		if err := d.PutPart(context.Background(), u.ID, n, bytes.NewReader(part), u.PartLen(n)); err != nil {
			t.Fatalf("part %d: %v", n, err)
		}
	}
}

func upload(platform, kind, name string, data []byte, partSize int64) domain.AppUpload {
	return domain.AppUpload{
		ID: uploadID, Platform: platform, Kind: kind, Name: name, Size: int64(len(data)), SHA256: shaOf(data), PartSize: partSize,
		ExpiresAt: time.Now().Add(time.Hour),
	}
}

func TestParts(t *testing.T) {
	d := Disk{Downloads: t.TempDir(), Uploads: t.TempDir()}
	ctx := context.Background()
	if err := d.PutPart(ctx, "../x", 1, bytes.NewReader(nil), 0); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("not an upload ID: %v", err)
	}
	for name, body := range map[string]string{"short": "abc", "long": "abcdef"} {
		if err := d.PutPart(ctx, uploadID, 1, strings.NewReader(body), 5); !apperr.Is(err, apperr.CodeInvalidArgument) {
			t.Fatalf("a %s part: %v", name, err)
		}
	}
	if err := d.PutPart(ctx, uploadID, 1, strings.NewReader("abcde"), 5); err != nil {
		t.Fatal(err)
	}
	if err := d.PutPart(ctx, uploadID, 1, strings.NewReader("vwxyz"), 5); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(d.Uploads, uploadID, "1.part"))
	entries, _ := os.ReadDir(filepath.Join(d.Uploads, uploadID))
	if err != nil || string(got) != "vwxyz" || len(entries) != 1 {
		t.Fatalf("the part sent again replaces it: %q %v %d", got, err, len(entries))
	}
	if err := d.DropUpload(uploadID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(d.Uploads, uploadID)); !os.IsNotExist(err) {
		t.Fatalf("dropped: %v", err)
	}
}

func TestStore(t *testing.T) {
	d := Disk{Downloads: t.TempDir(), Uploads: t.TempDir()}
	ctx := context.Background()

	// An .apk in parts of 100 bytes: joined, checked, stored as its ID.
	apk := apppkgtest.APK("vip.astras.app", "1.2.0", 42, 24)
	u := upload(domain.AppAndroid, domain.AppKindApp, "Astras.apk", apk, 100)
	send(t, d, u, apk)
	f, err := d.Store(ctx, u, fileID, "https://astras.vip", "Astras")
	if err != nil || f.StoredAs != "android/"+fileID+".apk" || f.Manifest != "" || f.Package != "vip.astras.app" || f.Version != "1.2.0" ||
		f.Build != "42" || f.MinOS != "24" || f.Size != int64(len(apk)) || f.SHA256 != shaOf(apk) || f.Origin != "https://astras.vip" {
		t.Fatalf("stored %+v %v", f, err)
	}
	got, err := os.ReadFile(filepath.Join(d.Downloads, f.StoredAs))
	fi, _ := os.Stat(filepath.Join(d.Downloads, f.StoredAs))
	if err != nil || !bytes.Equal(got, apk) || fi.Mode().Perm() != fileMode {
		t.Fatalf("the file %v %v", err, fi.Mode())
	}
	if list, err := d.Stored(); err != nil || len(list) != 1 || list[0].Path != f.StoredAs {
		t.Fatalf("listed %+v %v", list, err)
	}

	// A part missing, a hash or size that differs, a package that is not
	// one: refused, nothing stored.
	other := upload(domain.AppAndroid, domain.AppKindApp, "x.apk", apk, 100)
	other.ID = "0192a000-0000-7000-8000-0000000000cc"
	if _, err := d.Store(ctx, other, "0192a000-0000-7000-8000-0000000000dd", "https://astras.vip", "Astras"); !apperr.Is(err, "PLATFORM_APP_UPLOAD_INCOMPLETE") ||
		!slices.Equal(missingOf(err), []int{1}) {
		t.Fatalf("no parts: %v", err)
	}
	send(t, d, other, apk)
	other.SHA256 = strings.Repeat("0", 64)
	if _, err := d.Store(ctx, other, "0192a000-0000-7000-8000-0000000000dd", "https://astras.vip", "Astras"); !apperr.Is(err, "PLATFORM_APP_FILE_INVALID") {
		t.Fatalf("another hash: %v", err)
	}
	notZip := []byte("this is not a zip archive at all")
	bad := upload(domain.AppAndroid, domain.AppKindApp, "bad.apk", notZip, 100)
	bad.ID = "0192a000-0000-7000-8000-0000000000ee"
	send(t, d, bad, notZip)
	if _, err := d.Store(ctx, bad, "0192a000-0000-7000-8000-0000000000ff", "https://astras.vip", "Astras"); !apperr.Is(err, "PLATFORM_APP_FILE_INVALID") {
		t.Fatalf("not a zip: %v", err)
	}
	if list, _ := d.Stored(); len(list) != 1 {
		t.Fatalf("a refused file left behind: %+v", list)
	}

	// An .apk of three parts of 1 MiB (the last one shorter), sent out of
	// order: joined in order.
	big := apppkgtest.APKPadded("vip.astras.app", "1.3.0", 43, 24, 2<<20+512)
	bu := upload(domain.AppAndroid, domain.AppKindApp, "Astras-big.apk", big, 1<<20)
	bu.ID = "0192a000-0000-7000-8000-000000000555"
	for _, n := range []int{3, 1, 2} {
		part := big[(n-1)*int(bu.PartSize) : min(n*int(bu.PartSize), len(big))]
		if err := d.PutPart(ctx, bu.ID, n, bytes.NewReader(part), bu.PartLen(n)); err != nil {
			t.Fatalf("part %d: %v", n, err)
		}
	}
	if f, err := d.Store(ctx, bu, "0192a000-0000-7000-8000-000000000666", "https://astras.vip", "Astras"); err != nil || f.Size != int64(len(big)) ||
		f.SHA256 != shaOf(big) || f.Build != "43" || bu.Parts() != 3 {
		t.Fatalf("three parts out of order %+v %v", f, err)
	}

	// An .ipa gets its manifest naming the file at origin; a configuration
	// profile is checked as one.
	ipa := apppkgtest.IPA("vip.astras.app", "2.0", "7", "15.0")
	iu := upload(domain.AppIOS, domain.AppKindApp, "Astras.ipa", ipa, 1<<20)
	iu.ID = "0192a000-0000-7000-8000-000000000111"
	send(t, d, iu, ipa)
	id := "0192a000-0000-7000-8000-000000000222"
	f, err = d.Store(ctx, iu, id, "https://example.com", "Astras")
	if err != nil || f.StoredAs != "ios/"+id+".ipa" || f.Manifest != "ios/"+id+".plist" || f.Version != "2.0" || f.MinOS != "15.0" {
		t.Fatalf("an .ipa %+v %v", f, err)
	}
	manifest, err := os.ReadFile(filepath.Join(d.Downloads, f.Manifest))
	if err != nil || !bytes.Contains(manifest, []byte("<string>https://example.com/downloads/ios/"+id+".ipa</string>")) ||
		!bytes.Contains(manifest, []byte("<string>vip.astras.app</string>")) {
		t.Fatalf("the manifest %s %v", manifest, err)
	}
	mc := apppkgtest.Mobileconfig()
	mu := upload(domain.AppIOS, domain.AppKindMobileconfig, "trust.mobileconfig", mc, 1<<20)
	mu.ID = "0192a000-0000-7000-8000-000000000333"
	send(t, d, mu, mc)
	if f, err := d.Store(ctx, mu, "0192a000-0000-7000-8000-000000000444", "https://astras.vip", "Astras"); err != nil ||
		f.StoredAs != "ios/0192a000-0000-7000-8000-000000000444.mobileconfig" || f.Package != "" {
		t.Fatalf("a profile %+v %v", f, err)
	}

	// Remove: only the files' own paths; one gone already is no error.
	if err := d.Remove("../../etc/passwd"); err == nil {
		t.Fatal("a path outside the files removed")
	}
	if err := d.Remove(f.StoredAs, f.Manifest, f.StoredAs); err != nil {
		t.Fatal(err)
	}
	if list, _ := d.Stored(); len(list) != 3 {
		t.Fatalf("after removing the .ipa: %+v", list)
	}
	if free, err := d.Free(); err != nil || free == 0 {
		t.Fatalf("free %d %v", free, err)
	}
	// A manifest a crash left half written is one of the files the sweep
	// sees (review GF, A76 ①).
	left := filepath.Join(d.Downloads, "ios", ".0192a000-0000-7000-8000-000000000777.plist.tmp")
	if err := os.WriteFile(left, []byte("<plist"), 0o600); err != nil {
		t.Fatal(err)
	}
	if list, _ := d.Stored(); !slices.ContainsFunc(list, func(p ports.StoredPath) bool { return p.Path == "ios/.0192a000-0000-7000-8000-000000000777.plist.tmp" }) {
		t.Fatalf("a manifest left over not listed: %+v", list)
	}
	if err := d.Remove("ios/.0192a000-0000-7000-8000-000000000777.plist.tmp"); err != nil {
		t.Fatal(err)
	}
	dirs, err := d.UploadDirs()
	if err != nil || len(dirs) != 6 || !slices.ContainsFunc(dirs, func(p ports.StoredPath) bool { return p.Path == uploadID }) {
		t.Fatalf("the uploads' directories %+v %v", dirs, err)
	}
}

// missingOf is an incomplete upload's missing parts.
func missingOf(err error) []int {
	var e *apperr.Error
	if !errors.As(err, &e) {
		return nil
	}
	m, _ := e.Details["missing"].([]int)
	return m
}
