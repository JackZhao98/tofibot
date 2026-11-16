package app

// The organization's File storage resource (design 4.2, 5.2, 8.2): one
// S3-compatible bucket per server, configured by an administrator. The Access
// key ID and secret live in the organization vault as one sealed value; the
// non-secret half is the resource config. A workspace gets a FileBackend (a
// store handle and a key layout), never the credentials.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JackZhao98/tofibot/internal/filestore"
)

const (
	orgFileStoreID         = "file_store"
	orgFileStoreKind       = "file_store"
	defaultFileStorePrefix = "tofi/"
)

var (
	fileStoreBucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	fileStorePrefixPattern = regexp.MustCompile(`^[a-z0-9._/-]{0,100}$`)
)

// fileStoreConfig is the resource's non-secret configuration.
type fileStoreConfig struct {
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	Prefix          string `json:"prefix"`
	PathStyle       bool   `json:"path_style"`
	Gen             string `json:"gen"`
	Kind            string `json:"kind"` // where new files go: "s3" or "disk"
	DeleteGraceDays int    `json:"delete_grace_days"`
	PublicOK        bool   `json:"public_ok,omitempty"`
	// Prev are the earlier bucket generations, readable until their files are moved.
	Prev []fileStoreGen `json:"prev,omitempty"`
}

// fileStoreGen is an earlier generation: where it is. Its access key is sealed in the
// organization vault under fileStoreGenVaultID(Gen).
type fileStoreGen struct {
	Gen        string `json:"gen"`
	Endpoint   string `json:"endpoint"`
	Region     string `json:"region"`
	Bucket     string `json:"bucket"`
	Prefix     string `json:"prefix"`
	PathStyle  bool   `json:"path_style"`
	ReplacedAt string `json:"replaced_at"`
}

func fileStoreGenVaultID(gen string) string { return orgFileStoreID + ":" + gen }

func (g fileStoreGen) config() fileStoreConfig {
	return fileStoreConfig{Endpoint: g.Endpoint, Region: g.Region, Bucket: g.Bucket, Prefix: g.Prefix, PathStyle: g.PathStyle, Gen: g.Gen}
}

func (c fileStoreConfig) asGen(at string) fileStoreGen {
	return fileStoreGen{Gen: prevGenName(c), Endpoint: c.Endpoint, Region: c.Region, Bucket: c.Bucket, Prefix: c.Prefix, PathStyle: c.PathStyle, ReplacedAt: at}
}

// nextGen is the generation after every one this configuration has used.
func (c fileStoreConfig) nextGen() string {
	max := 1
	names := []string{c.Gen}
	for _, p := range c.Prev {
		names = append(names, p.Gen)
	}
	for _, g := range names {
		if n, err := strconv.Atoi(strings.TrimPrefix(g, "g")); err == nil && n > max {
			max = n
		}
	}
	return "g" + strconv.Itoa(max+1)
}

func (c fileStoreConfig) s3(accessKey, secret string) filestore.S3Config {
	return filestore.S3Config{Endpoint: c.Endpoint, Region: c.Region, Bucket: c.Bucket, AccessKeyID: accessKey, Secret: secret, PathStyle: c.PathStyle}
}

func (c fileStoreConfig) sameLocation(o fileStoreConfig) bool {
	return c.Endpoint == o.Endpoint && c.Region == o.Region && c.Bucket == o.Bucket && c.Prefix == o.Prefix && c.PathStyle == o.PathStyle
}

// fileStoreCache keeps the client built from one stored version of the resource.
type fileStoreCache struct {
	mu        sync.Mutex
	updatedAt string
	store     filestore.Store
	old       map[string]filestore.Store
}

