package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// fakeApps keeps the platforms as instrument-service does: settings on
// the version read, files kept newest first, an app making FILE, a
// configuration profile replacing the one before.
type fakeApps struct {
	apps map[string]*fakeApp
	// down fails every call; lose keeps a file but loses the answer.
	down, lose bool
}

type fakeApp struct {
	Platform     string                `json:"platform"`
	Mode         string                `json:"mode"`
	LinkURL      string                `json:"link_url"`
	Enabled      bool                  `json:"enabled"`
	Notes        map[string]string     `json:"notes"`
	Current      *ports.StoredAppFile  `json:"current"`
	Mobileconfig *ports.StoredAppFile  `json:"mobileconfig"`
	Files        []ports.StoredAppFile `json:"files"`
	Version      int64                 `json:"version"`
	UpdatedBy    string                `json:"updated_by"`
}

func newFakeApps() *fakeApps {
	f := &fakeApps{apps: map[string]*fakeApp{}}
	for _, p := range []string{domain.AppAndroid, domain.AppIOS} {
		f.apps[p] = &fakeApp{Platform: p, Mode: "OFF", Notes: map[string]string{}, Files: []ports.StoredAppFile{}, Version: 1}
	}
	return f
}

var errInstrumentDown = apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "instrument-service is down")

func (f *fakeApps) Apps(context.Context) (json.RawMessage, error) {
	if f.down {
		return nil, errInstrumentDown
	}
	return json.Marshal(map[string]any{"apps": []*fakeApp{f.apps[domain.AppAndroid], f.apps[domain.AppIOS]}})
}

func (f *fakeApps) answer(a *fakeApp, key string, file *ports.StoredAppFile) (json.RawMessage, error) {
	out := map[string]any{"app": a}
	if key != "" {
		out[key] = file
	}
	return json.Marshal(out)
}

func (f *fakeApps) SetApp(_ context.Context, platform string, write json.RawMessage, actor, _ string) (json.RawMessage, error) {
	if f.down {
		return nil, errInstrumentDown
	}
	var w struct {
		Mode            string            `json:"mode"`
		LinkURL         string            `json:"link_url"`
		Notes           map[string]string `json:"notes"`
		Enabled         bool              `json:"enabled"`
		ExpectedVersion int64             `json:"expected_version"`
	}
	if err := json.Unmarshal(write, &w); err != nil {
		return nil, err
	}
	a := f.apps[platform]
	if w.ExpectedVersion != a.Version {
		return nil, apperr.New(apperr.KindConflict, "INSTRUMENT_PLATFORM_CHANGED", "changed")
	}
	a.Mode, a.LinkURL, a.Notes, a.Enabled, a.UpdatedBy = w.Mode, w.LinkURL, w.Notes, w.Enabled, actor
	a.Version++
	return f.answer(a, "", nil)
}

func (f *fakeApps) AddAppFile(_ context.Context, platform string, file ports.StoredAppFile, actor, _ string) (json.RawMessage, error) {
	if f.down {
		return nil, errInstrumentDown
	}
	a := f.apps[platform]
	var replaced *ports.StoredAppFile
	if file.Kind == domain.AppKindMobileconfig {
		replaced = a.Mobileconfig
		if replaced != nil {
			a.Files = slices.DeleteFunc(a.Files, func(o ports.StoredAppFile) bool { return o.FileID == replaced.FileID })
		}
		a.Mobileconfig = &file
	} else {
		a.Current, a.Mode = &file, "FILE"
	}
	a.Files = append([]ports.StoredAppFile{file}, a.Files...)
	a.Version++
	a.UpdatedBy = actor
	if f.lose {
		f.lose = false
		return nil, errInstrumentDown
	}
	return f.answer(a, "replaced", replaced)
}

