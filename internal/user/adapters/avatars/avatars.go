// Package avatars keeps the users' avatars as files in the directory nginx
// serves at /uploads/avatars/ (design 2026-10-07, avatars and usernames
// §1.2, §1.3): an upload is decoded (its content decides the format, PNG,
// JPEG or WebP), turned upright by its EXIF orientation, cut to its middle
// square and kept as two lossless WebP files, 256 and 64 pixels a side,
// without any metadata; the original is not kept.
package avatars

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // registers the JPEG decoder, a format taken
	_ "image/png"  // registers the PNG decoder, a format taken
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/HugoSmits86/nativewebp"
	"github.com/google/uuid"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registers the WebP decoder, a format taken

	"github.com/skill/exchange/internal/user/domain"
)

// MaxPixels bounds an upload's decoded size (a 5 MB file can describe a
// far larger image): 4096 x 4096, about 64 MB decoded, well within the
// service's memory one upload at a time (Dir serializes them).
const MaxPixels = 4096 * 4096

// Dir is the avatars directory: <dir>/<user_id>/<name>.webp.
type Dir struct {
	Path string
	// sem lets one upload decode at a time.
	sem chan struct{}
}

// New returns the avatars directory at path, made if missing.
func New(path string) (*Dir, error) {
	if path == "" {
		return nil, errors.New("avatars: no directory (AVATAR_DIR)")
	}
	if err := os.MkdirAll(path, 0o755); err != nil { //nolint:gosec // nginx, another user, serves the directory
		return nil, fmt.Errorf("avatars: %w", err)
	}
	return &Dir{Path: path, sem: make(chan struct{}, 1)}, nil
}

// Put processes an upload into the user's two files and returns their
// description; a bad image is domain.ErrAvatarInvalid (with a reason in
// its details), one too large domain.ErrAvatarTooLarge.
func (d *Dir) Put(ctx context.Context, userID string, upload []byte, at time.Time) (domain.Avatar, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return domain.Avatar{}, fmt.Errorf("avatars: user %q", userID)
	}
	if len(upload) > domain.MaxAvatarBytes {
		return domain.Avatar{}, domain.ErrAvatarTooLarge
	}
	select {
	case d.sem <- struct{}{}:
		defer func() { <-d.sem }()
	case <-ctx.Done():
		return domain.Avatar{}, ctx.Err()
	}
	big, thumb, err := Process(upload)
	if err != nil {
		return domain.Avatar{}, err
	}
	name, err := randomName()
	if err != nil {
		return domain.Avatar{}, err
	}
	dir := filepath.Join(d.Path, userID)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // nginx, another user, serves the directory
		return domain.Avatar{}, fmt.Errorf("avatars: %w", err)
	}
	a := domain.Avatar{
		Path: userID + "/" + name + ".webp", ThumbPath: userID + "/" + name + "_64.webp", UploadedAt: at,
		Size: int64(len(upload)),
	}
	sum := sha256.Sum256(big)
	a.SHA256 = hex.EncodeToString(sum[:])
	if err := write(filepath.Join(d.Path, a.Path), big); err != nil {
		return domain.Avatar{}, err
	}
	if err := write(filepath.Join(d.Path, a.ThumbPath), thumb); err != nil {
		_ = os.Remove(filepath.Join(d.Path, a.Path))
		return domain.Avatar{}, err
	}
	return a, nil
}

// pathRE is an avatar file's path under the directory: never anything a
// user wrote.
var pathRE = regexp.MustCompile(`^[0-9a-f-]{36}/[a-z0-9]{16}(_64)?\.webp$`)

// Remove deletes an avatar's files; files already gone are fine.
func (d *Dir) Remove(a domain.Avatar) error {
	var errs []error
	for _, p := range []string{a.Path, a.ThumbPath} {
		if !pathRE.MatchString(p) {
			errs = append(errs, fmt.Errorf("avatars: not an avatar's path: %q", p))
			continue
		}
		if err := os.Remove(filepath.Join(d.Path, p)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// write puts data at path through a temporary file, readable by nginx.
func write(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil { //nolint:gosec // nginx, another user, serves the file
		return fmt.Errorf("avatars: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("avatars: %w", err)
	}
	return nil
}

func randomName() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, 0, 16)
	var b [32]byte
	for len(out) < 16 {
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		for _, c := range b {
			if c < 252 && len(out) < 16 {
				out = append(out, alphabet[int(c)%len(alphabet)])
			}
		}
	}
	return string(out), nil
}

// invalid is domain.ErrAvatarInvalid with why.
func invalid(why string) error { return domain.ErrAvatarInvalid.WithDetail("reason", why) }

// Process turns an upload into the 256 and 64 pixel WebP files.
func Process(upload []byte) (big, thumb []byte, err error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(upload))
	if err != nil {
		return nil, nil, invalid("not a PNG, JPEG or WebP image")
	}
	switch format {
	case "png", "jpeg", "webp":
	default:
		return nil, nil, invalid("not a PNG, JPEG or WebP image")
	}
	if cfg.Width < domain.MinAvatarSide || cfg.Height < domain.MinAvatarSide {
		return nil, nil, invalid("each side must be at least 64 pixels")
	}
	if cfg.Width*cfg.Height > MaxPixels {
		return nil, nil, invalid("at most 4096 x 4096 pixels")
	}
	img, _, err := image.Decode(bytes.NewReader(upload))
	if err != nil {
		return nil, nil, invalid("the image does not decode")
	}
	orientation := 1
	if format == "jpeg" {
		orientation = jpegOrientation(upload)
	}
	// The middle square: the same whichever way the image is turned.
	b := img.Bounds()
	side := min(b.Dx(), b.Dy())
	x0, y0 := b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2
	square := image.Rect(x0, y0, x0+side, y0+side)
	large := image.NewNRGBA(image.Rect(0, 0, domain.AvatarSide, domain.AvatarSide))
	draw.CatmullRom.Scale(large, large.Bounds(), img, square, draw.Src, nil)
	upright := orient(large, orientation)
	small := image.NewNRGBA(image.Rect(0, 0, domain.AvatarThumbSide, domain.AvatarThumbSide))
	draw.CatmullRom.Scale(small, small.Bounds(), upright, upright.Bounds(), draw.Src, nil)
	if big, err = encode(upright); err != nil {
		return nil, nil, err
	}
	if thumb, err = encode(small); err != nil {
		return nil, nil, err
	}
	return big, thumb, nil
}

func encode(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := nativewebp.Encode(&buf, img, nil); err != nil {
		return nil, fmt.Errorf("avatars: encode: %w", err)
	}
	return buf.Bytes(), nil
}