// fileBackendFor is the resolver a workspace calls on each file operation.
func (g *AccountGateway) fileBackendFor(ctx context.Context, accountID string) (FileBackend, error) {
	row, found, err := g.getOrgResource(ctx, orgFileStoreID)
	if err != nil {
		return FileBackend{}, err
	}
	if !found {
		return FileBackend{Kind: fileStoreKindDisk}, nil
	}
	var cfg fileStoreConfig
	if json.Unmarshal([]byte(row.Config), &cfg) != nil {
		return FileBackend{}, errors.New("file storage configuration unreadable")
	}
	g.fileCache.mu.Lock()
	defer g.fileCache.mu.Unlock()
	if g.fileCache.store == nil || g.fileCache.updatedAt != row.UpdatedAt {
		g.fileCache.old = nil
		secret, err := g.orgCredential(ctx, orgFileStoreID)
		if err != nil {
			return FileBackend{}, err
		}
		accessKey, key, _ := strings.Cut(secret.Value, "\x00")
		s3, err := filestore.NewS3(cfg.s3(accessKey, key))
		if err != nil {
			return FileBackend{}, err
		}
		g.fileCache.store, g.fileCache.updatedAt = watchedStore{Store: s3, g: g}, row.UpdatedAt
	}
	kind := cfg.Kind
	if kind != fileStoreKindDisk {
		kind = fileStoreKindS3
	}
	stamp := row.UpdatedAt
	old := func(want string) (filestore.Store, error) { return g.oldFileStore(cfg, stamp, want) }
	return FileBackend{Kind: kind, Store: g.fileCache.store, Gen: prevGenName(cfg), Prefix: FileStoreKeyPrefix(cfg.Prefix, g.root.instance.ID, accountID), Account: accountID, GraceDays: cfg.DeleteGraceDays, Old: old}, nil
}

// oldFileStore opens a previous generation with its own sealed key. It is not watched:
// an earlier bucket failing does not mark the current resource rejected.
func (g *AccountGateway) oldFileStore(cfg fileStoreConfig, stamp, gen string) (filestore.Store, error) {
	g.fileCache.mu.Lock()
	defer g.fileCache.mu.Unlock()
	if st, ok := g.fileCache.old[gen]; ok && g.fileCache.updatedAt == stamp {
		return st, nil
	}
	for _, p := range cfg.Prev {
		if p.Gen != gen {
			continue
		}
		v, err := g.orgSecrets()
		if err != nil {
			return nil, err
		}
		got, ok := secretSource(v).secret(fileStoreGenVaultID(gen), orgFileStoreKind)
		if !ok {
			return nil, errOrgResourceMissing
		}
		accessKey, key, _ := strings.Cut(got.Value, "\x00")
		st, err := filestore.NewS3(p.config().s3(accessKey, key))
		if err != nil {
			return nil, err
		}
		if g.fileCache.updatedAt == stamp {
			if g.fileCache.old == nil {
				g.fileCache.old = map[string]filestore.Store{}
			}
			g.fileCache.old[gen] = st
		}
		return st, nil
	}
	return nil, errOrgResourceMissing
}

func (g *AccountGateway) wireFileStore(c *Config, a Account) {
	id := a.ID
	c.FileStore = func(ctx context.Context) (FileBackend, error) { return g.fileBackendFor(ctx, id) }
}

// watchedStore marks the resource as failing when the bucket refuses or cannot
// be reached, so the administrator sees it on the card.
type watchedStore struct {
	filestore.Store
	g *AccountGateway
}

func (w watchedStore) note(err error) error {
	switch {
	case errors.Is(err, filestore.ErrDenied):
		go w.g.recordOrgRejection(orgFileStoreID, "credentials_rejected")
	case errors.Is(err, filestore.ErrUnavailable):
		go w.g.recordOrgRejection(orgFileStoreID, "endpoint_unreachable")
	}
	return err
}

func (w watchedStore) Put(ctx context.Context, key string, src io.ReadSeeker, m filestore.ObjectMeta) error {
	return w.note(w.Store.Put(ctx, key, src, m))
}

func (w watchedStore) Open(ctx context.Context, key string) (io.ReadSeekCloser, filestore.ObjectInfo, error) {
	r, info, err := w.Store.Open(ctx, key)
	return r, info, w.note(err)
}

func (w watchedStore) Stat(ctx context.Context, key string) (filestore.ObjectInfo, error) {
	info, err := w.Store.Stat(ctx, key)
	return info, w.note(err)
}

func (w watchedStore) Delete(ctx context.Context, key string) error {
	return w.note(w.Store.Delete(ctx, key))
}

func (w watchedStore) List(ctx context.Context, prefix string, fn func(filestore.ObjectInfo) error) error {
	return w.note(w.Store.List(ctx, prefix, fn))
}

