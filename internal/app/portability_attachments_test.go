package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// All bytes, names and storage faults below are synthetic. No VM is started.
type portableFixtureBlobs struct {
	mu         sync.Mutex
	data       map[string][]byte
	puts       int
	failPutAt  int
	failDelete bool
	quota      int
}

func newPortableFixtureBlobs() *portableFixtureBlobs {
	return &portableFixtureBlobs{data: map[string][]byte{}}
}
func (f *portableFixtureBlobs) PutBlob(_ context.Context, id string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts++
	if f.quota > 0 && len(data) > f.quota {
		return errors.New("synthetic quota full")
	}
	if _, ok := f.data[id]; ok {
		return errors.New("alias exists")
	}
	f.data[id] = append([]byte(nil), data...)
	if f.puts == f.failPutAt {
		return errors.New("synthetic acknowledgement lost")
	}
	return nil
}
func (f *portableFixtureBlobs) GetBlob(_ context.Context, id string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if data, ok := f.data[id]; ok {
		return append([]byte(nil), data...), nil
	}
	return nil, errors.New("missing fixture")
}
func (f *portableFixtureBlobs) DeleteBlob(ctx context.Context, id string) error {
	return f.DeleteStagedBlob(ctx, id, "")
}
func (f *portableFixtureBlobs) DeleteStagedBlob(_ context.Context, id, digest string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failDelete {
		return errors.New("synthetic storage unavailable")
	}
	delete(f.data, id)
	return nil
}
func (f *portableFixtureBlobs) aliases() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.data) }
func portableAssetFixture(t *testing.T) (*Store, portableBundle, *portableFixtureBlobs) {
	t.Helper()
	s, b := portableFixture(t)
	backend := newPortableFixtureBlobs()
	s.requireGuestAttachments = true
	s.guestBlobs = backend
	message := b.Messages[0]
	for i := 0; i < 2; i++ {
		a, err := s.AddAttachment(message.ConversationID, "synthetic-"+string(rune('a'+i))+".txt", "text/plain", strings.NewReader("Synthetic bytes 中文 "+string(rune('a'+i))))
		if err != nil {
			t.Fatal(err)
		}
		if err = s.BindAttachments(message.ConversationID, message.ID, []string{a.ID}); err != nil {
			t.Fatal(err)
		}
	}
	b, err := s.exportPortable(context.Background(), "synthetic-source-instance", portableSelection{}, "account")
	if err != nil {
		t.Fatal(err)
	}
	return s, b, backend
}
func assertNoPortableRows(t *testing.T, s *Store) {
	t.Helper()
	for _, table := range []string{"bots", "conversations", "members", "messages", "memories", "schedules", "attachments", "attachment_messages", "portability_provenance", "portability_missing_assets", "workspace_events"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("partial exposure in %s: %d %v", table, n, err)
		}
	}
}
func cloneAssetBundle(t *testing.T, b portableBundle) portableBundle {
	t.Helper()
	raw, _ := json.Marshal(b)
	var copy portableBundle
	if err := json.Unmarshal(raw, &copy); err != nil {
		t.Fatal(err)
	}
	return copy
}
func TestPortableAttachmentBytesReferencesProvenanceAndDuplicate(t *testing.T) {
	_, b, _ := portableAssetFixture(t)
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	files := newPortableFixtureBlobs()
	s.requireGuestAttachments = true
	s.guestBlobs = files
	p, err := s.previewPortable(context.Background(), b)
	if err != nil || !p.CanApply || p.Counts["attachments"] != 2 || p.Counts["attachment_bindings"] != 2 || p.AttachmentBytes == 0 {
		t.Fatalf("preview: %+v %v", p, err)
	}
	assertNoPortableRows(t, s)
	if files.puts != 0 {
		t.Fatal("preview wrote files")
	}
	results := make([]portableResult, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results[i], errs[i] = s.applyPortable(context.Background(), b, p.ID) }(i)
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || !reflect.DeepEqual(results[0], results[1]) || files.puts != 2 || files.aliases() != 2 {
		t.Fatalf("retry changed result or bytes: %v %v", errs[0], errs[1])
	}
	result := results[0]
	for _, a := range b.Attachments {
		got, path, err := s.Attachment(result.IDMap[a.ID])
		if err != nil || path != "vm:"+result.IDMap[a.ID] || got.ConversationID != result.IDMap[a.ConversationID] || got.Name != a.Name || result.IDMap[a.ID] == a.ID {
			t.Fatalf("metadata/ref remap: %+v %q %v", got, path, err)
		}
		reader, err := s.openAttachment(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		buf.ReadFrom(reader)
		reader.Close()
		wanted, _ := base64.StdEncoding.DecodeString(a.Data)
		if !bytes.Equal(buf.Bytes(), wanted) {
			t.Fatal("byte roundtrip changed")
		}
	}
	for _, binding := range b.AttachmentBindings {
		var n int
		if err = s.db.QueryRow(`SELECT COUNT(*) FROM attachment_messages WHERE attachment_id=? AND message_id=?`, result.IDMap[binding.AttachmentID], result.IDMap[binding.MessageID]).Scan(&n); err != nil || n != 1 {
			t.Fatal("attachment binding lost")
		}
	}
	if _, err = os.Stat(filepath.Join(dir, "attachments")); !os.IsNotExist(err) {
		t.Fatal("host fallback created")
	}
	exported, err := s.exportPortable(context.Background(), "destination", portableSelection{}, "account")
	if err != nil || len(exported.Attachments) != 2 || exported.Attachments[0].Origin.InstanceID != "synthetic-source-instance" {
		t.Fatalf("reexport provenance: %v", err)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&n)
	if n != 0 {
		t.Fatal("import executed work")
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM schedules WHERE status<>'paused'`).Scan(&n)
	if n != 0 {
		t.Fatal("import resumed schedule")
	}
	s.Close()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.guestBlobs = files
	retry, err := s.applyPortable(context.Background(), b, p.ID)
	if err != nil || !reflect.DeepEqual(retry, result) || files.puts != 2 {
		t.Fatal("restart duplicated assets")
	}
	s.guestBlobs = nil
	retry, err = s.applyPortable(context.Background(), b, p.ID)
	if err != nil || !reflect.DeepEqual(retry, result) {
		t.Fatal("receipt depended on available storage")
	}
}
func TestPortableAttachmentFailuresRollbackAndRestartRecovery(t *testing.T) {
	_, b, _ := portableAssetFixture(t)
	for _, fault := range []string{"quota", "lost_ack", "sqlite", "crash_after_stage"} {
		t.Run(fault, func(t *testing.T) {
			dir := t.TempDir()
			s, err := OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			files := newPortableFixtureBlobs()
			s.guestBlobs = files
			p, err := s.previewPortable(context.Background(), b)
			if err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "quota":
				files.quota = 1
			case "lost_ack":
				files.failPutAt = 2
				files.failDelete = true
			case "sqlite":
				_, err = s.db.Exec(`CREATE TRIGGER portable_fixture_failure BEFORE INSERT ON workspace_events BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`)
				if err != nil {
					t.Fatal(err)
				}
			case "crash_after_stage":
				if _, err = s.preparePortableAssets(context.Background(), b, p.ID); err != nil {
					t.Fatal(err)
				}
			}
			if fault != "crash_after_stage" {
				if _, err = s.applyPortable(context.Background(), b, p.ID); err == nil {
					t.Fatal("fault was ignored")
				}
			}
			assertNoPortableRows(t, s)
			if fault == "quota" || fault == "sqlite" {
				if files.aliases() != 0 {
					t.Fatal("rollback leaked aliases")
				}
			}
			var status string
			s.db.QueryRow(`SELECT status FROM portability_imports WHERE id=?`, p.ID).Scan(&status)
			if status != "preview" {
				t.Fatal("partial receipt published")
			}
			s.Close()
			files.failDelete = false
			files.failPutAt = 0
			files.quota = 0
			s, err = OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			s.guestBlobs = files
			if fault == "sqlite" {
				s.db.Exec(`DROP TRIGGER portable_fixture_failure`)
			}
			assertNoPortableRows(t, s)
			first, err := s.applyPortable(context.Background(), b, p.ID)
			if err != nil || files.aliases() != 2 {
				t.Fatalf("restart recovery: %v aliases=%d", err, files.aliases())
			}
			var pending int
			s.db.QueryRow(`SELECT COUNT(*) FROM portability_asset_staging`).Scan(&pending)
			if pending != 0 {
				t.Fatal("stage journal not finalized")
			}
			second, err := s.applyPortable(context.Background(), b, p.ID)
			if err != nil || !reflect.DeepEqual(first, second) {
				t.Fatal("retry published a second copy")
			}
		})
	}
}
func TestPortableAttachmentMissingAndUnsafeFilesRemainHonest(t *testing.T) {
	s, b, files := portableAssetFixture(t)
	files.DeleteBlob(context.Background(), b.Attachments[0].ID)
	b, err := s.exportPortable(context.Background(), "source", portableSelection{}, "account")
	if err != nil || len(b.Attachments) != 1 || len(b.MissingAttachments) != 1 || b.MissingAttachments[0].Reason != "missing" {
		t.Fatalf("missing export: %+v %v", b.Counts, err)
	}
	dest, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	p, err := dest.previewPortable(context.Background(), b)
	if err != nil || p.CanApply || !strings.Contains(strings.Join(p.Warnings, " "), "synthetic-") {
		t.Fatal("storage/missing preview incomplete")
	}
	if _, err = dest.applyPortable(context.Background(), b, p.ID); err == nil {
		t.Fatal("missing backend accepted")
	}
	assertNoPortableRows(t, dest)
	dest.guestBlobs = newPortableFixtureBlobs()
	p, err = dest.previewPortable(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = dest.applyPortable(context.Background(), b, p.ID); err != nil {
		t.Fatal(err)
	}
	again, err := dest.exportPortable(context.Background(), "destination", portableSelection{}, "account")
	if err != nil || again.AttachmentCount != 1 || again.MissingAttachments[0].Origin.RecordID != b.MissingAttachments[0].Origin.RecordID {
		t.Fatal("omission/provenance was erased on reexport")
	}
	without, err := selectPortable(b, portableSelection{Categories: []string{"bot_config", "chats"}})
	if err != nil || len(without.Attachments) != 0 || without.AttachmentCount != 2 {
		t.Fatal("deselection misreported omissions")
	}
	// Managed legacy files are eligible; forged paths, symlinks and hard links are not.
	local, base := portableFixture(t)
	a, err := local.AddAttachment(base.Conversations[0].ID, "synthetic-local.txt", "text/plain", strings.NewReader("synthetic managed bytes"))
	if err != nil {
		t.Fatal(err)
	}
	_, path, _ := local.Attachment(a.ID)
	outside := filepath.Join(t.TempDir(), "synthetic-outside")
	os.WriteFile(outside, []byte("synthetic never-export"), 0600)
	for _, fault := range []string{"symlink", "hardlink", "traversal", "metadata", "oversize"} {
		t.Run(fault, func(t *testing.T) {
			local.db.Exec(`UPDATE attachments SET disk_name=?,size=? WHERE id=?`, a.ID+".upload", len("synthetic managed bytes"), a.ID)
			os.Remove(path)
			os.WriteFile(path, []byte("synthetic managed bytes"), 0600)
			switch fault {
			case "symlink":
				os.Remove(path)
				os.Symlink(outside, path)
			case "hardlink":
				os.Remove(path)
				os.Link(outside, path)
			case "traversal":
				local.db.Exec(`UPDATE attachments SET disk_name=? WHERE id=?`, "../"+a.ID+".upload", a.ID)
			case "metadata":
				local.db.Exec(`UPDATE attachments SET size=size+1 WHERE id=?`, a.ID)
			case "oversize":
				local.db.Exec(`UPDATE attachments SET size=? WHERE id=?`, portableMaxAttachmentBytes+1, a.ID)
			}
			out, err := local.exportPortable(context.Background(), "source", portableSelection{}, "account")
			if err != nil || len(out.Attachments) != 0 || out.AttachmentCount != 1 {
				t.Fatalf("unsafe file was read: %s %v", fault, err)
			}
		})
	}
}
func TestPortableAttachmentForgedManifestsRejected(t *testing.T) {
	_, b, _ := portableAssetFixture(t)
	for _, fault := range []string{"hash", "size", "encoding", "MIME", "foreign_ref", "foreign_message", "duplicate_binding", "path", "unsupported_version", "excluded", "total"} {
		t.Run(fault, func(t *testing.T) {
			x := cloneAssetBundle(t, b)
			switch fault {
			case "hash":
				x.Attachments[0].SHA256 = strings.Repeat("0", 64)
			case "size":
				x.Attachments[0].Size++
			case "encoding":
				x.Attachments[0].Data += "\n"
			case "MIME":
				x.Attachments[0].MIME = "image/svg+xml"
			case "foreign_ref":
				x.Attachments[0].ConversationID = uuid.NewString()
			case "foreign_message":
				x.AttachmentBindings[0].MessageID = uuid.NewString()
			case "duplicate_binding":
				x.AttachmentBindings = append(x.AttachmentBindings, x.AttachmentBindings[0])
			case "path":
				x.Attachments[0].Name = "../private"
			case "unsupported_version":
				x.Version = 1
			case "excluded":
				x.Excluded = []string{}
			case "total":
				data := bytes.Repeat([]byte{'x'}, portableMaxAttachmentBytes)
				a := x.Attachments[0]
				a.Data = base64.StdEncoding.EncodeToString(data)
				a.Size = int64(len(data))
				a.SHA256 = portableBytesHash(data)
				a.MIME = portableFileMIME(data)
				x.Attachments = nil
				x.AttachmentBindings = nil
				for i := 0; i < 3; i++ {
					a.ID = uuid.NewString()
					x.Attachments = append(x.Attachments, a)
				}
			}
			x.Counts = x.counts()
			if x.validate() == nil {
				t.Fatal("forged assets accepted")
			}
		})
	}
	raw, _ := json.Marshal(b)
	injected := bytes.Replace(raw, []byte(`"data_base64":`), []byte(`"url":"https://example.test/never-fetch","data_base64":`), 1)
	if _, err := parsePortableBundle(injected); err == nil {
		t.Fatal("remote URL accepted")
	}
}
func TestPortableAttachmentForwardedStandaloneScope(t *testing.T) {
	s, b, files := portableAssetFixture(t)
	var dm portableConversation
	var group portableConversation
	for _, c := range b.Conversations {
		if c.Kind == "dm" && len(dm.ID) == 0 {
			dm = c
		}
		if c.Kind == "group" {
			group = c
		}
	}
	m, _, err := s.AddMessage(dm.ID, "user", "", "", "synthetic forwarded bytes", "forward-fixture")
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.AddAttachment(group.ID, "synthetic-forwarded.txt", "text/plain", strings.NewReader("synthetic forwarded only"))
	if err != nil {
		t.Fatal(err)
	}
	s.db.Exec(`INSERT INTO attachment_messages VALUES(?,?)`, a.ID, m.ID)
	out, err := s.exportPortable(context.Background(), "source", portableSelection{Categories: []string{"bot_config", "chats", "attachments"}, BotIDs: []string{dm.BotID}}, "bot")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range out.Attachments {
		if x.ID == a.ID {
			found = x.ConversationID == dm.ID && x.Origin.ConversationID == group.ID
		}
	}
	if !found {
		t.Fatal("forwarded file scope lost")
	}
	for _, c := range out.Conversations {
		if c.Kind == "group" {
			t.Fatal("owner group history leaked")
		}
	}
	files.DeleteBlob(context.Background(), a.ID)
	account, err := s.exportPortable(context.Background(), "source", portableSelection{}, "account")
	if err != nil {
		t.Fatal(err)
	}
	partial, err := selectPortable(account, portableSelection{Categories: []string{"bot_config", "chats", "attachments"}, BotIDs: []string{dm.BotID}})
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, missing := range partial.MissingAttachments {
		if missing.ID == a.ID {
			found = missing.ConversationID == dm.ID && len(missing.MessageIDs) == 1
		}
	}
	if !found {
		t.Fatal("forwarded missing asset disappeared from partial preview")
	}
}
func TestPortableAttachmentHTTPAccountIsolation(t *testing.T) {
	_, bundle, _ := portableAssetFixture(t)
	g := accountFixture(t)
	a, err := g.create(context.Background(), "asset-a", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	if err != nil {
		t.Fatal(err)
	}
	b, err := g.create(context.Background(), "asset-b", "", "SyntheticPassword123!", false, "")
	if err != nil {
		t.Fatal(err)
	}
	g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0 WHERE id=?`, b.ID)
	ac, bc := accountCookie(t, g, a), accountCookie(t, g, b)
	own, _ := g.workspace(a)
	other, _ := g.workspace(b)
	fa, fb := newPortableFixtureBlobs(), newPortableFixtureBlobs()
	own.store.guestBlobs, other.store.guestBlobs = fa, fb
	raw, _ := json.Marshal(bundle)
	req, _ := json.Marshal(portableImportRequest{Bundle: raw})
	w := accountRequest(g, "POST", "/api/portability/preview", string(req), ac)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var p portablePreview
	json.Unmarshal(w.Body.Bytes(), &p)
	req, _ = json.Marshal(portableImportRequest{Bundle: raw, PreviewID: p.ID})
	if w = accountRequest(g, "POST", "/api/portability/apply", string(req), bc); w.Code != 409 || fb.puts != 0 {
		t.Fatal("foreign preview wrote blobs")
	}
	assertNoPortableRows(t, other.store)
	w = accountRequest(g, "POST", "/api/portability/apply", string(req), ac)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var result portableResult
	json.Unmarshal(w.Body.Bytes(), &result)
	id := result.IDMap[bundle.Attachments[0].ID]
	w = accountRequest(g, "GET", "/api/attachments/"+id, "", bc)
	if w.Code != 404 {
		t.Fatal("sibling read imported file")
	}
	w = accountRequest(g, "GET", "/api/attachments/"+id, "", ac)
	data, _ := base64.StdEncoding.DecodeString(bundle.Attachments[0].Data)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data) {
		t.Fatal("own attachment download lost bytes")
	}
	// A manifest can only refer to conversations carried in its own package.
	request := httptest.NewRequest("POST", "/api/portability/apply", strings.NewReader(string(req)))
	request.AddCookie(ac)
	request.Header.Set("Origin", "https://evil.example.test")
	response := httptest.NewRecorder()
	g.Handler().ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatal("cross-site apply accepted")
	}
}

