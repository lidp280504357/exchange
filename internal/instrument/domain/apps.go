package domain

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/skill/exchange/internal/platform/apperr"
)

// The apps to download (design 2026-10-07, App download page): Android's
// and iOS's, each off, a link (an app store, TestFlight, another page) or
// an app uploaded in the console, shown by the sites while enabled. The
// files are on the server's disk - admin-service writes them, nginx serves
// /downloads/ on the three sites - and a platform keeps what was read of
// them.

// The platforms, Android first as the console and the sites list them.
const (
	AppAndroid = "ANDROID"
	AppIOS     = "IOS"
)

// AppPlatforms are the platforms in order.
var AppPlatforms = []string{AppAndroid, AppIOS}

// A platform's mode.
const (
	AppOff  = "OFF"
	AppLink = "LINK"
	AppFile = "FILE"
)

// The kinds of an uploaded file: an app (.apk, .ipa) or iOS's optional
// configuration profile (.mobileconfig).
const (
	AppKindApp          = "APP"
	AppKindMobileconfig = "MOBILECONFIG"
)

const (
	// maxAppNotes bounds a language's version notes.
	maxAppNotes = 1000
	// MaxAppFiles bounds the files kept for a platform: each is up to
	// 500 MiB on the server's disk, kept until deleted (§1.2 #4).
	MaxAppFiles = 10
	// maxAppText bounds a file's name and what was read of it.
	maxAppText = 200
	// maxAppLink is the link's limit (as the contract's link_url).
	maxAppLink = 500
)

var (
	sha256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)
	uuidRE   = `[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`
	// storedRE is where a file is under /downloads/: <platform>/<file_id>.<extension>.
	storedRE = regexp.MustCompile(`^(android|ios)/(` + uuidRE + `)\.(apk|ipa|mobileconfig|plist)$`)
)

// ErrNoSuchApp refuses a platform that is neither Android nor iOS.
var ErrNoSuchApp = apperr.NotFound("no such platform: ANDROID or IOS")

// ErrAppFiles refuses a file beyond MaxAppFiles.
var ErrAppFiles = apperr.New(apperr.KindConflict, "PLATFORM_APP_FILES_FULL",
	fmt.Sprintf("a platform keeps at most %d files: delete one first", MaxAppFiles))

// AppFileInfo is a file uploaded in the console as a platform keeps it:
// where it is under /downloads/, the site its manifest names, and what
// was read of it.
type AppFileInfo struct {
	FileID string
	Kind   string
	// Name is the file's name when uploaded.
	Name   string
	Size   int64
	SHA256 string
	// StoredAs is its path under /downloads/ (android/<file_id>.apk);
	// Manifest an .ipa's manifest.plist beside it, empty otherwise.
	StoredAs string
	Manifest string
	// Origin is the site the upload named (https://<host>): an .ipa's
	// manifest points there, and the files are served there while the
	// profile has no domain.
	Origin string
	// Package is the Android package or the iOS bundle identifier;
	// Version, Build and MinOS what the package says (empty when not).
	Package    string
	Version    string
	Build      string
	MinOS      string
	UploadedAt time.Time
	UploadedBy string
}

// PlatformApp is one platform's download as the console set it.
type PlatformApp struct {
	Platform string
	Mode     string
	// LinkURL is an https link, kept while the mode is FILE or OFF.
	LinkURL string
	Enabled bool
	Notes   Texts
	// Current is the app FILE serves; Mobileconfig iOS's configuration
	// profile; Files every file kept, newest first (those two among them).
	Current      *AppFileInfo
	Mobileconfig *AppFileInfo
	Files        []AppFileInfo
	Version      int64
	UpdatedBy    string
	UpdatedAt    time.Time
}

// AppSetting is what the console sets of a platform: all but its files.
type AppSetting struct {
	Mode    string
	LinkURL string
	Notes   Texts
	Enabled bool
}

// ValidAppPlatform reports whether p is ANDROID or IOS.
func ValidAppPlatform(p string) bool { return slices.Contains(AppPlatforms, p) }