// deleteAccountFiles is the `files` step of account deletion: every object under the
// account's prefix in the current bucket and in every earlier generation, no grace. A
// generation that cannot be reached fails the step by name: the account's files must not
// be left behind in a bucket nobody looked at. Without a bucket there is nothing to remove.
func (g *AccountGateway) deleteAccountFiles(ctx context.Context, accountID string) error {
	b, err := g.fileBackendFor(ctx, accountID)
	if err != nil {
		return err
	}
	if b.Store == nil {
		return nil
	}
	type target struct {
		st     filestore.Store
		prefix string
	}
	targets := []target{{b.Store, b.Prefix}}
	if row, found, rerr := g.getOrgResource(ctx, orgFileStoreID); rerr != nil {
		return rerr
	} else if found {
		var cfg fileStoreConfig
		_ = json.Unmarshal([]byte(row.Config), &cfg)
		for _, p := range cfg.Prev {
			st, oerr := b.Old(p.Gen)
			if oerr != nil || st == nil {
				return newFileStoreError(codeFileStoreMisconfigured, errors.New("earlier bucket not available"))
			}
			targets = append(targets, target{st, FileStoreKeyPrefix(p.Prefix, g.root.instance.ID, accountID)})
		}
	}
	for _, t := range targets {
		var keys []string
		if err = t.st.List(ctx, t.prefix, func(o filestore.ObjectInfo) error { keys = append(keys, o.Key); return nil }); err != nil {
			return fileStoreFailure(err)
		}
		for _, k := range keys {
			if err = t.st.Delete(ctx, k); err != nil {
				return fileStoreFailure(err)
			}
		}
	}
	return nil
}

