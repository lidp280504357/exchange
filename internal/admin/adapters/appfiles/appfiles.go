// Package appfiles keeps the apps' files on the server's disk (design
// 2026-10-07, App download page §7 #4): the parts of the uploads in
// progress under Uploads (<upload_id>/<n>.part; nginx does not serve
// them), and the files nginx serves under Downloads - android/<file_id>.apk,
// ios/<file_id>.ipa with its ios/<file_id>.plist, ios/<file_id>.mobileconfig.
// No path comes from what a user typed: upload and file IDs are UUIDs, the
// rest is fixed.
package appfiles

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/skill/exchange/internal/admin/adapters/apppkg"
	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// Disk implements ports.AppFiles in two directories.
type Disk struct {
	// Downloads is what nginx serves as /downloads/ (APP_DOWNLOADS_DIR).
	Downloads string
	// Uploads keeps the parts (APP_UPLOADS_DIR).
	Uploads string
}

var (
	uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	// storedRE is a path under Downloads: a platform's directory and a
	// file named by its ID, or a file being written (.<name>.tmp).
	storedRE = regexp.MustCompile(`^(android|ios)/\.?[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.(apk|ipa|mobileconfig|plist)(\.tmp)?$`)
)

// Directories and files are readable by nginx, which runs as another
// user; nothing is executable.
const (
	dirMode  = 0o755
	fileMode = 0o644
)

// errNoDir is the disk not mounted where it should be.
func errNoDir(dir string, err error) error {
	return apperr.Wrap(err, apperr.KindUnavailable, apperr.CodeUnavailable, "the app files' directory "+dir+" is not available")
}

func (d Disk) uploadDir(id string) (string, error) {
	if !uuidRE.MatchString(id) {
		return "", apperr.Invalid("not an upload ID")
	}
	return filepath.Join(d.Uploads, id), nil
}

