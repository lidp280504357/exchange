package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/skill/exchange/internal/platform/apperr"
)

const (
	fileA = "0192a000-0000-7000-8000-00000000000a"
	fileB = "0192a000-0000-7000-8000-00000000000b"
	fileC = "0192a000-0000-7000-8000-00000000000c"
)

func appFile(platform, kind, id string) AppFileInfo {
	f := AppFileInfo{
		FileID: id, Kind: kind, Name: "Astras.apk", Size: 1234, SHA256: strings.Repeat("ab", 32), Origin: "https://astras.vip",
		Package: "vip.astras.app", Version: "1.2.0", Build: "42", MinOS: "24", UploadedAt: time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC),
		UploadedBy: "ops@example.com",
	}
	switch {
	case kind == AppKindMobileconfig:
		f.Name, f.StoredAs, f.Package, f.Version, f.Build, f.MinOS = "trust.mobileconfig", "ios/"+id+".mobileconfig", "", "", "", ""
	case platform == AppIOS:
		f.Name, f.StoredAs, f.Manifest, f.MinOS = "Astras.ipa", "ios/"+id+".ipa", "ios/"+id+".plist", "15.0"
	default:
		f.StoredAs = "android/" + id + ".apk"
	}
	return f
}

func TestAppSettings(t *testing.T) {
	a := PlatformApp{Platform: AppAndroid, Mode: AppOff, Version: 1}
	for name, s := range map[string]AppSetting{
		"an unknown mode":         {Mode: "ON"},
		"a LINK without a link":   {Mode: AppLink},
		"an http link":            {Mode: AppLink, LinkURL: "http://example.com/app"},
		"a link without a host":   {Mode: AppLink, LinkURL: "https:///app"},
		"a link of 501":           {Mode: AppLink, LinkURL: "https://example.com/" + strings.Repeat("a", 481)},
		"FILE without an app":     {Mode: AppFile},
		"notes in another tongue": {Mode: AppOff, Notes: Texts{"fr": "x"}},
		"notes too long":          {Mode: AppOff, Notes: Texts{LocaleZH: strings.Repeat("长", 1001)}},
	} {
		if err := a.Apply(s); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	if err := a.Apply(AppSetting{Mode: AppLink, LinkURL: "https://example.com/" + strings.Repeat("a", 480), Enabled: true}); err != nil {
		t.Fatalf("a link of 500: %v", err)
	}
	if err := a.Apply(AppSetting{
		Mode: AppLink, LinkURL: "https://play.google.com/store/apps/details?id=vip.astras.app", Enabled: true,
		Notes: Texts{LocaleZH: "修复\n若干问题"},
	}); err != nil || a.Mode != AppLink || !a.Enabled || a.Notes[LocaleEN] != "" || a.Notes[LocaleZH] != "修复\n若干问题" {
		t.Fatalf("a link %+v %v", a, err)
	}
	if p := a.Public(""); p == nil || p.Mode != AppLink || p.URL != a.LinkURL || p.IOSInstall != "" || p.File != nil {
		t.Fatalf("a link shown %+v", p)
	}
	a.Enabled = false
	if a.Public("") != nil {
		t.Fatal("a disabled platform shown")
	}
}

func TestAppFiles(t *testing.T) {
	for name, f := range map[string]AppFileInfo{
		"a configuration profile on Android": appFile(AppIOS, AppKindMobileconfig, fileA),
		"stored elsewhere": func() AppFileInfo {
			f := appFile(AppAndroid, AppKindApp, fileA)
			f.StoredAs = "../" + fileA + ".apk"
			return f
		}(),
		"another file's name": func() AppFileInfo {
			f := appFile(AppAndroid, AppKindApp, fileA)
			f.StoredAs = "android/" + fileB + ".apk"
			return f
		}(),
		"an .ipa on Android": func() AppFileInfo {
			f := appFile(AppAndroid, AppKindApp, fileA)
			f.StoredAs = "android/" + fileA + ".ipa"
			return f
		}(),
		"a manifest beside an .apk": func() AppFileInfo {
			f := appFile(AppAndroid, AppKindApp, fileA)
			f.Manifest = "android/" + fileA + ".plist"
			return f
		}(),
		"no hash": func() AppFileInfo { f := appFile(AppAndroid, AppKindApp, fileA); f.SHA256 = "AB"; return f }(),
		"an http origin": func() AppFileInfo {
			f := appFile(AppAndroid, AppKindApp, fileA)
			f.Origin = "http://astras.vip"
			return f
		}(),
		"an origin's path": func() AppFileInfo {
			f := appFile(AppAndroid, AppKindApp, fileA)
			f.Origin = "https://astras.vip/x"
			return f
		}(),
		"no package":     func() AppFileInfo { f := appFile(AppAndroid, AppKindApp, fileA); f.Package = ""; return f }(),
		"a control char": func() AppFileInfo { f := appFile(AppAndroid, AppKindApp, fileA); f.Version = "1.0\x00"; return f }(),
	} {
		a := PlatformApp{Platform: AppAndroid, Mode: AppOff}
		if _, err := a.AddFile(f); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}

	// An app becomes the current one and the mode FILE; enabled stays.
	a := PlatformApp{Platform: AppIOS, Mode: AppLink, LinkURL: "https://apps.apple.com/app/id1", Version: 3}
	if r, err := a.AddFile(appFile(AppIOS, AppKindApp, fileA)); err != nil || r != nil || a.Mode != AppFile || a.Current.FileID != fileA ||
		len(a.Files) != 1 || a.Enabled {
		t.Fatalf("an app %+v %v", a, err)
	}
	if _, err := a.AddFile(appFile(AppIOS, AppKindApp, fileA)); err == nil {
		t.Fatal("a file kept twice")
	}
	// A configuration profile replaces the one before, which leaves the list.
	if r, err := a.AddFile(appFile(AppIOS, AppKindMobileconfig, fileB)); err != nil || r != nil || a.Mobileconfig.FileID != fileB || len(a.Files) != 2 {
		t.Fatalf("a profile %+v %v", a, err)
	}
	if r, err := a.AddFile(appFile(AppIOS, AppKindMobileconfig, fileC)); err != nil || r == nil || r.FileID != fileB || a.Mobileconfig.FileID != fileC ||
		len(a.Files) != 2 || a.Files[0].FileID != fileC || a.Files[1].FileID != fileA {
		t.Fatalf("a profile replaced %+v %+v %v", a, r, err)
	}

	// Shown: the OTA install of the uploaded app, at the profile's domain
	// or else the upload's site.
	a.Enabled = true
	p := a.Public("")
	if p == nil || p.Mode != AppFile || p.IOSInstall != "OTA" || p.URL != "https://astras.vip/downloads/ios/"+fileA+".ipa" ||
		p.InstallURL != "itms-services://?action=download-manifest&url=https%3A%2F%2Fastras.vip%2Fdownloads%2Fios%2F"+fileA+".plist" ||
		p.MobileconfigURL != "https://astras.vip/downloads/ios/"+fileC+".mobileconfig" || p.File.Version != "1.2.0" {
		t.Fatalf("shown %+v", p)
	}
	if p := a.Public("example.com"); p.URL != "https://example.com/downloads/ios/"+fileA+".ipa" {
		t.Fatalf("at the domain %s", p.URL)
	}

	// The current app deleted, the platform goes back to its link.
	if gone, err := a.DeleteFile(fileA); err != nil || gone.FileID != fileA || a.Current != nil || a.Mode != AppLink || len(a.Files) != 1 {
		t.Fatalf("the app deleted %+v %v", a, err)
	}
	if _, err := a.DeleteFile(fileA); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("deleted twice: %v", err)
	}
	if _, err := a.DeleteFile(fileC); err != nil || a.Mobileconfig != nil || len(a.Files) != 0 {
		t.Fatalf("the profile deleted %+v %v", a, err)
	}
	// Without a link, OFF.
	b := PlatformApp{Platform: AppAndroid, Mode: AppOff}
	if _, err := b.AddFile(appFile(AppAndroid, AppKindApp, fileA)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.DeleteFile(fileA); err != nil || b.Mode != AppOff {
		t.Fatalf("no link %+v %v", b, err)
	}

	// At most MaxAppFiles kept.
	c := PlatformApp{Platform: AppAndroid, Mode: AppOff}
	for i := range MaxAppFiles + 1 {
		id := "0192a000-0000-7000-8000-0000000001" + string(rune('a'+i/10)) + string(rune('0'+i%10))
		_, err := c.AddFile(appFile(AppAndroid, AppKindApp, id))
		if i < MaxAppFiles && err != nil {
			t.Fatalf("file %d: %v", i, err)
		}
		if i == MaxAppFiles && !apperr.Is(err, "PLATFORM_APP_FILES_FULL") {
			t.Fatalf("one more: %v", err)
		}
	}
}