// aliasedGeneration writes a probe object through the new configuration and looks for it
// through the current generation and every earlier one. It returns the generation that can
// read it (the same physical bucket under another name), or "" when none can. A generation
// that is gone (no such bucket) is not the same bucket; one that cannot be asked (denied,
// unreachable) leaves the answer unverified, and the caller decides whether to go on.
func (g *AccountGateway) aliasedGeneration(ctx context.Context, cfg, prev fileStoreConfig, accessKey, secret string) (alias string, unverified bool, err error) {
	s3, err := filestore.NewS3(cfg.s3(accessKey, secret))
	if err != nil {
		return "", false, err
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	key := cfg.Prefix + fileStoreLayoutV1 + "/" + g.root.instance.ID + "/_probe/" + newID()
	payload := []byte("tofi-alias-probe")
	if err = s3.Put(ctx, key, bytes.NewReader(payload), filestore.ObjectMeta{Size: int64(len(payload)), SHA256: "probe", SafeMIME: "application/octet-stream"}); err != nil {
		return "", false, err
	}
	defer deleteProbe(s3, key)
	gens := append([]fileStoreGen{prev.asGen("")}, prev.Prev...)
	for _, c := range gens {
		st, oerr := g.openGenStore(prev, c)
		if oerr != nil {
			unverified = true
			continue
		}
		switch g.probeAnswer(ctx, st, c.Gen, key) {
		case probeVisible:
			return c.Gen, false, nil
		case probeAbsent:
		default:
			unverified = true
		}
	}
	return "", unverified, nil
}

// openGenStore builds a client for one generation straight from the vault (no cache).
func (g *AccountGateway) openGenStore(cfg fileStoreConfig, c fileStoreGen) (filestore.Store, error) {
	id := fileStoreGenVaultID(c.Gen)
	if c.Gen == prevGenName(cfg) {
		id = orgFileStoreID
	}
	v, err := g.orgSecrets()
	if err != nil {
		return nil, err
	}
	got, ok := secretSource(v).secret(id, orgFileStoreKind)
	if !ok {
		return nil, errOrgResourceMissing
	}
	accessKey, key, _ := strings.Cut(got.Value, "\x00")
	return filestore.NewS3(c.config().s3(accessKey, key))
}

// fileStoreIn is the PUT body. A blank key pair keeps the stored one only while
// the endpoint and bucket are unchanged, so a stored key never goes to another host.
type fileStoreIn struct {
	Kind            string  `json:"kind"`
	Endpoint        string  `json:"endpoint"`
	Region          string  `json:"region"`
	Bucket          string  `json:"bucket"`
	Prefix          *string `json:"prefix"`
	PathStyle       bool    `json:"path_style"`
	AccessKeyID     string  `json:"access_key_id"`
	Key             string  `json:"key"`
	DeleteGraceDays int     `json:"delete_grace_days"`
	ConfirmPublic   bool    `json:"confirm_public"`
	// ConfirmUnverified accepts a new bucket although an earlier one could not be reached to compare.
	ConfirmUnverified bool `json:"confirm_unverified"`
}

func fileStoreFail(code, msg string) orgVerifyResult {
	status, reject := 400, true
	switch code {
	case "endpoint_unreachable":
		status, reject = 502, false
	case "bucket_is_public":
		reject = false
	}
	return orgVerifyFailed(status, code, msg, reject)
}

var fileStoreProbeMessages = map[string]string{
	"endpoint_unreachable":        "The endpoint did not answer. Check the endpoint address and that the server can reach it.",
	"credentials_rejected":        "The bucket rejected the access key.",
	"bucket_not_found":            "The bucket does not exist at this endpoint.",
	"write_denied":                "The access key cannot write to this bucket.",
	"conditional_put_unsupported": "The endpoint does not support write-once puts (If-None-Match).",
	"range_unsupported":           "The endpoint does not support ranged reads.",
	"delete_denied":               "The access key cannot delete objects in this bucket.",
	"bucket_is_public":            "The bucket answers reads with no credentials. Make it private, or confirm to keep it.",
}

// verifyFileStore runs the connection test (design 8.2) with the given key.
func (g *AccountGateway) verifyFileStore(ctx context.Context, cfg fileStoreConfig, accessKey, secret string) orgVerifyResult {
	s3, err := filestore.NewS3(cfg.s3(accessKey, secret))
	if err != nil {
		return fileStoreFail("endpoint_unreachable", fileStoreProbeMessages["endpoint_unreachable"])
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	probeKey := cfg.Prefix + fileStoreLayoutV1 + "/" + g.root.instance.ID + "/_probe/" + newID()
	public, err := filestore.Probe(ctx, s3, probeKey)
	var pe *filestore.ProbeError
	if errors.As(err, &pe) {
		return fileStoreFail(pe.Code, fileStoreProbeMessages[pe.Code])
	}
	if err != nil {
		return fileStoreFail("endpoint_unreachable", fileStoreProbeMessages["endpoint_unreachable"])
	}
	if public && !cfg.PublicOK {
		return fileStoreFail("bucket_is_public", fileStoreProbeMessages["bucket_is_public"])
	}
	return orgVerifyResult{ok: true}
}

// fileStoreBeforeLock lets a test change the resource between the network work and the save.
var fileStoreBeforeLock func()

func (g *AccountGateway) putOrgFileStore(w http.ResponseWriter, r *http.Request, sp orgResourceSpec) {
	var in fileStoreIn
	if decodeStrict(r, orgBodyLimit, &in) != nil {
		writeErr(w, 400, "invalid_request", "invalid request")
		return
	}
	ctx := r.Context()
	// The network work (connection test, same-bucket probe) happens before the lock; the lock is then
	// taken, the resource re-read, and the save aborted if it changed in the meantime.
	old, found, err := g.getOrgResource(ctx, sp.ID)
	if err != nil {
		storageFailure(w, "read resource", err)
		return
	}
	var cfg, prev fileStoreConfig
	if found {
		_ = json.Unmarshal([]byte(old.Config), &prev)
		cfg = prev
	}
	// "disk" turns new files back to this server's disk and keeps the bucket
	// readable: rows already in it are still read from it.
	if in.Kind == fileStoreKindDisk {
		g.orgWriteMu.Lock()
		defer g.orgWriteMu.Unlock()
		cur, ok, rerr := g.getOrgResource(ctx, sp.ID)
		if rerr != nil {
			storageFailure(w, "read resource", rerr)
			return
		}
		if !ok {
			writeErr(w, 404, "not_found", "resource not found")
			return
		}
		var current fileStoreConfig
		_ = json.Unmarshal([]byte(cur.Config), &current)
		current.Kind = fileStoreKindDisk
		g.saveFileStoreConfig(w, ctx, cur, current)
		return
	}
	if in.Kind != "" && in.Kind != fileStoreKindS3 {
		writeErr(w, 400, "invalid_request", "kind is s3 or disk")
		return
	}
	cfg.Kind = fileStoreKindS3
	if in.Endpoint != "" || !found {
		cfg.Endpoint = strings.TrimRight(strings.TrimSpace(in.Endpoint), "/")
		cfg.Region = strings.TrimSpace(in.Region)
		cfg.Bucket = strings.TrimSpace(in.Bucket)
		cfg.PathStyle = in.PathStyle
		cfg.Prefix = defaultFileStorePrefix
		if in.Prefix != nil {
			cfg.Prefix = strings.Trim(strings.TrimSpace(*in.Prefix), "/")
			if cfg.Prefix != "" {
				cfg.Prefix += "/"
			}
		}
		if cfg.Region == "" {
			cfg.Region = "auto"
		}
		u, uerr := url.Parse(cfg.Endpoint)
		if uerr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" {
			writeErr(w, 400, "invalid_endpoint", "Enter the endpoint as https://host (no path).")
			return
		}
		if !fileStoreBucketPattern.MatchString(cfg.Bucket) {
			writeErr(w, 400, "invalid_bucket", "Enter the bucket name.")
			return
		}
		if !fileStorePrefixPattern.MatchString(cfg.Prefix) {
			writeErr(w, 400, "invalid_prefix", "The prefix takes lowercase letters, digits, dots, dashes and slashes.")
			return
		}
	}
	if cfg.Gen == "" {
		cfg.Gen = firstFileStoreGen
	}
	switch {
	case in.DeleteGraceDays != 0:
		cfg.DeleteGraceDays = in.DeleteGraceDays
	case cfg.DeleteGraceDays == 0:
		cfg.DeleteGraceDays = defaultDeleteGraceDay
	}
	if cfg.DeleteGraceDays < 1 || cfg.DeleteGraceDays > 365 {
		writeErr(w, 400, "invalid_grace", "Deleted files are kept 1 to 365 days.")
		return
	}
	cfg.PublicOK = in.ConfirmPublic
	accessKey, secretKey := strings.TrimSpace(in.AccessKeyID), strings.TrimSpace(in.Key)
	if accessKey == "" && secretKey == "" {
		if !found || !cfg.sameLocation(prev) {
			writeErr(w, 400, "invalid_key", "Enter the access key ID and secret.")
			return
		}
		stored, err := g.orgCredential(ctx, sp.ID)
		if err != nil {
			writeErr(w, 400, "invalid_key", "Enter the access key ID and secret.")
			return
		}
		accessKey, secretKey, _ = strings.Cut(stored.Value, "\x00")
	}
	if len(accessKey) < 3 || len(accessKey) > 256 || strings.ContainsAny(accessKey, " \t\r\n\x00") || !validSecretKey(secretKey, 1024) {
		writeErr(w, 400, "invalid_key", "Enter the full access key ID and secret.")
		return
	}
	// A different bucket, endpoint or prefix is a migration, not an edit: the new config becomes the
	// next generation (an empty one is simply retirable) and the current one stays readable. Whether
	// it really is another bucket is decided by looking, not by comparing strings: a probe written
	// through the new config that an existing generation can read is the same physical bucket.
	newGen, verified := false, false
	if found && !cfg.sameLocation(prev) {
		for _, p := range prev.Prev {
			if cfg.sameLocation(p.config()) {
				writeErr(w, 409, "file_store_location_in_use", "That bucket is already an earlier bucket. Move its files or retire it first.")
				return
			}
		}
		if res := g.verifyFileStore(ctx, cfg, accessKey, secretKey); !res.ok {
			writeErr(w, res.status, res.code, res.msg)
			return
		}
		verified = true
		alias, unverified, aerr := g.aliasedGeneration(ctx, cfg, prev, accessKey, secretKey)
		if aerr != nil {
			writeErr(w, 502, "endpoint_unreachable", fileStoreProbeMessages["endpoint_unreachable"])
			return
		}
		switch {
		case alias == "" && unverified && !in.ConfirmUnverified:
			writeErr(w, 409, "file_store_cannot_compare", "An earlier bucket could not be reached, so it cannot be checked that the new one is a different bucket. Confirm to save anyway.")
			return
		case alias == "":
			newGen = true
		case alias != prevGenName(prev):
			writeErr(w, 409, "file_store_location_in_use", "That bucket is already an earlier bucket. Move its files or retire it first.")
			return
		case cfg.Prefix != prev.Prefix:
			// The same bucket under another name: the address is an edit, but a prefix change would
			// leave the account-scoped listing of the old prefix behind while files live there.
			usage, uerr := g.fileStoreUsage(ctx, true)
			if uerr != nil {
				storageFailure(w, "count stored files", uerr)
				return
			}
			if u := usage[alias]; u.Files+u.Pending+u.Staging > 0 {
				writeErr(w, 409, "file_store_same_bucket", "This is the bucket already in use. Its prefix cannot change while files are stored in it.")
				return
			}
		}
		if newGen {
			cfg.Prev = append(append([]fileStoreGen{}, prev.Prev...), prev.asGen(now()))
			cfg.Gen = prev.nextGen()
		}
	}
	if !verified {
		if res := g.verifyFileStore(ctx, cfg, accessKey, secretKey); !res.ok {
			writeErr(w, res.status, res.code, res.msg)
			return
		}
	}
	if fileStoreBeforeLock != nil {
		fileStoreBeforeLock()
	}
	g.orgWriteMu.Lock()
	defer g.orgWriteMu.Unlock()
	if cur, ok, rerr := g.getOrgResource(ctx, sp.ID); rerr != nil {
		storageFailure(w, "read resource", rerr)
		return
	} else if ok != found || (found && cur.UpdatedAt != old.UpdatedAt) {
		writeErr(w, 409, "file_store_changed", "File storage was changed while you were saving. Reload and try again.")
		return
	}
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		storageFailure(w, "encode config", err)
		return
	}
	row := orgResourceRow{ID: sp.ID, Kind: sp.Kind, Config: string(cfgJSON), KeyHint: orgKeyHint(accessKey), KeyPrefix: orgKeyPrefix(accessKey), Status: orgStatusVerified, VerifiedAt: sql.NullString{String: now(), Valid: true}, UpdatedAt: now()}
	rec := secretRecord{ID: sp.ID, Name: sp.Name, Kind: sp.Kind, Target: sp.Target, Label: orgKeyHint(accessKey), Status: "stored", CreatedAt: now(), VerifiedAt: row.VerifiedAt.String}
	v, err := g.orgSecrets()
	if err != nil {
		storageFailure(w, "open organization vault", err)
		return
	}
	var oldRec secretRecord
	var oldExisted bool
	oldID := ""
	if newGen {
		// The current key moves to the generation it belongs to before the new one takes its place.
		stored, serr := g.orgCredential(ctx, sp.ID)
		if serr != nil {
			storageFailure(w, "read stored key", serr)
			return
		}
		oldID = fileStoreGenVaultID(prevGenName(prev))
		oldRec, oldExisted, err = v.put(secretRecord{ID: oldID, Name: sp.Name, Kind: sp.Kind, Target: sp.Target, Label: old.KeyHint, Status: "stored", CreatedAt: now()}, stored.Value)
		if err != nil {
			storageFailure(w, "store resource", err)
			return
		}
	}
	prevRec, existed, err := v.put(rec, accessKey+"\x00"+secretKey)
	if err == nil {
		if err = g.saveOrgResource(ctx, row); err != nil {
			v.restore(rec.ID, prevRec, existed)
		}
	}
	if err != nil && oldID != "" {
		v.restore(oldID, oldRec, oldExisted)
	}
	if err != nil {
		storageFailure(w, "store resource", err)
		return
	}
	writeJSON(w, 200, row.view())
}