func (f *fakeApps) DeleteAppFile(_ context.Context, platform, fileID, actor, _ string) (json.RawMessage, error) {
	if f.down {
		return nil, errInstrumentDown
	}
	a := f.apps[platform]
	i := slices.IndexFunc(a.Files, func(o ports.StoredAppFile) bool { return o.FileID == fileID })
	if i < 0 {
		return nil, apperr.NotFound("no such file")
	}
	gone := a.Files[i]
	a.Files = slices.Delete(a.Files, i, i+1)
	if a.Current != nil && a.Current.FileID == fileID {
		a.Current, a.Mode = nil, "OFF"
		if a.LinkURL != "" {
			a.Mode = "LINK"
		}
	}
	if a.Mobileconfig != nil && a.Mobileconfig.FileID == fileID {
		a.Mobileconfig = nil
	}
	a.Version++
	a.UpdatedBy = actor
	return f.answer(a, "removed", &gone)
}

// fakeAppFiles keeps the parts and the stored files in memory: a part
// must have its length, a file its size and SHA-256; invalid fails the
// package check.
type fakeAppFiles struct {
	parts   map[string]map[int][]byte
	dirs    map[string]time.Time
	stored  map[string]time.Time
	free    uint64
	invalid bool
	origins []string
	now     func() time.Time
}

func newFakeAppFiles(now func() time.Time) *fakeAppFiles {
	return &fakeAppFiles{parts: map[string]map[int][]byte{}, dirs: map[string]time.Time{}, stored: map[string]time.Time{}, free: 100 << 30, now: now}
}

func (f *fakeAppFiles) PutPart(_ context.Context, id string, n int, body io.Reader, size int64) error {
	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if int64(len(b)) != size {
		return apperr.Invalid("a part of the wrong length")
	}
	if f.parts[id] == nil {
		f.parts[id] = map[int][]byte{}
	}
	f.parts[id][n] = b
	return nil
}

func (f *fakeAppFiles) Store(_ context.Context, u domain.AppUpload, fileID, origin, _ string) (ports.StoredAppFile, error) {
	var all []byte
	for n := 1; n <= u.Parts(); n++ {
		all = append(all, f.parts[u.ID][n]...)
	}
	sum := sha256.Sum256(all)
	switch {
	case hex.EncodeToString(sum[:]) != u.SHA256:
		return ports.StoredAppFile{}, domain.AppFileInvalid("its SHA-256 is another")
	case f.invalid:
		return ports.StoredAppFile{}, domain.AppFileInvalid("no AndroidManifest.xml")
	}
	f.origins = append(f.origins, origin)
	dir := strings.ToLower(u.Platform)
	out := ports.StoredAppFile{
		FileID: fileID, Kind: u.Kind, Name: u.Name, Size: int64(len(all)), SHA256: u.SHA256, StoredAs: dir + "/" + fileID + "." + u.Ext(),
		Origin: origin, Package: "vip.astras.app", Version: "1.2.0", Build: "42",
	}
	if u.Ext() == "ipa" {
		out.Manifest = dir + "/" + fileID + ".plist"
		f.stored[out.Manifest] = f.now()
	}
	if u.Kind == domain.AppKindMobileconfig {
		out.Package, out.Version, out.Build = "", "", ""
	}
	f.stored[out.StoredAs] = f.now()
	return out, nil
}

func (f *fakeAppFiles) DropUpload(id string) error {
	delete(f.parts, id)
	delete(f.dirs, id)
	return nil
}

func (f *fakeAppFiles) Remove(paths ...string) error {
	for _, p := range paths {
		delete(f.stored, p)
	}
	return nil
}

func (f *fakeAppFiles) Stored() ([]ports.StoredPath, error) {
	var out []ports.StoredPath
	for p, at := range f.stored {
		out = append(out, ports.StoredPath{Path: p, ModTime: at})
	}
	return out, nil
}

func (f *fakeAppFiles) Free() (uint64, error) { return f.free, nil }

// UploadDirs lists the uploads with parts, and the directories left
// without one (dirs), all written at the time put in dirs or now.
func (f *fakeAppFiles) UploadDirs() ([]ports.StoredPath, error) {
	var out []ports.StoredPath
	for id := range f.parts {
		out = append(out, ports.StoredPath{Path: id, ModTime: f.now()})
	}
	for id, at := range f.dirs {
		out = append(out, ports.StoredPath{Path: id, ModTime: at})
	}
	return out, nil
}

