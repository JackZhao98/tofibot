package app

// The organization's File storage resource (design 4.2, 5.2, 8.2): one
// S3-compatible bucket per server, configured by an administrator. The Access
// key ID and secret live in the organization vault as one sealed value; the
// non-secret half is the resource config. A workspace gets a FileBackend (a
// store handle and a key layout), never the credentials.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
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
	return FileBackend{Kind: kind, Store: g.fileCache.store, Gen: cfg.Gen, Prefix: FileStoreKeyPrefix(cfg.Prefix, g.root.instance.ID, accountID), Account: accountID, GraceDays: cfg.DeleteGraceDays}, nil
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

// objectRowsAnywhere counts `obj:` rows in every account's workspace.
func (g *AccountGateway) objectRowsAnywhere(ctx context.Context) (int, error) {
	rows, err := g.root.store.db.QueryContext(ctx, `SELECT id,username,email,role,disabled,legacy,deleting FROM accounts`)
	if err != nil {
		return 0, err
	}
	var accounts []Account
	for rows.Next() {
		var a Account
		if err = rows.Scan(&a.ID, &a.Username, &a.Email, &a.Role, &a.Disabled, &a.Legacy, &a.Deleting); err != nil {
			rows.Close()
			return 0, err
		}
		accounts = append(accounts, a)
	}
	rows.Close()
	total := 0
	for _, a := range accounts {
		ws, err := g.workspace(a)
		if err != nil {
			return 0, err
		}
		n, err := ws.store.objectRows()
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

// deleteAccountFiles is the `files` step of account deletion: every object under
// the account's prefix, no grace. Without a bucket there is nothing to remove.
func (g *AccountGateway) deleteAccountFiles(ctx context.Context, accountID string) error {
	b, err := g.fileBackendFor(ctx, accountID)
	if err != nil {
		return err
	}
	if b.Store == nil {
		return nil
	}
	var keys []string
	if err = b.Store.List(ctx, b.Prefix, func(o filestore.ObjectInfo) error { keys = append(keys, o.Key); return nil }); err != nil {
		return err
	}
	for _, k := range keys {
		if err = b.Store.Delete(ctx, k); err != nil {
			return err
		}
	}
	return nil
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

func (g *AccountGateway) putOrgFileStore(w http.ResponseWriter, r *http.Request, sp orgResourceSpec) {
	var in fileStoreIn
	if decodeStrict(r, orgBodyLimit, &in) != nil {
		writeErr(w, 400, "invalid_request", "invalid request")
		return
	}
	ctx := r.Context()
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
	g.orgWriteMu.Lock()
	defer g.orgWriteMu.Unlock()
	// "disk" turns new files back to this server's disk and keeps the bucket
	// readable: rows already in it are still read from it.
	if in.Kind == fileStoreKindDisk {
		if !found {
			writeErr(w, 404, "not_found", "resource not found")
			return
		}
		cfg.Kind = fileStoreKindDisk
		g.saveFileStoreConfig(w, ctx, old, cfg)
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
		cfg.Gen = "g1"
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
	if found && !cfg.sameLocation(prev) {
		n, err := g.objectRowsAnywhere(ctx)
		if err != nil {
			storageFailure(w, "count stored files", err)
			return
		}
		if n > 0 {
			writeErr(w, 409, "file_store_in_use", "Files are stored in the current bucket. Its address cannot change until they are moved.")
			return
		}
	}
	if res := g.verifyFileStore(ctx, cfg, accessKey, secretKey); !res.ok {
		writeErr(w, res.status, res.code, res.msg)
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
	prevRec, existed, err := v.put(rec, accessKey+"\x00"+secretKey)
	if err == nil {
		if err = g.saveOrgResource(ctx, row); err != nil {
			v.restore(rec.ID, prevRec, existed)
		}
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