// saveFileStoreConfig rewrites the config of a stored resource without touching its key.
func (g *AccountGateway) saveFileStoreConfig(w http.ResponseWriter, ctx context.Context, row orgResourceRow, cfg fileStoreConfig) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		storageFailure(w, "encode config", err)
		return
	}
	row.Config, row.UpdatedAt = string(raw), now()
	if err = g.saveOrgResource(ctx, row); err != nil {
		storageFailure(w, "store resource", err)
		return
	}
	writeJSON(w, 200, row.view())
}

// generationQuietPeriod is how long after a switch an earlier generation stays unretirable.
var generationQuietPeriod = time.Hour

func prevGenName(c fileStoreConfig) string {
	if c.Gen == "" {
		return firstFileStoreGen
	}
	return c.Gen
}

// fileStoreUsage sums, over every account, the rows and pending removals of each bucket
// generation. Strict: an account whose workspace cannot be opened is an error (a retire or a
// new generation must not be decided on partial counts); otherwise it is skipped.
func (g *AccountGateway) fileStoreUsage(ctx context.Context, strict bool) (map[string]genUsage, error) {
	accounts, err := g.fileJobAccounts(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]genUsage{}
	for _, a := range accounts {
		var st *Store
		if strict {
			ws, werr := g.workspace(a)
			if werr != nil {
				return nil, werr
			}
			st = ws.store
		} else {
			var code string
			if st, code = g.fileJobStore(a); code != "" {
				continue
			}
		}
		per, err := st.objectUsageByGen()
		if err != nil {
			return nil, err
		}
		for gen, u := range per {
			t := out[gen]
			t.Files, t.Bytes, t.Pending, t.Staging = t.Files+u.Files, t.Bytes+u.Bytes, t.Pending+u.Pending, t.Staging+u.Staging
			if u.ClearsAt > t.ClearsAt {
				t.ClearsAt = u.ClearsAt
			}
			out[gen] = t
		}
	}
	return out, nil
}