// memUploads keeps app_uploads in a map.
type memUploads struct {
	rows map[string]domain.AppUpload
	hold map[string]time.Time
}

func newMemUploads() *memUploads {
	return &memUploads{rows: map[string]domain.AppUpload{}, hold: map[string]time.Time{}}
}

func (m *memUploads) Create(_ context.Context, u domain.AppUpload) error {
	m.rows[u.ID] = u
	return nil
}

func (m *memUploads) Get(_ context.Context, id string) (*domain.AppUpload, error) {
	u, ok := m.rows[id]
	if !ok {
		return nil, nil
	}
	u.Received = slices.Clone(u.Received)
	return &u, nil
}

func (m *memUploads) Received(ctx context.Context, id string, n int) (*domain.AppUpload, error) {
	u, ok := m.rows[id]
	if !ok {
		return nil, nil
	}
	if !slices.Contains(u.Received, n) {
		u.Received = append(u.Received, n)
		slices.Sort(u.Received)
	}
	m.rows[id] = u
	return m.Get(ctx, id)
}

func (m *memUploads) Claim(_ context.Context, id string, now, until time.Time) (bool, error) {
	if h, ok := m.hold[id]; ok && h.After(now) {
		return false, nil
	}
	m.hold[id] = until
	return true, nil
}

func (m *memUploads) Release(_ context.Context, id string) error {
	delete(m.hold, id)
	return nil
}

func (m *memUploads) Busy(_ context.Context, id string, now time.Time) (bool, error) {
	h, ok := m.hold[id]
	return ok && h.After(now), nil
}

func (m *memUploads) Delete(_ context.Context, id string) error {
	delete(m.rows, id)
	return nil
}

func (m *memUploads) Open(_ context.Context, now time.Time) (int, error) {
	n := 0
	for _, u := range m.rows {
		if u.ExpiresAt.After(now) {
			n++
		}
	}
	return n, nil
}

