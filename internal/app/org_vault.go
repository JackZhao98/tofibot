package app

// The organization vault (design 3.6). Organization secrets live in
// <data>/org/secrets, opened with the same secretVault code as a workspace but
// in a directory no workspace and not the root Server ever uses: the legacy
// owner's workspace shares the root's DataDir and so its <data>/secrets (risk
// S1), and deleting, renaming or demoting an account never touches <data>/org.
// Record ids and kinds match the workspace vaults ("provider:openai",
// "webtool:tavily", "computer:e2b"), so a later reader can use one secretSource
// interface for both. Organization secrets are write-only: no response returns
// plaintext, only key_prefix and key_hint, and not even an admin can read one back.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

const (
	orgDirName      = "org"
	orgWebToolsID   = "web_tools"
	orgWebToolsKind = "web_tools"

	orgStatusStored   = "stored"
	orgStatusVerified = "verified"
	orgStatusRejected = "rejected"
)

// secretValue is one revealed record.
type secretValue struct {
	Value  string
	Record secretRecord
}

// secretSource is the read side shared by workspace vaults and the
// organization vault: a record of this id and kind, revealed, or none.
type secretSource interface {
	secret(id, kind string) (secretValue, bool)
}

func (v *secretVault) secret(id, kind string) (secretValue, bool) {
	v.mu.Lock()
	rec, ok := v.records[id]
	v.mu.Unlock()
	if !ok || rec.Kind != kind || rec.RunID != "" {
		return secretValue{}, false
	}
	value, err := v.reveal(rec)
	if err != nil || value == "" {
		return secretValue{}, false
	}
	return secretValue{Value: value, Record: rec}, true
}

