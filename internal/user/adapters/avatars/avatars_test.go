package avatars

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/image/webp"

	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/user/domain"
)

var (
	red   = color.NRGBA{R: 255, A: 255}
	blue  = color.NRGBA{B: 255, A: 255}
	green = color.NRGBA{G: 255, A: 255}
)

// paint is a w x h image colored by where each pixel is.
func paint(w, h int, at func(x, y int) color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, at(x, y))
		}
	}
	return img
}

func pngOf(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// withOrientation is a JPEG of img with an EXIF segment saying how to turn
// it (and a camera make, metadata that must not be kept).
func withOrientation(t *testing.T, img image.Image, orientation uint16) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	// TIFF, big-endian: two IFD entries, Make (ASCII, 4 bytes inline) and
	// Orientation (SHORT).
	tiff := []byte("MM\x00\x2a\x00\x00\x00\x08")
	tiff = binary.BigEndian.AppendUint16(tiff, 2)
	tiff = append(tiff, 0x01, 0x0F, 0x00, 0x02, 0, 0, 0, 4, 'A', 'C', 'M', 0)
	tiff = append(tiff, 0x01, 0x12, 0x00, 0x03, 0, 0, 0, 1)
	tiff = binary.BigEndian.AppendUint16(tiff, orientation)
	tiff = append(tiff, 0, 0, 0, 0, 0, 0, 0, 0)
	seg := append([]byte("Exif\x00\x00"), tiff...)
	app1 := []byte{0xFF, 0xE1}
	app1 = binary.BigEndian.AppendUint16(app1, uint16(len(seg)+2)) //nolint:gosec // a few dozen bytes
	app1 = append(app1, seg...)
	out := append([]byte{}, buf.Bytes()[:2]...)
	out = append(out, app1...)
	return append(out, buf.Bytes()[2:]...)
}

func decodeWebP(t *testing.T, b []byte) image.Image {
	t.Helper()
	img, err := webp.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func near(c color.Color, want color.NRGBA) bool {
	r, g, b, _ := c.RGBA()
	d := func(a uint32, w uint8) bool {
		return a>>8 >= uint32(w)-40 && a>>8 <= uint32(w)+40 || (w == 0 && a>>8 <= 40)
	}
	return d(r, want.R) && d(g, want.G) && d(b, want.B)
}

// The middle square of a wide image, scaled to 256 and 64 pixels.
func TestTheMiddleSquareIsKept(t *testing.T) {
	img := paint(300, 200, func(x, _ int) color.NRGBA {
		switch {
		case x < 50:
			return red
		case x >= 250:
			return green
		}
		return blue
	})
	big, thumb, err := Process(pngOf(t, img))
	if err != nil {
		t.Fatal(err)
	}
	b, s := decodeWebP(t, big), decodeWebP(t, thumb)
	if b.Bounds() != image.Rect(0, 0, 256, 256) || s.Bounds() != image.Rect(0, 0, 64, 64) {
		t.Fatalf("sizes %v %v", b.Bounds(), s.Bounds())
	}
	for _, p := range []image.Point{{1, 1}, {128, 128}, {254, 254}, {1, 254}} {
		if !near(b.At(p.X, p.Y), blue) {
			t.Fatalf("at %v: %v, not the middle's blue", p, b.At(p.X, p.Y))
		}
	}
	if !near(s.At(32, 32), blue) {
		t.Fatalf("thumbnail %v", s.At(32, 32))
	}
}

// A photo turned by its EXIF orientation comes out upright, without the
// metadata.
func TestAPhotoIsTurnedUpright(t *testing.T) {
	// Stored with its top red and its bottom blue: orientation 6 says to
	// turn it a quarter clockwise, so the red goes right.
	img := paint(120, 120, func(_, y int) color.NRGBA {
		if y < 60 {
			return red
		}
		return blue
	})
	upload := withOrientation(t, img, 6)
	if got := jpegOrientation(upload); got != 6 {
		t.Fatalf("orientation %d", got)
	}
	big, _, err := Process(upload)
	if err != nil {
		t.Fatal(err)
	}
	out := decodeWebP(t, big)
	if !near(out.At(230, 128), red) || !near(out.At(25, 128), blue) {
		t.Fatalf("right %v, left %v: not turned", out.At(230, 128), out.At(25, 128))
	}
	if bytes.Contains(big, []byte("Exif")) || bytes.Contains(big, []byte("ACM")) {
		t.Fatal("metadata kept")
	}
	for o := uint16(1); o <= 8; o++ {
		if got := jpegOrientation(withOrientation(t, img, o)); got != int(o) {
			t.Fatalf("orientation %d read as %d", o, got)
		}
	}
	// The eight turns of a square: 6 then 8 is as it was.
	sq := paint(4, 4, func(x, y int) color.NRGBA {
		return color.NRGBA{R: uint8(x * 60), G: uint8(y * 60), A: 255} //nolint:gosec // at most 180
	})
	if back := orient(orient(sq, 6), 8); !bytes.Equal(back.Pix, sq.Pix) {
		t.Fatal("6 then 8 is not the identity")
	}
	if back := orient(orient(sq, 3), 3); !bytes.Equal(back.Pix, sq.Pix) {
		t.Fatal("3 twice is not the identity")
	}
}

// pngHeader is a PNG's signature and header claiming w x h, nothing more.
func pngHeader(w, h uint32) []byte {
	ihdr := binary.BigEndian.AppendUint32(nil, w)
	ihdr = binary.BigEndian.AppendUint32(ihdr, h)
	ihdr = append(ihdr, 8, 6, 0, 0, 0)
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(ihdr))) //nolint:gosec // 13 bytes
	chunk = append(chunk, "IHDR"...)
	chunk = append(chunk, ihdr...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
	return append([]byte("\x89PNG\r\n\x1a\n"), chunk...)
}

