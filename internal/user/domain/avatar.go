package domain

import (
	"net/http"
	"time"

	"github.com/skill/exchange/internal/platform/apperr"
)

// Avatars (design 2026-10-07, avatars and usernames §1.2, §1.3): an
// uploaded image kept as two WebP files, 256 and 64 pixels a side, in the
// avatars directory nginx serves at AvatarURLPrefix; none is the sites'
// default (one of 12, chosen by the user ID).

// AvatarURLPrefix is where nginx serves the avatars directory.
const AvatarURLPrefix = "/uploads/avatars/"

// The limits of an upload.
const (
	MaxAvatarBytes = 5 << 20
	MinAvatarSide  = 64
	// AvatarSide and AvatarThumbSide are the stored files' sides.
	AvatarSide      = 256
	AvatarThumbSide = 64
)

// Avatar describes the uploaded files: their paths under the avatars
// directory (<user_id>/<random>.webp), when, the upload's size in bytes and
// the 256-pixel file's SHA-256.
type Avatar struct {
	Path       string    `json:"path"`
	ThumbPath  string    `json:"thumb_path"`
	UploadedAt time.Time `json:"uploaded_at"`
	Size       int64     `json:"size"`
	SHA256     string    `json:"sha256"`
}

// URL and ThumbURL are the files' paths on the sites; empty without an
// avatar.
func (a *Avatar) URL() string {
	if a == nil {
		return ""
	}
	return AvatarURLPrefix + a.Path
}

// ThumbURL is the 64-pixel file's path on the sites.
func (a *Avatar) ThumbURL() string {
	if a == nil {
		return ""
	}
	return AvatarURLPrefix + a.ThumbPath
}

// Errors of avatars (appendix C).
var (
	ErrAvatarInvalid = apperr.New(apperr.KindInvalid, "USER_AVATAR_INVALID",
		"the avatar must be a PNG, JPEG or WebP image of at least 64 pixels a side")
	// ErrAvatarTooLarge is answered with AvatarTooLargeStatus (the kind
	// has no 413).
	ErrAvatarTooLarge = apperr.New(apperr.KindInvalid, "USER_AVATAR_TOO_LARGE", "the avatar may be at most 5 MB")
)

// AvatarTooLargeStatus is ErrAvatarTooLarge's HTTP status.
const AvatarTooLargeStatus = http.StatusRequestEntityTooLarge