func (m *memUploads) Expired(_ context.Context, now time.Time) ([]domain.AppUpload, error) {
	var out []domain.AppUpload
	for _, u := range m.rows {
		if !u.ExpiresAt.After(now) {
			out = append(out, u)
		}
	}
	return out, nil
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// appsOf gives the service the downloads: instrument-service's apps, the
// disk and the uploads, in memory.
func (h *harness) appsOf(t *testing.T) (*fakeApps, *fakeAppFiles, *memUploads) {
	t.Helper()
	apps, files, uploads := newFakeApps(), newFakeAppFiles(func() time.Time { return h.now }), newMemUploads()
	h.svc.Apps, h.svc.AppFiles, h.svc.AppUploads = apps, files, uploads
	h.svc.Platform = newFakePlatform()
	return apps, files, uploads
}

func TestAppUploads(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	h.admin(t, "ops@example.com", domain.RoleOperator)
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	boss, ops, auditor := h.login(t, "boss@example.com"), h.login(t, "ops@example.com"), h.login(t, "audit@example.com")
	if _, err := h.svc.PlatformApps(ctx, auditor); code(err) != apperr.CodeUnavailable {
		t.Fatalf("without the downloads: %v", err)
	}
	apps, files, uploads := h.appsOf(t)
	if raw, err := h.svc.PlatformApps(ctx, auditor); err != nil || !strings.Contains(string(raw), `"platform":"ANDROID"`) {
		t.Fatalf("read %s %v", raw, err)
	}

	// A file of two parts and a bit.
	apk := bytes.Repeat([]byte("apk!"), (2*domain.AppPartSize+100)/4)
	if _, err := h.svc.StartAppUpload(ctx, ops, domain.AppAndroid, domain.AppKindApp, "a.apk", int64(len(apk)), sha(apk)); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an OPERATOR: %v", err)
	}
	for name, bad := range map[string][4]any{
		"a configuration profile on Android": {domain.AppAndroid, domain.AppKindMobileconfig, "a.mobileconfig", int64(10)},
		"an .ipa on Android":                 {domain.AppAndroid, domain.AppKindApp, "a.ipa", int64(10)},
		"too big":                            {domain.AppAndroid, domain.AppKindApp, "a.apk", int64(domain.AppMaxSize + 1)},
		"a big profile":                      {domain.AppIOS, domain.AppKindMobileconfig, "a.mobileconfig", int64(domain.AppMaxMobileconfig + 1)},
		"empty":                              {domain.AppIOS, domain.AppKindApp, "a.ipa", int64(0)},
	} {
		if _, err := h.svc.StartAppUpload(ctx, boss, bad[0].(string), bad[1].(string), bad[2].(string), bad[3].(int64), sha(apk)); code(err) != apperr.CodeInvalidArgument {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := h.svc.StartAppUpload(ctx, boss, "WINDOWS", domain.AppKindApp, "a.apk", 10, sha(apk)); code(err) != apperr.CodeNotFound {
		t.Fatalf("no such platform: %v", err)
	}
	if _, err := h.svc.StartAppUpload(ctx, boss, domain.AppAndroid, domain.AppKindApp, "a.apk", 10, "ABC"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("a bad SHA-256: %v", err)
	}
	files.free = 1 << 30
	if _, err := h.svc.StartAppUpload(ctx, boss, domain.AppAndroid, domain.AppKindApp, "a.apk", int64(len(apk)), sha(apk)); code(err) != "PLATFORM_APP_DISK_FULL" {
		t.Fatalf("no room: %v", err)
	}
	files.free = 100 << 30
	u, err := h.svc.StartAppUpload(ctx, boss, domain.AppAndroid, domain.AppKindApp, " Astras-1.2.0.apk ", int64(len(apk)), sha(apk))
	if err != nil || u.Parts() != 3 || u.Name != "Astras-1.2.0.apk" || u.StartedBy != "boss@example.com" || !u.ExpiresAt.Equal(h.now.Add(24*time.Hour)) {
		t.Fatalf("started %+v %v", u, err)
	}

	// Parts in any order, each its length; completing with one missing
	// says which.
	part := func(n int) []byte { return apk[(n-1)*domain.AppPartSize : min(n*domain.AppPartSize, len(apk))] }
	if _, err := h.svc.PutAppUploadPart(ctx, boss, domain.AppAndroid, u.ID, 4, bytes.NewReader(nil)); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("part 4 of 3: %v", err)
	}
	if _, err := h.svc.PutAppUploadPart(ctx, boss, domain.AppAndroid, u.ID, 3, bytes.NewReader(part(2))); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("a part of another length: %v", err)
	}
	if _, err := h.svc.PutAppUploadPart(ctx, boss, domain.AppIOS, u.ID, 3, bytes.NewReader(part(3))); code(err) != apperr.CodeNotFound {
		t.Fatalf("on the other platform: %v", err)
	}
	for _, n := range []int{3, 1} {
		got, err := h.svc.PutAppUploadPart(ctx, boss, domain.AppAndroid, u.ID, n, bytes.NewReader(part(n)))
		if err != nil {
			t.Fatalf("part %d: %v", n, err)
		}
		u = got
	}
	if !slices.Equal(u.Received, []int{1, 3}) {
		t.Fatalf("received %v", u.Received)
	}
	if _, err := h.svc.CompleteAppUpload(ctx, boss, domain.AppAndroid, u.ID, "version 1.2.0", "admin.astras.vip"); code(err) != "PLATFORM_APP_UPLOAD_INCOMPLETE" ||
		!strings.Contains(detail(err, "missing"), "2") {
		t.Fatalf("incomplete: %v", err)
	}
	if _, err := h.svc.PutAppUploadPart(ctx, boss, domain.AppAndroid, u.ID, 2, bytes.NewReader(part(2))); err != nil {
		t.Fatal(err)
	}
	// Another completion (or a part being written) holds it: parts,
	// completions and drops wait (review FX, A74 ③); a part sent lets go.
	uploads.hold[u.ID] = h.now.Add(time.Minute)
	if _, err := h.svc.PutAppUploadPart(ctx, boss, domain.AppAndroid, u.ID, 2, bytes.NewReader(part(2))); code(err) != "PLATFORM_APP_UPLOAD_BUSY" {
		t.Fatalf("a part while completing: %v", err)
	}
	if _, err := h.svc.CompleteAppUpload(ctx, boss, domain.AppAndroid, u.ID, "version 1.2.0", "admin.astras.vip"); code(err) != "PLATFORM_APP_UPLOAD_BUSY" {
		t.Fatalf("completed twice: %v", err)
	}
	if err := h.svc.DropAppUpload(ctx, boss, domain.AppAndroid, u.ID); code(err) != "PLATFORM_APP_UPLOAD_BUSY" {
		t.Fatalf("dropped while held: %v", err)
	}
	delete(uploads.hold, u.ID)
	if _, err := h.svc.PutAppUploadPart(ctx, boss, domain.AppAndroid, u.ID, 2, bytes.NewReader(part(2))); err != nil || uploads.hold[u.ID].After(h.now) {
		t.Fatalf("a part sent again: %v, still held until %v", err, uploads.hold[u.ID])
	}

	// instrument-service down: the stored file goes, the upload stays.
	apps.down = true
	if _, err := h.svc.CompleteAppUpload(ctx, boss, domain.AppAndroid, u.ID, "version 1.2.0", "admin.astras.vip"); code(err) != apperr.CodeUnavailable {
		t.Fatalf("instrument-service down: %v", err)
	}
	apps.down = false
	if len(files.stored) != 1 || uploads.rows[u.ID].ID == "" || uploads.hold[u.ID].After(h.now) {
		t.Fatalf("after a failed completion: stored %v, upload %+v, hold %v", files.stored, uploads.rows[u.ID], uploads.hold[u.ID])
	}
	// (A file stored while it could not be told whether it was kept stays
	// for the sweep.)
	for p := range files.stored {
		delete(files.stored, p)
	}

	// Completed: the platform's current app, at the profile's domain; the
	// upload and its parts gone; audited.
	raw, err := h.svc.CompleteAppUpload(ctx, boss, domain.AppAndroid, u.ID, "version 1.2.0", "admin.astras.vip")
	var got fakeApp
	if err != nil || json.Unmarshal(raw, &got) != nil || got.Mode != "FILE" || got.Current == nil || got.Current.UploadedBy != "boss@example.com" ||
		got.Current.Origin != "https://astras.vip" || got.Current.Size != int64(len(apk)) {
		t.Fatalf("completed %s %v", raw, err)
	}
	if _, ok := uploads.rows[u.ID]; ok || files.parts[u.ID] != nil || len(files.stored) != 1 {
		t.Fatalf("after completing: %+v %v", uploads.rows, files.stored)
	}
	if a := h.auditsOf("admin.platform.app_file_uploaded"); len(a) != 1 || !strings.Contains(a[0], `"sha256":"`+sha(apk)+`"`) ||
		!strings.HasPrefix(a[0], "app:ANDROID version 1.2.0") {
		t.Fatalf("audited %v", a)
	}
	if _, err := h.svc.AppUpload(ctx, boss, domain.AppAndroid, u.ID); code(err) != apperr.CodeNotFound {
		t.Fatalf("a completed upload: %v", err)
	}

	// Without a domain the console's site: admin.<site> is <site>.
	h.svc.Platform.(*fakePlatform).profile["domain"] = ""
	mc := []byte("<plist><dict><key>PayloadType</key><string>Configuration</string></dict></plist>")
	cfg1 := h.startAndSend(t, boss, domain.AppIOS, domain.AppKindMobileconfig, "trust.mobileconfig", mc)
	if _, err := h.svc.CompleteAppUpload(ctx, boss, domain.AppIOS, cfg1.ID, "trust profile", "admin.example.com:443"); err != nil ||
		files.origins[len(files.origins)-1] != "https://example.com" {
		t.Fatalf("a profile at the console's site: %v %v", err, files.origins)
	}
	if _, err := h.svc.CompleteAppUpload(ctx, boss, domain.AppIOS, h.startAndSend(t, boss, domain.AppIOS, domain.AppKindMobileconfig, "b.mobileconfig", mc).ID,
		"another", "localhost"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("no domain and no site: %v", err)
	}
	h.svc.Platform.(*fakePlatform).profile["domain"] = "astras.vip"
	// A second profile replaces the first, whose file goes.
	first := apps.apps[domain.AppIOS].Mobileconfig.StoredAs
	cfg2 := h.startAndSend(t, boss, domain.AppIOS, domain.AppKindMobileconfig, "trust2.mobileconfig", mc)
	if _, err := h.svc.CompleteAppUpload(ctx, boss, domain.AppIOS, cfg2.ID, "a new profile", "admin.astras.vip"); err != nil {
		t.Fatal(err)
	}
	if _, ok := files.stored[first]; ok || len(apps.apps[domain.AppIOS].Files) != 1 {
		t.Fatalf("the replaced profile stays: %v", files.stored)
	}

	// A file that is not what it claims drops its upload.
	files.invalid = true
	bad := h.startAndSend(t, boss, domain.AppAndroid, domain.AppKindApp, "bad.apk", []byte("not a zip"))
	if _, err := h.svc.CompleteAppUpload(ctx, boss, domain.AppAndroid, bad.ID, "a bad one", "admin.astras.vip"); code(err) != "PLATFORM_APP_FILE_INVALID" ||
		detail(err, "reason") != "no AndroidManifest.xml" {
		t.Fatalf("an invalid file: %v", err)
	}
	if _, ok := uploads.rows[bad.ID]; ok {
		t.Fatal("an invalid upload kept")
	}
	files.invalid = false

	// At most three open (the one without a site is still open); one
	// dropped, another fits.
	var open []domain.AppUpload
	for i := range 2 {
		u, err := h.svc.StartAppUpload(ctx, boss, domain.AppAndroid, domain.AppKindApp, "x.apk", 10, sha([]byte{byte(i)}))
		if err != nil {
			t.Fatal(err)
		}
		open = append(open, u)
	}
	if _, err := h.svc.StartAppUpload(ctx, boss, domain.AppAndroid, domain.AppKindApp, "x.apk", 10, sha(apk)); code(err) != "PLATFORM_APP_UPLOADS_FULL" {
		t.Fatalf("a fourth: %v", err)
	}
	if err := h.svc.DropAppUpload(ctx, boss, domain.AppAndroid, open[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.StartAppUpload(ctx, boss, domain.AppAndroid, domain.AppKindApp, "x.apk", 10, sha(apk)); err != nil {
		t.Fatalf("after one dropped: %v", err)
	}

	// A day later the open ones are swept; a file no platform keeps goes
	// once an hour old, those kept stay.
	files.stored["android/0192a000-0000-7000-8000-0000000000ff.apk"] = h.now
	// A directory left without its row (review FX, A74 ④): swept once old.
	files.dirs["0192a000-0000-7000-8000-0000000000dd"] = h.now
	h.now = h.now.Add(25 * time.Hour)
	files.stored["android/0192a000-0000-7000-8000-0000000000fe.apk"] = h.now
	files.dirs["0192a000-0000-7000-8000-0000000000dc"] = h.now
	swept, gone, err := h.svc.SweepAppFiles(ctx)
	if err != nil || swept != 4 || gone != 1 || len(uploads.rows) != 0 || len(files.dirs) != 1 || files.dirs["0192a000-0000-7000-8000-0000000000dc"].IsZero() {
		t.Fatalf("swept %d uploads, %d files: %v %v", swept, gone, err, files.stored)
	}
	if _, ok := files.stored["android/0192a000-0000-7000-8000-0000000000fe.apk"]; !ok {
		t.Fatal("a young file swept")
	}
	if _, ok := files.stored[apps.apps[domain.AppAndroid].Current.StoredAs]; !ok {
		t.Fatal("a kept file swept")
	}
}

func TestAppSettingsAndDeletion(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	boss := h.login(t, "boss@example.com")
	apps, files, _ := h.appsOf(t)

	write := json.RawMessage(`{"mode":"LINK","link_url":"https://apps.apple.com/app/id1","notes":{"zh-CN":"首版"},"enabled":true,"expected_version":1}`)
	raw, err := h.svc.SetPlatformApp(ctx, boss, domain.AppIOS, write, "on the App Store")
	if err != nil || !strings.Contains(string(raw), `"mode":"LINK"`) || apps.apps[domain.AppIOS].UpdatedBy != "boss@example.com" {
		t.Fatalf("set %s %v", raw, err)
	}
	if a := h.auditsOf("admin.platform.app_updated"); len(a) != 1 || !strings.Contains(a[0], `"mode":{"new":"LINK","old":"OFF"}`) ||
		!strings.Contains(a[0], `"enabled":{"new":true,"old":false}`) || strings.Contains(a[0], `"link_url":{"new":"","old":""}`) {
		t.Fatalf("audited %v", a)
	}
	if _, err := h.svc.SetPlatformApp(ctx, boss, domain.AppIOS, write, "stale"); code(err) != "INSTRUMENT_PLATFORM_CHANGED" {
		t.Fatalf("stale: %v", err)
	}

	ipa := []byte("an ipa")
	u := h.startAndSend(t, boss, domain.AppIOS, domain.AppKindApp, "Astras.ipa", ipa)
	if _, err := h.svc.CompleteAppUpload(ctx, boss, domain.AppIOS, u.ID, "enterprise build", "admin.astras.vip"); err != nil {
		t.Fatal(err)
	}
	cur := *apps.apps[domain.AppIOS].Current
	if len(files.stored) != 2 || cur.Manifest == "" {
		t.Fatalf("an .ipa with its manifest: %v %+v", files.stored, cur)
	}
	raw, err = h.svc.DeleteAppFile(ctx, boss, domain.AppIOS, cur.FileID, "withdrawn")
	if err != nil || !strings.Contains(string(raw), `"mode":"LINK"`) || len(files.stored) != 0 {
		t.Fatalf("deleted %s %v %v", raw, err, files.stored)
	}
	if a := h.auditsOf("admin.platform.app_file_deleted"); len(a) != 1 || !strings.Contains(a[0], cur.FileID) {
		t.Fatalf("audited %v", a)
	}
	if _, err := h.svc.DeleteAppFile(ctx, boss, domain.AppIOS, cur.FileID, "again"); code(err) != apperr.CodeNotFound {
		t.Fatalf("deleted twice: %v", err)
	}
	if _, err := h.svc.DeleteAppFile(ctx, boss, domain.AppIOS, "../../etc", "a path"); code(err) != apperr.CodeNotFound {
		t.Fatalf("not a file ID: %v", err)
	}
}

// startAndSend starts an upload of data and sends its parts.
func (h *harness) startAndSend(t *testing.T, p Principal, platform, kind, name string, data []byte) domain.AppUpload {
	t.Helper()
	ctx := context.Background()
	u, err := h.svc.StartAppUpload(ctx, p, platform, kind, name, int64(len(data)), sha(data))
	if err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	for n := 1; n <= u.Parts(); n++ {
		chunk := data[(n-1)*domain.AppPartSize : min(n*domain.AppPartSize, len(data))]
		if u, err = h.svc.PutAppUploadPart(ctx, p, platform, u.ID, n, bytes.NewReader(chunk)); err != nil {
			t.Fatalf("part %d of %s: %v", n, name, err)
		}
	}
	return u
}

// detail is an error's detail as text.
func detail(err error, key string) string {
	var e *apperr.Error
	if !errors.As(err, &e) {
		return ""
	}
	b, _ := json.Marshal(e.Details[key])
	return strings.Trim(string(b), `"`)
}
