package app

// Every key, value and account below is created inside a disposable fixture.
import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

const environmentCanary = "  Synthetic-ONLY-Environment-Canary 中文\nKeep exact trailing space.  "

func portableSecretFixture(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	v, err := initializeSecretVault(dir)
	if err != nil {
		t.Fatal(err)
	}
	account := uuid.NewString()
	st.portabilitySecrets = &portableSecretCodec{vault: v, account: account}
	s := &Server{store: st, secretVault: v, instance: instanceIdentity{ID: account}}
	t.Cleanup(func() { s.store.Close() })
	return s, dir
}
func portableSyntheticEnvironment(t *testing.T, s *Server, target, value string) string {
	t.Helper()
	id := uuid.NewString()
	encrypted, err := s.secretVault.seal(id, value)
	if err != nil {
		t.Fatal(err)
	}
	s.secretVault.mu.Lock()
	defer s.secretVault.mu.Unlock()
	s.secretVault.records[id] = secretRecord{ID: id, Name: "Synthetic fixture", Kind: "env", Target: target, Status: "stored", CreatedAt: now(), Ciphertext: encrypted}
	if err = s.secretVault.saveLocked(); err != nil {
		t.Fatal(err)
	}
	return id
}
func portableSyntheticSensitiveBundle(t *testing.T, s *Server, id string) portableBundle {
	t.Helper()
	b, err := s.exportPortable(context.Background(), s.instance.ID, portableSelection{Categories: []string{"bot_config", portableEnvironmentCategory}, VaultEnvironmentIDs: []string{id}}, "account")
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func portableSensitiveSelection(b portableBundle) portableSelection {
	ids := []string{}
	for _, x := range b.VaultEnvironment {
		ids = append(ids, x.ID)
	}
	return portableSelection{Categories: b.Included, VaultEnvironmentIDs: ids}
}
func sensitiveRequest(s *Server, method, path string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://fixture.test/api/"+path, bytes.NewReader(body))
	r.Header.Set("Origin", "https://fixture.test")
	r = r.WithContext(context.WithValue(r.Context(), portableAccountAuthKey{}, true))
	w := httptest.NewRecorder()
	s.routePortability(w, r, path)
	return w
}

type portableSecretNoGuest struct{ calls int }

func (f *portableSecretNoGuest) PutBlob(context.Context, string, []byte) error {
	f.calls++
	return errPortableSecret
}
func (f *portableSecretNoGuest) GetBlob(context.Context, string) ([]byte, error) {
	f.calls++
	return nil, errPortableSecret
}
func (f *portableSecretNoGuest) DeleteBlob(context.Context, string) error {
	f.calls++
	return errPortableSecret
}
func (f *portableSecretNoGuest) PutStagedBlob(context.Context, string, []byte) error {
	f.calls++
	return errPortableSecret
}
func (f *portableSecretNoGuest) DeleteStagedBlob(context.Context, string, string) error {
	f.calls++
	return errPortableSecret
}

func TestPortableEnvironmentExplicitDefaultsAndAccountOnly(t *testing.T) {
	s, _ := portableSecretFixture(t)
	id := portableSyntheticEnvironment(t, s, "SYNTHETIC_TOKEN", environmentCanary)
	b := portableSyntheticSensitiveBundle(t, s, id)
	second := portableSyntheticEnvironment(t, s, "SYNTHETIC_SECOND", "Synthetic unselected second value")
	if len(b.VaultEnvironment) != 1 || b.VaultEnvironment[0].ID != id {
		t.Fatal("explicit export included unselected record")
	}
	two, e := s.exportPortable(context.Background(), s.instance.ID, portableSelection{Categories: []string{"bot_config", portableEnvironmentCategory}, VaultEnvironmentIDs: []string{id, second}}, "account")
	if e != nil {
		t.Fatal(e)
	}
	subset, e := selectPortable(two, portableSelection{Categories: two.Included, VaultEnvironmentIDs: []string{id}})
	if e != nil || len(subset.VaultEnvironment) != 1 || subset.Counts[portableEnvironmentCategory] != 1 || subset.VaultEnvironment[0].ID != id {
		t.Fatal("import subset broadened selection")
	}
	for _, sel := range []portableSelection{{}, {Categories: []string{}}} {
		exported, err := s.exportPortable(context.Background(), s.instance.ID, sel, "account")
		if err != nil || len(exported.VaultEnvironment) != 0 || containsPortable(exported.Included, portableEnvironmentCategory) || exported.Version == 3 {
			t.Fatal("legacy export opted into credentials")
		}
		selected, err := selectPortable(b, sel)
		if err != nil || len(selected.VaultEnvironment) != 0 || containsPortable(selected.Included, portableEnvironmentCategory) || selected.Version == 3 {
			t.Fatal("legacy import opted into credentials")
		}
	}
	for _, sel := range []portableSelection{{Categories: []string{"bot_config", portableEnvironmentCategory}}, {Categories: []string{portableEnvironmentCategory}, VaultEnvironmentIDs: []string{id}}, {Categories: []string{"bot_config", portableEnvironmentCategory, portableEnvironmentCategory}, VaultEnvironmentIDs: []string{id}}, {VaultEnvironmentIDs: []string{id}}, {Categories: []string{"bot_config", portableEnvironmentCategory}, VaultEnvironmentIDs: []string{id, id}}} {
		if _, err := s.exportPortable(context.Background(), s.instance.ID, sel, "account"); err == nil {
			t.Fatal("incomplete/duplicate explicit export accepted")
		}
		if _, err := selectPortable(b, sel); err == nil {
			t.Fatal("incomplete/duplicate explicit import accepted")
		}
	}
	if _, err := s.store.exportPortable(context.Background(), s.instance.ID, portableSensitiveSelection(b), "account"); err == nil {
		t.Fatal("store bypass accepted sensitive export")
	}
	if _, err := s.exportPortable(context.Background(), s.instance.ID, portableSensitiveSelection(b), "bot"); err == nil {
		t.Fatal("Bot export accepted account credentials")
	}
	b.Kind = "bot"
	if b.validate() == nil {
		t.Fatal("Bot archive accepted credentials")
	}
}
func TestPortableEnvironmentInactiveRoundTripRestartAndRedaction(t *testing.T) {
	source, _ := portableSecretFixture(t)
	id := portableSyntheticEnvironment(t, source, "SYNTHETIC_TOKEN", environmentCanary)
	b := portableSyntheticSensitiveBundle(t, source, id)
	destination, dir := portableSecretFixture(t)
	guest := &portableSecretNoGuest{}
	destination.store.guestBlobs = guest
	active := portableSyntheticEnvironment(t, destination, "SYNTHETIC_TOKEN", "Synthetic destination active value")
	before, _ := os.ReadFile(filepath.Join(dir, "secrets", "vault.json"))
	fileBefore, _ := os.Stat(filepath.Join(dir, "secrets", "vault.json"))
	if _, err := destination.secretVault.reveal(source.secretVault.records[id]); err == nil {
		t.Fatal("source key accepted by different destination key")
	}
	p, err := destination.store.previewPortable(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Environment) != 1 || p.Environment[0].Status != "inactive_recovery" || p.EnvironmentBytes != len(environmentCanary) || len(p.Conflicts) != 1 {
		t.Fatal("redacted inactive preview incomplete")
	}
	previewJSON, _ := json.Marshal(p)
	if bytes.Contains(previewJSON, []byte(environmentCanary)) || bytes.Contains(previewJSON, []byte(portableDigest(b))) || bytes.Contains(previewJSON, []byte(`"value"`)) {
		t.Fatal("preview exposed value or digest")
	}
	var digest, dest string
	destination.store.db.QueryRow(`SELECT digest,destination FROM portability_imports WHERE id=?`, p.ID).Scan(&digest, &dest)
	if !strings.HasPrefix(digest, "sealed-v3:") || digest == portableDigest(b) || dest != "sealed-v3" {
		t.Fatal("bare sensitive digest persisted")
	}
	result, err := destination.store.applyPortable(context.Background(), b, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if guest.calls != 0 {
		t.Fatal("secret import touched Guest")
	}
	target := result.IDMap[id]
	if !portableID(target) || target == id || result.Counts[portableEnvironmentCategory] != 1 {
		t.Fatal("recovery IDs/count wrong")
	}
	if len(destination.secretVault.records) != 1 || destination.secretVault.records[target].ID != "" || destination.secretVault.records[active].ID != active {
		t.Fatal("recovery entered active vault")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "secrets", "vault.json"))
	fileAfter, _ := os.Stat(filepath.Join(dir, "secrets", "vault.json"))
	if !bytes.Equal(before, after) || !fileBefore.ModTime().Equal(fileAfter.ModTime()) {
		t.Fatal("import wrote active vault")
	}
	var capsule []byte
	destination.store.db.QueryRow(`SELECT capsule FROM portability_secret_recovery WHERE id=?`, target).Scan(&capsule)
	if bytes.Contains(capsule, []byte(environmentCanary)) {
		t.Fatal("clear recovery persisted")
	}
	for _, name := range []string{"tofi.db", "tofi.db-wal"} {
		data, _ := os.ReadFile(filepath.Join(dir, name))
		if bytes.Contains(data, []byte(environmentCanary)) || bytes.Contains(data, []byte(portableDigest(b))) {
			t.Fatal("canary or bare digest persisted in database")
		}
	}
	receipt, _ := json.Marshal(result)
	if bytes.Contains(receipt, []byte(environmentCanary)) || bytes.Contains(receipt, []byte(`"value"`)) {
		t.Fatal("receipt exposed value")
	}
	for _, table := range []string{"runs", "schedules", "portability_provenance", "portability_asset_staging"} {
		var n int
		if err = destination.store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatal("recovery introduced execution, activation or plaintext provenance")
		}
	}
	destination.store.Close()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	destination.store = st
	st.portabilitySecrets = &portableSecretCodec{vault: destination.secretVault, account: destination.instance.ID}
	replayed, err := st.applyPortable(context.Background(), b, p.ID)
	if err != nil || !reflect.DeepEqual(result, replayed) {
		t.Fatal("restart retry receipt changed")
	}
	var n int
	st.db.QueryRow(`SELECT COUNT(*) FROM portability_secret_recovery`).Scan(&n)
	if n != 1 {
		t.Fatal("retry duplicated recovery")
	}
	reexport, err := destination.exportPortable(context.Background(), destination.instance.ID, portableSelection{Categories: []string{"bot_config", portableEnvironmentCategory}, RecoveredEnvironmentIDs: []string{target}}, "account")
	if err != nil || len(reexport.VaultEnvironment) != 1 || reexport.VaultEnvironment[0].Value != environmentCanary || reexport.VaultEnvironment[0].Origin.RecordID != id || reexport.VaultEnvironment[0].SourceCategory != "inactive_recovery" {
		t.Fatal("inactive re-export lost exact bytes/provenance")
	}
	metadata, err := st.portableEnvironmentInventory(context.Background())
	if err != nil || len(metadata) != 2 {
		t.Fatal("recovery inventory wrong")
	}
	list, _ := json.Marshal(metadata)
	if bytes.Contains(list, []byte(environmentCanary)) {
		t.Fatal("inventory exposed value")
	}
	w := sensitiveRequest(destination, http.MethodDelete, "portability/recovery/"+target, nil)
	if w.Code != 200 || len(destination.secretVault.records) != 1 {
		t.Fatal("recovery deletion changed active data")
	}
	if _, err = destination.exportPortable(context.Background(), destination.instance.ID, portableSelection{Categories: []string{"bot_config", portableEnvironmentCategory}, RecoveredEnvironmentIDs: []string{target}}, "account"); err == nil {
		t.Fatal("deleted recovery exported")
	}
}
func TestPortableEnvironmentExcludedSourcesAndLimits(t *testing.T) {
	s, _ := portableSecretFixture(t)
	good := portableSyntheticEnvironment(t, s, "SYNTHETIC_TOKEN", environmentCanary)
	for _, kind := range []string{"run", "conversation", "bot", "ssh", "provider", "empty"} {
		r := s.secretVault.records[good]
		r.ID = uuid.NewString()
		switch kind {
		case "run":
			r.RunID = uuid.NewString()
		case "conversation":
			r.ConversationID = uuid.NewString()
		case "bot":
			r.BotID = uuid.NewString()
		case "ssh", "provider":
			r.Kind = kind
		case "empty":
			r.Ciphertext = nil
		}
		s.secretVault.records[r.ID] = r
		if _, err := s.exportPortable(context.Background(), s.instance.ID, portableSelection{Categories: []string{"bot_config", portableEnvironmentCategory}, VaultEnvironmentIDs: []string{r.ID}}, "account"); err == nil {
			t.Fatal("excluded source accepted")
		}
	}
	b := portableSyntheticSensitiveBundle(t, s, good)
	for _, kind := range []string{"value_size", "total_size", "record_count", "invalid_utf8", "nul", "env_target", "kind", "future_version", "global_id_collision"} {
		copy := cloneAssetBundle(t, b)
		x := copy.VaultEnvironment[0]
		switch kind {
		case "value_size":
			copy.VaultEnvironment[0].Value = strings.Repeat("x", 65537)
		case "invalid_utf8":
			copy.VaultEnvironment[0].Value = string([]byte{0xff})
		case "nul":
			copy.VaultEnvironment[0].Value = "a\x00b"
		case "env_target":
			copy.VaultEnvironment[0].Target = "LD_PRELOAD"
		case "kind":
			copy.VaultEnvironment[0].Kind = "ssh"
		case "future_version":
			copy.Version = 4
		case "total_size", "record_count":
			n := 17
			if kind == "record_count" {
				n = 65
			}
			copy.VaultEnvironment = nil
			for i := 0; i < n; i++ {
				x.ID = uuid.NewString()
				if kind == "total_size" {
					x.Value = strings.Repeat("x", 65536)
				}
				copy.VaultEnvironment = append(copy.VaultEnvironment, x)
			}
		case "global_id_collision":
			legacy, _ := parsePortableBundle([]byte(portableLegacyFixture))
			copy.Bots = legacy.Bots
			copy.Conversations = legacy.Conversations
			copy.VaultEnvironment[0].ID = copy.Bots[0].ID
		}
		copy.Counts = copy.counts()
		if copy.validate() == nil {
			t.Fatalf("invalid %s accepted", kind)
		}
	}
	raw, _ := json.Marshal(b)
	if bytes.Contains(raw, []byte(`"ciphertext"`)) || bytes.Contains(raw, []byte("secrets/key")) || bytes.Contains(raw, []byte(`"run_id"`)) {
		t.Fatal("archive included live vault bindings/key material")
	}
}
func TestPortableEnvironmentPreviewBindingAndUnavailableKey(t *testing.T) {
	for _, change := range []string{"value", "selection", "destination", "key", "account", "expired", "foreign_preview", "capsule_id", "capsule_account", "capsule_purpose", "capsule_corrupt"} {
		t.Run(change, func(t *testing.T) {
			source, _ := portableSecretFixture(t)
			id := portableSyntheticEnvironment(t, source, "SYNTHETIC_TOKEN", environmentCanary)
			b := portableSyntheticSensitiveBundle(t, source, id)
			dest, _ := portableSecretFixture(t)
			p, err := dest.store.previewPortable(context.Background(), b)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(change, "capsule_") {
				result, err := dest.store.applyPortable(context.Background(), b, p.ID)
				if err != nil {
					t.Fatal(err)
				}
				target := result.IDMap[id]
				var capsule []byte
				dest.store.db.QueryRow(`SELECT capsule FROM portability_secret_recovery WHERE id=?`, target).Scan(&capsule)
				switch change {
				case "capsule_id":
					if _, err = dest.store.portabilitySecrets.open("recovery", uuid.NewString(), capsule); err == nil {
						t.Fatal("UUID binding bypass")
					}
				case "capsule_account":
					codec := *dest.store.portabilitySecrets
					codec.account = uuid.NewString()
					if _, err = codec.open("recovery", target, capsule); err == nil {
						t.Fatal("account binding bypass")
					}
				case "capsule_purpose":
					if _, err = dest.store.portabilitySecrets.open("preview", target, capsule); err == nil {
						t.Fatal("purpose binding bypass")
					}
				case "capsule_corrupt":
					capsule[len(capsule)-1] ^= 1
					if _, err = dest.store.portabilitySecrets.open("recovery", target, capsule); err == nil {
						t.Fatal("corruption accepted")
					}
				}
				return
			}
			switch change {
			case "value":
				b.VaultEnvironment[0].Value += "changed"
			case "selection":
				b, _ = selectPortable(b, portableSelection{})
			case "destination":
				portableSyntheticEnvironment(t, dest, "SYNTHETIC_OTHER", "synthetic destination change")
			case "key":
				dest.store.portabilitySecrets = nil
			case "account":
				dest.store.portabilitySecrets.account = uuid.NewString()
			case "expired":
				dest.store.db.Exec(`UPDATE portability_imports SET expires_at=0 WHERE id=?`, p.ID)
			case "foreign_preview":
				other, _ := portableSecretFixture(t)
				dest = other
			}
			if _, err = dest.store.applyPortable(context.Background(), b, p.ID); err == nil {
				t.Fatal("changed/foreign preview accepted")
			}
			var n int
			dest.store.db.QueryRow(`SELECT COUNT(*) FROM portability_secret_recovery`).Scan(&n)
			if n != 0 {
				t.Fatal("failed preview published recovery")
			}
		})
	}
	source, _ := portableSecretFixture(t)
	id := portableSyntheticEnvironment(t, source, "SYNTHETIC_TOKEN", environmentCanary)
	b := portableSyntheticSensitiveBundle(t, source, id)
	dest, _ := portableSecretFixture(t)
	dest.store.portabilitySecrets = nil
	if _, err := dest.store.previewPortable(context.Background(), b); err == nil {
		t.Fatal("missing key preview accepted")
	}
}
func TestPortableEnvironmentSQLRollbackAndRecoveryQuota(t *testing.T) {
	for _, fault := range []string{"sql", "rows", "bytes", "cancel"} {
		t.Run(fault, func(t *testing.T) {
			source, _ := portableSecretFixture(t)
			id := portableSyntheticEnvironment(t, source, "SYNTHETIC_TOKEN", environmentCanary)
			source.store.CreateBot("Synthetic source", "inert", "synthetic-model")
			b := portableSyntheticSensitiveBundle(t, source, id)
			dest, _ := portableSecretFixture(t)
			p, err := dest.store.previewPortable(context.Background(), b)
			if err != nil {
				t.Fatal(err)
			}
			expected := 0
			switch fault {
			case "sql":
				dest.store.db.Exec(`CREATE TRIGGER synthetic_environment_failure BEFORE INSERT ON workspace_events BEGIN SELECT RAISE(ABORT,'synthetic storage failure'); END`)
			case "rows", "bytes":
				n := portableMaxRecoveryRecords
				size := 1
				if fault == "bytes" {
					n = 64
					size = 65536
				}
				for i := 0; i < n; i++ {
					_, err = dest.store.db.Exec(`INSERT INTO portability_secret_recovery(id,import_id,source_id,capsule,size,created_at) VALUES(?,?,?,?,?,?)`, uuid.NewString(), uuid.NewString(), uuid.NewString(), []byte("synthetic-quota-row"), size, now())
					if err != nil {
						t.Fatal(err)
					}
				}
				expected = n
			}
			if fault == "rows" || fault == "bytes" {
				if _, e := dest.store.previewPortable(context.Background(), b); e == nil {
					t.Fatal("recovery capacity preview accepted")
				}
			}
			ctx := context.Background()
			if fault == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, err = dest.store.applyPortable(ctx, b, p.ID); err == nil {
				t.Fatal("fault accepted")
			}
			var n int
			dest.store.db.QueryRow(`SELECT COUNT(*) FROM portability_secret_recovery`).Scan(&n)
			if n != expected {
				t.Fatal("partial recovery publication")
			}
			assertNoPortableRows(t, dest.store)
		})
	}
}
func TestPortableEnvironmentSensitiveTransportAndResponseLock(t *testing.T) {
	source, _ := portableSecretFixture(t)
	id := portableSyntheticEnvironment(t, source, "SYNTHETIC_TOKEN", environmentCanary)
	b := portableSyntheticSensitiveBundle(t, source, id)
	raw, _ := json.Marshal(b)
	body, _ := json.Marshal(portableImportRequest{Bundle: raw, Selection: portableSensitiveSelection(b)})
	for _, fault := range []string{"unauthenticated", "unauthenticated_owner", "insecure", "foreign_origin", "missing_origin", "cross_site"} {
		r := httptest.NewRequest("POST", "https://fixture.test/api/portability/preview", bytes.NewReader(body))
		r.Header.Set("Origin", "https://fixture.test")
		if fault != "unauthenticated" && fault != "unauthenticated_owner" {
			r = r.WithContext(context.WithValue(r.Context(), portableAccountAuthKey{}, true))
		}
		switch fault {
		case "unauthenticated_owner":
			source.ownerAuth = &ownerAuth{store: source.store}
		case "insecure":
			r.TLS = nil
		case "foreign_origin":
			r.Header.Set("Origin", "https://foreign.test")
		case "missing_origin":
			r.Header.Del("Origin")
		case "cross_site":
			r.Header.Set("Sec-Fetch-Site", "cross-site")
		}
		w := httptest.NewRecorder()
		source.routePortability(w, r, "portability/preview")
		source.ownerAuth = nil
		if w.Code != 401 && w.Code != 403 {
			t.Fatal("sensitive ingress accepted", fault, w.Code)
		}
	}
	w := &reviewBlockedPortableWriter{ResponseRecorder: httptest.NewRecorder(), entered: make(chan struct{}), release: make(chan struct{})}
	r := httptest.NewRequest("POST", "https://fixture.test/api/portability/preview", bytes.NewReader(body))
	r.Header.Set("Origin", "https://fixture.test")
	r = r.WithContext(context.WithValue(r.Context(), portableAccountAuthKey{}, true))
	done := make(chan struct{})
	go func() { defer close(done); source.routePortability(w, r, "portability/preview") }()
	defer func() { close(w.release); <-done }()
	select {
	case <-w.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("no response")
	}
	if w.Code != 200 {
		t.Fatal("preview failed")
	}
	available := make(chan struct{})
	go func() {
		source.mu.Lock()
		source.mu.Unlock()
		source.secretVault.mu.Lock()
		source.secretVault.mu.Unlock()
		var n int
		source.store.db.QueryRow(`SELECT COUNT(*) FROM bots`).Scan(&n)
		close(available)
	}()
	select {
	case <-available:
	case <-time.After(300 * time.Millisecond):
		t.Error("response retained runtime/vault/database lock")
	}
}
func TestPortableEnvironmentAccountSessionIsolation(t *testing.T) {
	g := accountFixture(t)
	g.runtimeFactory = func(c Config) (*Server, error) { c.Engine = testEngine{}; return NewServer(c) }
	a, err := g.create(context.Background(), "synthetic-owner", "", "SyntheticOwnerOnly123!", true)
	if err != nil {
		t.Fatal(err)
	}
	other, err := g.create(context.Background(), "synthetic-other", "", "SyntheticOtherOnly123!", false)
	if err != nil {
		t.Fatal(err)
	}
	g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0 WHERE id=?`, other.ID)
	s, err := g.workspace(a)
	if err != nil {
		t.Fatal(err)
	}
	id := portableSyntheticEnvironment(t, s, "SYNTHETIC_TOKEN", environmentCanary)
	cookie := accountCookie(t, g, a)
	otherCookie := accountCookie(t, g, other)
	request := func(method, path string, body []byte, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://fixture.test"+path, bytes.NewReader(body))
		r.TLS = &tls.ConnectionState{}
		r.Header.Set("Origin", "https://fixture.test")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		g.Handler().ServeHTTP(w, r)
		return w
	}
	body, _ := json.Marshal(struct {
		Selection portableSelection `json:"selection"`
		Kind      string            `json:"kind"`
	}{portableSelection{Categories: []string{"bot_config", portableEnvironmentCategory}, VaultEnvironmentIDs: []string{id}}, "account"})
	w := request("POST", "/api/portability/export", body, cookie)
	if w.Code != 200 {
		t.Fatal("owner export failed", w.Code)
	}
	b, err := parsePortableBundle(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if w = request("POST", "/api/portability/export", body, otherCookie); w.Code == 200 || bytes.Contains(w.Body.Bytes(), []byte(environmentCanary)) {
		t.Fatal("foreign account export")
	}
	forged := bytes.Replace(body, []byte(`"kind":"account"`), []byte(`"kind":"account","account_id":"`+other.ID+`"`), 1)
	if w = request("POST", "/api/portability/export", forged, cookie); w.Code != 400 {
		t.Fatal("forged account override accepted")
	}
	raw, _ := json.Marshal(b)
	previewBody, _ := json.Marshal(portableImportRequest{Bundle: raw, Selection: portableSensitiveSelection(b)})
	w = request("POST", "/api/portability/preview", previewBody, cookie)
	if w.Code != 200 {
		t.Fatal("owner preview failed", w.Code)
	}
	var p portablePreview
	json.Unmarshal(w.Body.Bytes(), &p)
	applyBody, _ := json.Marshal(portableImportRequest{Bundle: raw, Selection: portableSensitiveSelection(b), PreviewID: p.ID})
	if w = request("POST", "/api/portability/apply", applyBody, otherCookie); w.Code != 409 {
		t.Fatal("foreign preview applied", w.Code)
	}
	w = request("POST", "/api/portability/apply", applyBody, cookie)
	if w.Code != 200 {
		t.Fatal("owner apply failed", w.Code)
	}
	var result portableResult
	if json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatal("invalid receipt")
	}
	target := result.IDMap[id]
	w = request("GET", "/api/portability/environment", nil, cookie)
	if w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte(environmentCanary)) || !bytes.Contains(w.Body.Bytes(), []byte("inactive_recovery")) {
		t.Fatal("recovery list was not redacted/inactive")
	}
	w = request("GET", "/api/portability/environment", nil, otherCookie)
	if w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte(target)) || bytes.Contains(w.Body.Bytes(), []byte("SYNTHETIC_TOKEN")) {
		t.Fatal("foreign recovery list exposed source metadata")
	}
	if w = request("DELETE", "/api/portability/recovery/"+target, nil, otherCookie); w.Code != 404 {
		t.Fatal("foreign recovery deletion accepted")
	}
	reexportBody, _ := json.Marshal(struct {
		Selection portableSelection `json:"selection"`
		Kind      string            `json:"kind"`
	}{portableSelection{Categories: []string{"bot_config", portableEnvironmentCategory}, RecoveredEnvironmentIDs: []string{target}}, "account"})
	w = request("POST", "/api/portability/export", reexportBody, cookie)
	if w.Code != 200 {
		t.Fatal("inactive API re-export failed")
	}
	recovered, err := parsePortableBundle(w.Body.Bytes())
	if err != nil || len(recovered.VaultEnvironment) != 1 || recovered.VaultEnvironment[0].Value != environmentCanary || recovered.VaultEnvironment[0].SourceCategory != "inactive_recovery" {
		t.Fatal("inactive API re-export lost exact value")
	}
	if w = request("DELETE", "/api/portability/recovery/"+target, nil, cookie); w.Code != 200 || s.secretVault.records[id].ID != id {
		t.Fatal("owner recovery deletion changed active vault")
	}
	g.root.store.db.Exec(`UPDATE accounts SET disabled=1 WHERE id=?`, a.ID)
	if w = request("GET", "/api/portability/environment", nil, cookie); w.Code != 401 {
		t.Fatal("disabled session read recovery")
	}
	g.root.store.db.Exec(`UPDATE accounts SET disabled=0 WHERE id=?`, a.ID)
	request("POST", "/api/auth/logout", nil, cookie)
	if w = request("GET", "/api/portability/environment", nil, cookie); w.Code != 401 {
		t.Fatal("logged-out session read recovery")
	}
}

func TestPortableEnvironmentCombinedAttachmentAtomicity(t *testing.T) {
	source, _, _ := portableAssetFixture(t)
	vault, err := initializeSecretVault(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	account := uuid.NewString()
	source.portabilitySecrets = &portableSecretCodec{vault: vault, account: account}
	server := &Server{store: source, secretVault: vault, instance: instanceIdentity{ID: account}}
	id := portableSyntheticEnvironment(t, server, "SYNTHETIC_TOKEN", environmentCanary)
	cats := append(append([]string(nil), portableDefaultCategories...), portableEnvironmentCategory)
	b, err := server.exportPortable(context.Background(), account, portableSelection{Categories: cats, VaultEnvironmentIDs: []string{id}}, "account")
	if err != nil || b.Version != 3 || len(b.Attachments) != 2 {
		t.Fatal("combined v3 export failed", err)
	}
	selected, err := selectPortable(b, portableSensitiveSelection(b))
	if err != nil {
		t.Fatal(err)
	}
	destination, _ := portableSecretFixture(t)
	backend := newPortableFixtureBlobs()
	destination.store.guestBlobs = backend
	p, err := destination.store.previewPortable(context.Background(), selected)
	if err != nil {
		t.Fatal(err)
	}
	codec := destination.store.portabilitySecrets
	destination.store.portabilitySecrets = nil
	if _, err = destination.store.applyPortable(context.Background(), selected, p.ID); err == nil || backend.puts != 0 {
		t.Fatal("unavailable key reached Guest staging")
	}
	destination.store.portabilitySecrets = codec
	if _, err = destination.store.db.Exec(`CREATE TRIGGER synthetic_combined_failure BEFORE INSERT ON workspace_events BEGIN SELECT RAISE(ABORT,'synthetic combined failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = destination.store.applyPortable(context.Background(), selected, p.ID); err == nil {
		t.Fatal("combined transaction fault accepted")
	}
	assertNoPortableRows(t, destination.store)
	var n int
	destination.store.db.QueryRow(`SELECT COUNT(*) FROM portability_secret_recovery`).Scan(&n)
	if n != 0 || backend.aliases() != 0 {
		t.Fatal("combined rollback leaked capsules/bytes")
	}
	destination.store.db.Exec(`DROP TRIGGER synthetic_combined_failure`)
	result, err := destination.applyPortableDefaults(context.Background(), selected, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := destination.store.portableRecovery(context.Background(), result.IDMap[id])
	if err != nil || recovered.Value != environmentCanary {
		t.Fatal("combined recovery lost value")
	}
	destination.store.db.QueryRow(`SELECT COUNT(*) FROM schedules WHERE status!='paused'`).Scan(&n)
	if n != 0 {
		t.Fatal("combined import activated schedules")
	}
	beforePuts := backend.puts
	replay, err := destination.applyPortableDefaults(context.Background(), selected, p.ID)
	if err != nil || !reflect.DeepEqual(result, replay) || backend.puts != beforePuts {
		t.Fatal("combined retry restaged files or changed receipt")
	}
}

func TestPortableEnvironmentCredentialOnlyDoesNotRunGuestMaintenance(t *testing.T) {
	source, _ := portableSecretFixture(t)
	id := portableSyntheticEnvironment(t, source, "SYNTHETIC_TOKEN", environmentCanary)
	b := portableSyntheticSensitiveBundle(t, source, id)
	destination, _ := portableSecretFixture(t)
	guest := &portableSecretNoGuest{}
	destination.store.guestBlobs = guest
	if _, err := destination.store.db.Exec(`INSERT INTO portability_asset_staging(import_id,target_id,sha256,size,created_at) VALUES(?,?,?,?,?)`, uuid.NewString(), uuid.NewString(), portableBytesHash([]byte("Synthetic orphan")), len("Synthetic orphan"), now()); err != nil {
		t.Fatal(err)
	}
	p, err := destination.store.previewPortable(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = destination.store.applyPortable(context.Background(), b, p.ID); err != nil || guest.calls != 0 {
		t.Fatal("credential-only import used Guest maintenance")
	}
	var n int
	destination.store.db.QueryRow(`SELECT COUNT(*) FROM portability_asset_staging`).Scan(&n)
	if n != 1 {
		t.Fatal("unrelated asset journal was discarded")
	}
}
