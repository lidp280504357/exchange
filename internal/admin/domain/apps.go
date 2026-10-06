package domain

import (
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/skill/exchange/internal/platform/apperr"
)

// The apps to download (design 2026-10-07, App download page): the console
// uploads Android's .apk, iOS's .ipa and its optional .mobileconfig in
// parts (Cloudflare takes at most 100 MB a request, §1.2 #5), checks them
// and stores them for nginx to serve; instrument-service keeps each
// platform's settings and files.

// The platforms and the kinds of file.
const (
	AppAndroid          = "ANDROID"
	AppIOS              = "IOS"
	AppKindApp          = "APP"
	AppKindMobileconfig = "MOBILECONFIG"
)

const (
	// AppPartSize is a part of an upload but the last (10 MiB).
	AppPartSize = 10 << 20
	// AppMaxSize bounds an app (500 MiB).
	AppMaxSize = 524288000
	// AppMaxMobileconfig bounds a configuration profile (1 MiB).
	AppMaxMobileconfig = 1 << 20
	// AppUploadTTL is how long an upload waits for its parts and its
	// completion before it is dropped with them.
	AppUploadTTL = 24 * time.Hour
	// AppUploadsOpen bounds the uploads in progress (each up to
	// AppMaxSize on the server's disk).
	AppUploadsOpen = 3
	// AppCompleteHold is how long a completion holds an upload against
	// another (joining and checking 500 MiB takes seconds).
	AppCompleteHold = 5 * time.Minute
)

var (
	appNameRE   = regexp.MustCompile(`\.(apk|ipa|mobileconfig)$`)
	appSHA256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Errors of the uploads (api/admin/admin.yaml; 需求文档 附录 C PLATFORM_).
var (
	// ErrAppUploadIncomplete refuses to complete an upload with a part
	// missing (details missing).
	ErrAppUploadIncomplete = apperr.New(apperr.KindConflict, "PLATFORM_APP_UPLOAD_INCOMPLETE", "a part of the upload is missing")
	// ErrAppUploadsFull refuses an upload while AppUploadsOpen are open.
	ErrAppUploadsFull = apperr.New(apperr.KindConflict, "PLATFORM_APP_UPLOADS_FULL",
		fmt.Sprintf("%d uploads are open: complete or drop one first", AppUploadsOpen))
	// ErrAppUploadBusy refuses a change of an upload another request is
	// completing.
	ErrAppUploadBusy = apperr.New(apperr.KindConflict, "PLATFORM_APP_UPLOAD_BUSY", "the upload is being completed")
	// ErrAppDiskFull refuses an upload the server's disk has no room for.
	ErrAppDiskFull = apperr.New(apperr.KindConflict, "PLATFORM_APP_DISK_FULL", "the server's disk has no room for the file")
)

// AppFileInvalid refuses a file that is not what it claims (details reason).
func AppFileInvalid(reason string) error {
	return apperr.New(apperr.KindUnprocessable, "PLATFORM_APP_FILE_INVALID", "the file is not a valid "+
		"app or configuration profile: "+reason).WithDetail("reason", reason)
}

// AppUpload is an upload in progress: what it is, and the parts that came
// (in any order; a part sent again replaces it).
type AppUpload struct {
	ID        string
	Platform  string
	Kind      string
	Name      string
	Size      int64
	SHA256    string
	PartSize  int64
	Received  []int
	StartedBy string
	StartedAt time.Time
	ExpiresAt time.Time
}

// NewAppUpload checks an upload about to start: the kind on its platform,
// the name's extension, the size and the SHA-256.
func NewAppUpload(id, platform, kind, name string, size int64, sha256, by string, now time.Time) (AppUpload, error) {
	u := AppUpload{
		ID: id, Platform: platform, Kind: kind, Name: name, Size: size, SHA256: sha256, PartSize: AppPartSize, Received: []int{}, StartedBy: by,
		StartedAt: now, ExpiresAt: now.Add(AppUploadTTL),
	}
	most := int64(AppMaxSize)
	kindOK := kind == AppKindApp || (kind == AppKindMobileconfig && platform == AppIOS)
	switch {
	case platform != AppAndroid && platform != AppIOS:
		return AppUpload{}, apperr.NotFound("no such platform: ANDROID or IOS")
	case !kindOK:
		return AppUpload{}, apperr.Invalid("kind: APP, or MOBILECONFIG on IOS")
	case kind == AppKindMobileconfig:
		most = AppMaxMobileconfig
	}
	switch {
	case len(name) > 200 || !appNameRE.MatchString(name) || appNameRE.FindStringSubmatch(name)[1] != u.Ext():
		return AppUpload{}, apperr.Invalid("name: at most 200 characters, ending ." + u.Ext())
	case size < 1 || size > most:
		return AppUpload{}, apperr.Invalid(fmt.Sprintf("size: 1 to %d bytes", most))
	case !appSHA256RE.MatchString(sha256):
		return AppUpload{}, apperr.Invalid("sha256: 64 lowercase hex digits")
	}
	return u, nil
}

// Ext is the extension the upload's platform and kind take.
func (u AppUpload) Ext() string {
	switch {
	case u.Kind == AppKindMobileconfig:
		return "mobileconfig"
	case u.Platform == AppIOS:
		return "ipa"
	}
	return "apk"
}

// Parts is how many parts the upload takes.
func (u AppUpload) Parts() int {
	return int((u.Size + u.PartSize - 1) / u.PartSize)
}

// PartLen is how many bytes part n has, 0 for a part out of range.
func (u AppUpload) PartLen(n int) int64 {
	switch {
	case n < 1 || n > u.Parts():
		return 0
	case n < u.Parts():
		return u.PartSize
	}
	return u.Size - int64(u.Parts()-1)*u.PartSize
}

// Missing are the parts that have not come.
func (u AppUpload) Missing() []int {
	var out []int
	for n := 1; n <= u.Parts(); n++ {
		if !slices.Contains(u.Received, n) {
			out = append(out, n)
		}
	}
	return out
}