// PutPart stores part n of an upload, exactly size bytes of body: written
// aside, then renamed over the part sent before.
func (d Disk) PutPart(_ context.Context, uploadID string, n int, body io.Reader, size int64) error {
	dir, err := d.uploadDir(uploadID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return errNoDir(d.Uploads, err)
	}
	f, err := os.CreateTemp(dir, strconv.Itoa(n)+".part.*")
	if err != nil {
		return errNoDir(d.Uploads, err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	got, err := io.Copy(f, io.LimitReader(body, size+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return apperr.Wrap(err, apperr.KindInvalid, apperr.CodeInvalidArgument, fmt.Sprintf("part %d could not be read", n))
	case got != size:
		return apperr.Invalid(fmt.Sprintf("part %d has %d bytes, not %d", n, got, size))
	}
	return os.Rename(tmp, filepath.Join(dir, strconv.Itoa(n)+".part"))
}

// DropUpload removes an upload's parts.
func (d Disk) DropUpload(uploadID string) error {
	dir, err := d.uploadDir(uploadID)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// platformDir is where a platform's files are under Downloads.
func platformDir(platform string) string { return strings.ToLower(platform) }

// Store joins an upload's parts into a file written aside in its
// platform's directory, checks its size, SHA-256 and package, writes an
// .ipa's manifest.plist, and renames the file into place.
func (d Disk) Store(_ context.Context, u domain.AppUpload, fileID, origin, title string) (ports.StoredAppFile, error) {
	parts, err := d.uploadDir(u.ID)
	if err != nil {
		return ports.StoredAppFile{}, err
	}
	if !uuidRE.MatchString(fileID) {
		return ports.StoredAppFile{}, apperr.Invalid("not a file ID")
	}
	dir := platformDir(u.Platform)
	if err := os.MkdirAll(filepath.Join(d.Downloads, dir), dirMode); err != nil {
		return ports.StoredAppFile{}, errNoDir(d.Downloads, err)
	}
	stored := dir + "/" + fileID + "." + u.Ext()
	final := filepath.Join(d.Downloads, stored)
	tmp := filepath.Join(d.Downloads, dir, "."+fileID+"."+u.Ext()+".tmp")
	defer func() { _ = os.Remove(tmp) }()
	sum, size, err := join(parts, u.Parts(), tmp)
	if err != nil {
		return ports.StoredAppFile{}, err
	}
	switch {
	case size != u.Size:
		return ports.StoredAppFile{}, domain.AppFileInvalid(fmt.Sprintf("%d bytes, not the %d given at the start", size, u.Size))
	case sum != u.SHA256:
		return ports.StoredAppFile{}, domain.AppFileInvalid("its SHA-256 is " + sum + ", not the one given at the start")
	}
	info, err := check(u, tmp, size)
	if err != nil {
		return ports.StoredAppFile{}, err
	}
	out := ports.StoredAppFile{
		FileID: fileID, Kind: u.Kind, Name: u.Name, Size: size, SHA256: sum, StoredAs: stored, Origin: origin, Package: info.Package,
		Version: info.Version, Build: info.Build, MinOS: info.MinOS,
	}
	if u.Ext() == "ipa" {
		out.Manifest = dir + "/" + fileID + ".plist"
		manifest := apppkg.OTAManifest(origin+"/downloads/"+stored, info, title)
		if err := writeFile(filepath.Join(d.Downloads, out.Manifest), manifest); err != nil {
			return ports.StoredAppFile{}, err
		}
	}
	if err := os.Chmod(tmp, fileMode); err != nil {
		return ports.StoredAppFile{}, err
	}
	if err := os.Rename(tmp, final); err != nil {
		if out.Manifest != "" {
			_ = os.Remove(filepath.Join(d.Downloads, out.Manifest))
		}
		return ports.StoredAppFile{}, err
	}
	return out, nil
}

// join writes the parts 1..n of dir into path and returns its SHA-256 and
// size.
func join(dir string, n int, path string) (string, int64, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fileMode) //nolint:gosec // a path of fixed parts and an ID
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	var size int64
	for i := 1; i <= n; i++ {
		got, err := appendPart(io.MultiWriter(f, h), dir, i)
		if err != nil {
			_ = f.Close()
			return "", 0, err
		}
		size += got
	}
	if err := f.Close(); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), size, nil
}

// appendPart copies part n of dir to w; a part missing is the upload's
// refusal naming it, as the part numbers the service names.
func appendPart(w io.Writer, dir string, n int) (int64, error) {
	p, err := os.Open(filepath.Join(dir, strconv.Itoa(n)+".part")) //nolint:gosec // a path of fixed parts and an ID
	if errors.Is(err, fs.ErrNotExist) {
		return 0, domain.ErrAppUploadIncomplete.WithDetail("missing", []int{n})
	}
	if err != nil {
		return 0, err
	}
	defer func() { _ = p.Close() }()
	return io.Copy(w, p)
}

// check reads the joined file as the package its upload claims to be.
func check(u domain.AppUpload, path string, size int64) (apppkg.Info, error) {
	f, err := os.Open(path) //nolint:gosec // a path of fixed parts and an ID
	if err != nil {
		return apppkg.Info{}, err
	}
	defer func() { _ = f.Close() }()
	var info apppkg.Info
	switch u.Ext() {
	case "apk":
		info, err = apppkg.ReadAPK(f, size)
	case "ipa":
		info, err = apppkg.ReadIPA(f, size)
	default:
		var b []byte
		if b, err = io.ReadAll(io.LimitReader(f, domain.AppMaxMobileconfig+1)); err == nil {
			err = apppkg.CheckMobileconfig(b)
		}
	}
	if errors.Is(err, apppkg.ErrInvalid) {
		return apppkg.Info{}, domain.AppFileInvalid(strings.TrimPrefix(err.Error(), apppkg.ErrInvalid.Error()+": "))
	}
	return info, err
}

// writeFile writes data to path aside first, then renames it into place.
func writeFile(path string, data []byte) error {
	// .<name>.tmp: a leftover of a crash is one of the files the sweep
	// lists (review GF, A76 ①), unlike a random temporary name.
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fileMode) //nolint:gosec // a path of fixed parts and an ID
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp) }()
	_, err = io.Copy(f, bytes.NewReader(data))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, fileMode)
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Remove deletes files under Downloads by their paths there; a path that
// is not one of the files' is refused, one gone already is no error.
func (d Disk) Remove(paths ...string) error {
	var errs []error
	for _, p := range paths {
		if p == "" {
			continue
		}
		if !storedRE.MatchString(p) {
			errs = append(errs, fmt.Errorf("not a stored file: %q", p))
			continue
		}
		if err := os.Remove(filepath.Join(d.Downloads, p)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Stored lists the files under Downloads (those being written too).
func (d Disk) Stored() ([]ports.StoredPath, error) {
	var out []ports.StoredPath
	for _, dir := range []string{"android", "ios"} {
		entries, err := os.ReadDir(filepath.Join(d.Downloads, dir))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, errNoDir(d.Downloads, err)
		}
		for _, e := range entries {
			p := dir + "/" + e.Name()
			if !e.Type().IsRegular() || !storedRE.MatchString(p) {
				continue
			}
			fi, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, ports.StoredPath{Path: p, ModTime: fi.ModTime()})
		}
	}
	return out, nil
}

// UploadDirs lists the uploads' directories by their IDs.
func (d Disk) UploadDirs() ([]ports.StoredPath, error) {
	entries, err := os.ReadDir(d.Uploads)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errNoDir(d.Uploads, err)
	}
	var out []ports.StoredPath
	for _, e := range entries {
		if !e.IsDir() || !uuidRE.MatchString(e.Name()) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, ports.StoredPath{Path: e.Name(), ModTime: fi.ModTime()})
	}
	return out, nil
}

// Free is the room left on the downloads' disk for an unprivileged user.
func (d Disk) Free() (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(d.Downloads, &st); err != nil {
		return 0, errNoDir(d.Downloads, err)
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil //nolint:gosec,unconvert // the block size is positive; its type differs by system
}