// fileStoreRemoveBlock says why the file store cannot be removed yet, over every generation; code is "" when it can.
func fileStoreRemoveBlock(usage map[string]genUsage) (code, msg, clearsAt string) {
	var files, pending, staging bool
	for _, u := range usage {
		files = files || u.Files > 0
		pending = pending || u.Pending > 0
		staging = staging || u.Staging > 0
		if u.Pending > 0 && u.ClearsAt > clearsAt {
			clearsAt = u.ClearsAt
		}
	}
	switch {
	case files:
		return "file_store_in_use", "Files are stored in this bucket. It cannot be removed until they are moved.", ""
	case pending:
		return "file_store_cleanup_pending", "Deleted files are still waiting out their grace period in the bucket. It can be removed after they are cleaned up.", clearsAt
	case staging:
		return "file_store_busy", "An import into the bucket is still waiting to finish. Try again later.", ""
	}
	return "", "", ""
}

// retireFileStoreGen deletes an earlier generation's key and entry once nothing refers to it.
func (g *AccountGateway) retireFileStoreGen(ctx context.Context, gen string) (code, msg string, status int, err error) {
	g.orgWriteMu.Lock()
	defer g.orgWriteMu.Unlock()
	row, found, err := g.getOrgResource(ctx, orgFileStoreID)
	if err != nil {
		return "", "", 0, err
	}
	var cfg fileStoreConfig
	if found {
		_ = json.Unmarshal([]byte(row.Config), &cfg)
	}
	idx := -1
	for i, p := range cfg.Prev {
		if p.Gen == gen {
			idx = i
		}
	}
	if idx < 0 {
		return "generation_not_found", "There is no earlier bucket to retire.", 404, nil
	}
	usage, err := g.fileStoreUsage(ctx, true)
	if err != nil {
		return "", "", 0, err
	}
	switch u := usage[gen]; {
	case u.Files > 0:
		return "file_store_generation_in_use", "Files are still stored in this bucket. Move them to the current bucket first.", 409, nil
	case u.Pending > 0:
		return "generation_cleanup_pending", "Deleted files are still waiting out their grace period in this bucket.", 409, nil
	case u.Staging > 0:
		return "generation_busy", "An import to this bucket is still waiting to finish.", 409, nil
	}
	// An upload that wrote to this bucket just before the switch may still be inserting its row.
	if at, perr := time.Parse(time.RFC3339, cfg.Prev[idx].ReplacedAt); perr != nil || time.Since(at) < generationQuietPeriod {
		return "generation_busy", "This bucket was replaced a moment ago. Uploads that were in progress may still finish into it. Try again in an hour.", 409, nil
	}
	v, err := g.orgSecrets()
	if err != nil {
		return "", "", 0, err
	}
	cfg.Prev = append(append([]fileStoreGen{}, cfg.Prev[:idx]...), cfg.Prev[idx+1:]...)
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", "", 0, err
	}
	row.Config, row.UpdatedAt = string(raw), now()
	if err = g.saveOrgResource(ctx, row); err != nil {
		return "", "", 0, err
	}
	// The entry is gone first: a failed key removal leaves an unreachable sealed value, never a dangling generation.
	return "", "", 0, v.remove(fileStoreGenVaultID(gen))
}