// Anything but a decodable PNG, JPEG or WebP of 64 to 4096 pixels a side
// is refused, its reason in the details.
func TestABadUploadIsRefused(t *testing.T) {
	for name, upload := range map[string][]byte{
		"svg":         []byte(`<svg xmlns="http://www.w3.org/2000/svg"><circle r="10"/></svg>`),
		"gif":         []byte("GIF89a\x40\x00\x40\x00\x00\x00\x00;"),
		"text":        []byte("hello"),
		"too small":   pngOf(t, paint(63, 200, func(int, int) color.NRGBA { return red })),
		"too many":    pngHeader(4097, 4097),
		"truncated":   pngOf(t, paint(100, 100, func(int, int) color.NRGBA { return red }))[:60],
		"header only": pngHeader(100, 100),
	} {
		if _, _, err := Process(upload); apperr.From(err).Code != "USER_AVATAR_INVALID" {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Put stores both files under the user's directory, readable by nginx;
// Remove deletes them and touches nothing else.
func TestPutAndRemove(t *testing.T) {
	dir, err := New(filepath.Join(t.TempDir(), "avatars"))
	if err != nil {
		t.Fatal(err)
	}
	user := "0192f0c4-8a3e-7b2d-9c1f-3e5a7d9b1c2e"
	at := time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC)
	upload := pngOf(t, paint(100, 100, func(int, int) color.NRGBA { return green }))
	a, err := dir.Put(context.Background(), user, upload, at)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(a.Path, user+"/") || !strings.HasSuffix(a.ThumbPath, "_64.webp") || a.Size != int64(len(upload)) ||
		len(a.SHA256) != 64 || !a.UploadedAt.Equal(at) || a.URL() != "/uploads/avatars/"+a.Path {
		t.Fatalf("avatar %+v", a)
	}
	for _, p := range []string{a.Path, a.ThumbPath} {
		fi, err := os.Stat(filepath.Join(dir.Path, p))
		if err != nil || fi.Mode().Perm() != 0o644 {
			t.Fatalf("%s: %v %v", p, fi, err)
		}
	}
	if _, err := dir.Put(context.Background(), user, make([]byte, domain.MaxAvatarBytes+1), at); !errors.Is(err, domain.ErrAvatarTooLarge) {
		t.Fatalf("too large: %v", err)
	}
	if err := dir.Remove(domain.Avatar{Path: "../etc/passwd", ThumbPath: user + "/x.webp"}); err == nil {
		t.Fatal("removed a path outside the avatars")
	}
	if err := dir.Remove(a); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir.Path, a.Path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("still there: %v", err)
	}
	if err := dir.Remove(a); err != nil {
		t.Fatalf("removing again: %v", err)
	}
}