// put seals and stores one record, returning the previous one so a caller can
// undo the write when its own bookkeeping fails.
func (v *secretVault) put(rec secretRecord, value string) (prev secretRecord, existed bool, err error) {
	if rec.Ciphertext, err = v.seal(rec.ID, value); err != nil {
		return secretRecord{}, false, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	prev, existed = v.records[rec.ID]
	v.records[rec.ID] = rec
	if err = v.saveLocked(); err != nil {
		v.restoreLocked(rec.ID, prev, existed)
		return secretRecord{}, false, err
	}
	return prev, existed, nil
}

func (v *secretVault) restoreLocked(id string, prev secretRecord, existed bool) {
	if existed {
		v.records[id] = prev
	} else {
		delete(v.records, id)
	}
}

// restore puts a record back after a failed follow-up write (best effort).
func (v *secretVault) restore(id string, prev secretRecord, existed bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.restoreLocked(id, prev, existed)
	_ = v.saveLocked()
}

func (v *secretVault) remove(id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	prev, existed := v.records[id]
	if !existed {
		return nil
	}
	delete(v.records, id)
	if err := v.saveLocked(); err != nil {
		v.records[id] = prev
		return err
	}
	return nil
}

// orgSecrets opens the organization vault on first use. It is the only place
// that decides where the vault lives.
func (g *AccountGateway) orgSecrets() (*secretVault, error) {
	g.orgInitMu.Lock()
	defer g.orgInitMu.Unlock()
	if g.orgVault != nil {
		return g.orgVault, nil
	}
	v, err := initializeSecretVault(filepath.Join(g.config.DataDir, orgDirName))
	if err != nil {
		return nil, err
	}
	g.orgVault = v
	return v, nil
}

// orgResourceSpec names one organization resource the admin may store.
type orgResourceSpec struct {
	ID, Kind, Target string
	// Name is the vault record's display name.
	Name string
	// Zone: the resource takes a zone (Bright Data).
	Zone bool
}

var orgResourceSpecs = []orgResourceSpec{
	{ID: providerVaultID(providerAnthropic), Kind: modelProviderKind, Target: providerAnthropic, Name: providerLabel(providerAnthropic) + " API key"},
	{ID: providerVaultID(providerOpenAI), Kind: modelProviderKind, Target: providerOpenAI, Name: providerLabel(providerOpenAI) + " API key"},
	{ID: providerVaultID(providerCompat), Kind: modelProviderKind, Target: providerCompat, Name: "OpenAI-compatible endpoint"},
	{ID: e2bKeyVaultID, Kind: computerProviderKind, Target: envKindE2B, Name: "E2B API key"},
	{ID: webVaultID("tavily"), Kind: webProviderKind, Target: "tavily", Name: "Tavily API key"},
	{ID: webVaultID("brave"), Kind: webProviderKind, Target: "brave", Name: "Brave Search API key"},
	{ID: webVaultID("firecrawl"), Kind: webProviderKind, Target: "firecrawl", Name: "Firecrawl API key"},
	{ID: webVaultID("brightdata"), Kind: webProviderKind, Target: "brightdata", Name: "Bright Data API key", Zone: true},
}

func orgSpecFor(id string) (orgResourceSpec, bool) {
	for _, sp := range orgResourceSpecs {
		if sp.ID == id {
			return sp, true
		}
	}
	return orgResourceSpec{}, false
}

// orgCredential reads one organization secret for the gateway's own use (it
// leaves the process only inside an outbound request). A resource that is not
// stored is errOrgResourceMissing: callers do not fall back to a member's key.
func (g *AccountGateway) orgCredential(ctx context.Context, resource string) (orgSecret, error) {
	sp, ok := orgSpecFor(resource)
	if !ok {
		return orgSecret{}, errOrgResourceMissing
	}
	if err := ctx.Err(); err != nil {
		return orgSecret{}, err
	}
	v, err := g.orgSecrets()
	if err != nil {
		return orgSecret{}, err
	}
	var src secretSource = v
	got, ok := src.secret(sp.ID, sp.Kind)
	if !ok {
		return orgSecret{}, errOrgResourceMissing
	}
	return orgSecret{ID: sp.ID, Kind: sp.Kind, Value: got.Value, Hint: got.Record.Label}, nil
}

// orgResourceRow is one org_resources row.
type orgResourceRow struct {
	ID, Kind, Config, KeyHint, KeyPrefix, Status, ErrorCode, UpdatedAt string
	VerifiedAt                                                         sql.NullString
}

const orgResourceColumns = `id,kind,config,key_hint,key_prefix,status,verified_at,error_code,updated_at`

func scanOrgResource(sc interface{ Scan(...any) error }) (orgResourceRow, error) {
	var r orgResourceRow
	err := sc.Scan(&r.ID, &r.Kind, &r.Config, &r.KeyHint, &r.KeyPrefix, &r.Status, &r.VerifiedAt, &r.ErrorCode, &r.UpdatedAt)
	return r, err
}

func (g *AccountGateway) getOrgResource(ctx context.Context, id string) (orgResourceRow, bool, error) {
	r, err := scanOrgResource(g.root.store.db.QueryRowContext(ctx, `SELECT `+orgResourceColumns+` FROM org_resources WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return orgResourceRow{}, false, nil
	}
	return r, err == nil, err
}

func (g *AccountGateway) listOrgResources(ctx context.Context) ([]orgResourceRow, error) {
	rows, err := g.root.store.db.QueryContext(ctx, `SELECT `+orgResourceColumns+` FROM org_resources WHERE kind<>? ORDER BY id`, orgWebToolsKind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []orgResourceRow
	for rows.Next() {
		r, err := scanOrgResource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (g *AccountGateway) saveOrgResource(ctx context.Context, r orgResourceRow) error {
	_, err := g.root.store.db.ExecContext(ctx, `INSERT INTO org_resources(`+orgResourceColumns+`) VALUES(?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET kind=excluded.kind,config=excluded.config,key_hint=excluded.key_hint,key_prefix=excluded.key_prefix,status=excluded.status,verified_at=excluded.verified_at,error_code=excluded.error_code,updated_at=excluded.updated_at`,
		r.ID, r.Kind, r.Config, r.KeyHint, r.KeyPrefix, r.Status, r.VerifiedAt, r.ErrorCode, r.UpdatedAt)
	return err
}

// orgResourceView is the only public shape of a stored resource: non-secret
// state and the masked ends of the key. There is no field for the key itself.
type orgResourceView struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	Status     string          `json:"status"`
	KeyPrefix  string          `json:"key_prefix,omitempty"`
	KeyHint    string          `json:"key_hint,omitempty"`
	VerifiedAt string          `json:"verified_at,omitempty"`
	ErrorCode  string          `json:"error_code,omitempty"`
	UpdatedAt  string          `json:"updated_at"`
	Config     json.RawMessage `json:"config"`
	// Build is the template of the organization's E2B computer (computer:e2b only).
	Build *orgE2BBuildView `json:"build,omitempty"`
}

func (r orgResourceRow) view() orgResourceView {
	v := orgResourceView{ID: r.ID, Kind: r.Kind, Status: r.Status, KeyPrefix: r.KeyPrefix, KeyHint: r.KeyHint, ErrorCode: r.ErrorCode, UpdatedAt: r.UpdatedAt, Config: json.RawMessage(r.Config)}
	if r.VerifiedAt.Valid {
		v.VerifiedAt = r.VerifiedAt.String
	}
	if !json.Valid(v.Config) {
		v.Config = json.RawMessage("{}")
	}
	v.Build = orgE2BViewOf(r)
	return v
}

// primaryKey is the key part of a sealed value ("key\x00workspace" and
// "key\x00zone" carry a second field).
func primaryKey(value string) string {
	k, _, _ := strings.Cut(value, "\x00")
	return k
}