// Normalize gives the notes their three languages and the files an empty
// list, as the API shows them.
func (a *PlatformApp) Normalize() {
	a.Notes = a.Notes.normalized()
	if a.Files == nil {
		a.Files = []AppFileInfo{}
	}
}

// Apply sets s on the platform: a LINK needs an https link, FILE an app
// uploaded.
func (a *PlatformApp) Apply(s AppSetting) error {
	switch s.Mode {
	case AppOff, AppLink, AppFile:
	default:
		return apperr.Invalid("mode: OFF, LINK or FILE")
	}
	if s.LinkURL != "" {
		u, err := url.Parse(s.LinkURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || len(s.LinkURL) > maxAppLink || !printable(s.LinkURL) {
			return apperr.Invalid(fmt.Sprintf("link_url: an https URL of at most %d characters, or empty", maxAppLink))
		}
	}
	switch {
	case s.Mode == AppLink && s.LinkURL == "":
		return apperr.Invalid("a LINK needs link_url")
	case s.Mode == AppFile && a.Current == nil:
		return apperr.Invalid("FILE needs an app uploaded: upload one, or choose LINK or OFF")
	}
	if err := validTexts("notes", s.Notes, maxAppNotes, false); err != nil {
		return err
	}
	a.Mode, a.LinkURL, a.Notes, a.Enabled = s.Mode, s.LinkURL, s.Notes.normalized(), s.Enabled
	return nil
}

// Validate checks a file admin-service stored for platform p: its kind,
// where it is, its size and hash, the site and what was read of it.
func (f AppFileInfo) Validate(platform string) error {
	ext := map[string]string{AppAndroid: "apk", AppIOS: "ipa"}[platform]
	if f.Kind == AppKindMobileconfig {
		ext = "mobileconfig"
	}
	dir := strings.ToLower(platform)
	m := storedRE.FindStringSubmatch(f.StoredAs)
	kindOK := f.Kind == AppKindApp || (f.Kind == AppKindMobileconfig && platform == AppIOS)
	ipa := platform == AppIOS && f.Kind == AppKindApp
	switch {
	case !kindOK:
		return apperr.Invalid("kind: APP, or MOBILECONFIG on IOS")
	case m == nil || m[1] != dir || m[2] != f.FileID || m[3] != ext:
		return apperr.Invalid(fmt.Sprintf("stored_as: %s/<file_id>.%s", dir, ext))
	case ipa && f.Manifest != dir+"/"+f.FileID+".plist":
		return apperr.Invalid("manifest: ios/<file_id>.plist beside an .ipa")
	case !ipa && f.Manifest != "":
		return apperr.Invalid("manifest: only beside an .ipa")
	case f.Size <= 0 || !sha256RE.MatchString(f.SHA256):
		return apperr.Invalid("size and sha256 (lowercase hex) are required")
	case !originOK(f.Origin):
		return apperr.Invalid("origin: https://<host>")
	case f.Kind == AppKindApp && f.Package == "":
		return apperr.Invalid("an app's package is required")
	case strings.TrimSpace(f.UploadedBy) == "" || f.UploadedAt.IsZero():
		return apperr.Invalid("uploaded_by and uploaded_at are required")
	}
	for field, s := range map[string]string{"name": f.Name, "package": f.Package, "version": f.Version, "build": f.Build, "min_os": f.MinOS} {
		if utf8.RuneCountInString(s) > maxAppText || !utf8.ValidString(s) || !printable(s) {
			return apperr.Invalid(fmt.Sprintf("%s: at most %d printable characters", field, maxAppText))
		}
	}
	if f.Name == "" {
		return apperr.Invalid("name is required")
	}
	return nil
}

// originOK reports whether s is https://<host> and nothing more.
func originOK(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.Path == "" && u.RawQuery == "" && u.User == nil && len(s) <= maxAppLink
}

// AddFile keeps f, newest first: an app becomes the current one and the
// mode FILE (enabled stays as it was); a configuration profile replaces
// iOS's, and the one it replaced (nil for none) leaves the list - its file
// is for the caller to delete. Refused beyond MaxAppFiles, or for a file
// kept already.
func (a *PlatformApp) AddFile(f AppFileInfo) (*AppFileInfo, error) {
	if err := f.Validate(a.Platform); err != nil {
		return nil, err
	}
	for _, o := range a.Files {
		if o.FileID == f.FileID {
			return nil, apperr.New(apperr.KindConflict, apperr.CodeConflict, "file "+f.FileID+" is kept already")
		}
	}
	var replaced *AppFileInfo
	files := a.Files
	if f.Kind == AppKindMobileconfig && a.Mobileconfig != nil {
		old := *a.Mobileconfig
		replaced = &old
		files = slices.DeleteFunc(slices.Clone(files), func(o AppFileInfo) bool { return o.FileID == old.FileID })
	}
	if len(files) >= MaxAppFiles {
		return nil, ErrAppFiles
	}
	a.Files = append([]AppFileInfo{f}, files...)
	if f.Kind == AppKindMobileconfig {
		a.Mobileconfig = &f
	} else {
		a.Current, a.Mode = &f, AppFile
	}
	return replaced, nil
}

// DeleteFile drops the file fileID from the list and returns it: the
// current app deleted, the platform goes back to its link when it has
// one, else OFF; the configuration profile deleted, it has none.
func (a *PlatformApp) DeleteFile(fileID string) (AppFileInfo, error) {
	i := slices.IndexFunc(a.Files, func(f AppFileInfo) bool { return f.FileID == fileID })
	if i < 0 {
		return AppFileInfo{}, apperr.NotFound("no such file")
	}
	gone := a.Files[i]
	a.Files = slices.Delete(slices.Clone(a.Files), i, i+1)
	if a.Current != nil && a.Current.FileID == fileID {
		a.Current = nil
		if a.Mode == AppFile {
			a.Mode = AppOff
			if a.LinkURL != "" {
				a.Mode = AppLink
			}
		}
	}
	if a.Mobileconfig != nil && a.Mobileconfig.FileID == fileID {
		a.Mobileconfig = nil
	}
	return gone, nil
}

// AppDownload is what the sites show of a platform (api/openapi/
// platform.yaml's AppDownload): a link, or the uploaded app with what was
// read of it, and iOS's configuration profile.
type AppDownload struct {
	Mode string
	URL  string
	// InstallURL is an uploaded iOS app's over-the-air install link.
	InstallURL string
	// IOSInstall is APP_STORE for a link, OTA for an uploaded app, empty
	// on Android.
	IOSInstall      string
	File            *AppFileInfo
	MobileconfigURL string
	Notes           Texts
	UpdatedAt       time.Time
}

// FileURL is where the sites serve a stored path: under the profile's
// domain when it has one, else the site the upload named.
func FileURL(domain, origin, stored string) string {
	if stored == "" {
		return ""
	}
	if domain != "" {
		origin = "https://" + domain
	}
	return origin + "/downloads/" + stored
}

// Public is what the sites show of the platform, nil when nothing: off,
// disabled, a link missing or an app gone. domain is the profile's.
func (a PlatformApp) Public(domain string) *AppDownload {
	if !a.Enabled {
		return nil
	}
	out := &AppDownload{Mode: a.Mode, Notes: a.Notes.normalized(), UpdatedAt: a.UpdatedAt}
	switch {
	case a.Mode == AppLink && a.LinkURL != "":
		out.URL = a.LinkURL
		if a.Platform == AppIOS {
			out.IOSInstall = "APP_STORE"
		}
	case a.Mode == AppFile && a.Current != nil:
		f := *a.Current
		out.URL, out.File = FileURL(domain, f.Origin, f.StoredAs), &f
		if a.Platform == AppIOS {
			out.IOSInstall = "OTA"
			out.InstallURL = "itms-services://?action=download-manifest&url=" + url.QueryEscape(FileURL(domain, f.Origin, f.Manifest))
		}
	default:
		return nil
	}
	if a.Mobileconfig != nil {
		out.MobileconfigURL = FileURL(domain, a.Mobileconfig.Origin, a.Mobileconfig.StoredAs)
	}
	return out
}