type portableGateBlobs struct {
	*portableFixtureBlobs
	started chan struct{}
	release chan struct{}
	once    sync.Once
	cleanup bool
}

func (f *portableGateBlobs) PutBlob(ctx context.Context, id string, data []byte) error {
	if !f.cleanup {
		f.once.Do(func() { close(f.started) })
		<-f.release
	}
	return f.portableFixtureBlobs.PutBlob(ctx, id, data)
}
func (f *portableGateBlobs) DeleteStagedBlob(ctx context.Context, id, digest string) error {
	if f.cleanup {
		f.once.Do(func() { close(f.started) })
		<-f.release
	}
	return f.portableFixtureBlobs.DeleteStagedBlob(ctx, id, digest)
}
func TestPortableAttachmentGuestIODoesNotHoldRuntimeSettingsLock(t *testing.T) {
	_, b, _ := portableAssetFixture(t)
	for _, cleanup := range []bool{false, true} {
		t.Run(map[bool]string{false: "stage", true: "rollback"}[cleanup], func(t *testing.T) {
			s, err := OpenStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			f := &portableGateBlobs{portableFixtureBlobs: newPortableFixtureBlobs(), started: make(chan struct{}), release: make(chan struct{}), cleanup: cleanup}
			s.guestBlobs = f
			if cleanup {
				s.db.Exec(`CREATE TRIGGER portable_fixture_failure BEFORE INSERT ON workspace_events BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`)
			}
			p, err := s.previewPortable(context.Background(), b)
			if err != nil {
				t.Fatal(err)
			}
			server := &Server{store: s}
			done := make(chan error, 1)
			go func() { _, err := server.applyPortableDefaults(context.Background(), b, p.ID); done <- err }()
			select {
			case <-f.started:
			case <-time.After(2 * time.Second):
				close(f.release)
				t.Fatal("storage did not block")
			}
			unlocked := make(chan struct{})
			go func() { server.mu.Lock(); server.mu.Unlock(); close(unlocked) }()
			select {
			case <-unlocked:
			case <-time.After(time.Second):
				close(f.release)
				<-done
				t.Fatal("guest I/O held runtime mutex")
			}
			close(f.release)
			err = <-done
			if cleanup && err == nil {
				t.Fatal("SQL failure ignored")
			}
		})
	}
}

func (f *portableFixtureBlobs) PutStagedBlob(ctx context.Context, id string, data []byte) error {
	return f.PutBlob(ctx, id, data)
}
func (f *portableGateBlobs) PutStagedBlob(ctx context.Context, id string, data []byte) error {
	return f.PutBlob(ctx, id, data)
}
