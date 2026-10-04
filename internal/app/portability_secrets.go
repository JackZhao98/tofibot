package app

// Recovery is SQL-only, separate from the active vault and its apply routes.
// The codec reuses the account vault AEAD; no source key or ciphertext travels.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"
)

const portableEnvironmentCategory = "vault_environment"
const portableMaxEnvironmentRecords = 64
const portableMaxEnvironmentBytes = 1 << 20
const portableMaxRecoveryRecords = 256
const portableMaxRecoveryBytes = 4 << 20

var errPortableSecret = errors.New("Selected environment recovery is unavailable; check selection, storage and preview again")

type portableSecretOrigin struct {
	InstanceID string `json:"instance_id"`
	RecordID   string `json:"record_id"`
}
type portableEnvironment struct {
	ID             string               `json:"id"`
	Name           string               `json:"name"`
	Kind           string               `json:"kind"`
	Target         string               `json:"target"`
	CreatedAt      string               `json:"created_at"`
	Value          string               `json:"value"`
	SourceCategory string               `json:"source_category"`
	Origin         portableSecretOrigin `json:"origin"`
}

// Deliberately not embedded: public metadata can never serialize Value/Origin.
type portableEnvironmentMetadata struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Target         string `json:"target"`
	SourceCategory string `json:"source_category"`
	Status         string `json:"status"`
	Bytes          int    `json:"bytes,omitempty"`
}
type portableSecretCodec struct {
	vault         *secretVault
	account       string
	allowLoopback bool
}
type portableAccountAuthKey struct{}

func (c *portableSecretCodec) available() bool {
	return c != nil && c.vault != nil && c.vault.aead != nil && c.account != ""
}
func (c *portableSecretCodec) seal(purpose, id string, data []byte) ([]byte, error) {
	if !c.available() {
		return nil, errPortableSecret
	}
	return c.vault.seal("tofi.portability:"+purpose+":1:"+c.account+":"+id, string(data))
}
func (c *portableSecretCodec) open(purpose, id string, data []byte) ([]byte, error) {
	if !c.available() {
		return nil, errPortableSecret
	}
	value, err := c.vault.reveal(secretRecord{ID: "tofi.portability:" + purpose + ":1:" + c.account + ":" + id, Ciphertext: data})
	if err != nil {
		return nil, errPortableSecret
	}
	return []byte(value), nil
}
func portableEnvironmentPublic(x portableEnvironment) portableEnvironmentMetadata {
	return portableEnvironmentMetadata{ID: x.ID, Name: x.Name, Target: x.Target, SourceCategory: x.SourceCategory, Status: "inactive_recovery", Bytes: len(x.Value)}
}
func (b portableBundle) validatePortableEnvironment(cat map[string]bool, ids map[string]bool) error {
	if b.Version != 3 {
		if cat[portableEnvironmentCategory] || len(b.VaultEnvironment) != 0 {
			return errPortableSecret
		}
		return nil
	}
	if b.Kind != "account" || !cat[portableEnvironmentCategory] || len(b.VaultEnvironment) == 0 || len(b.VaultEnvironment) > portableMaxEnvironmentRecords {
		return errPortableSecret
	}
	total := 0
	for _, x := range b.VaultEnvironment {
		if !portableID(x.ID) || ids[x.ID] || x.Kind != "env" || !allowedSecretEnv(x.Target) || len(x.Name) > 120 || !utf8.ValidString(x.Name) || !validSecretValue(x.Value) || !utf8.ValidString(x.Value) || !portableTime(x.CreatedAt) || (x.SourceCategory != "active_vault" && x.SourceCategory != "inactive_recovery") || len(x.Origin.InstanceID) > 200 || !utf8.ValidString(x.Origin.InstanceID) || !portableID(x.Origin.RecordID) {
			return errPortableSecret
		}
		ids[x.ID] = true
		total += len(x.Value)
	}
	if total > portableMaxEnvironmentBytes {
		return errPortableSecret
	}
	return nil
}
func selectPortableEnvironment(out *portableBundle, source portableBundle, sel portableSelection, enabled bool) error {
	out.VaultEnvironment = nil
	if len(sel.RecoveredEnvironmentIDs) > 0 {
		return errPortableSecret
	}
	if !enabled {
		if len(sel.VaultEnvironmentIDs) > 0 {
			return errPortableSecret
		}
	} else {
		wanted, err := portableExplicitIDs(sel.VaultEnvironmentIDs)
		if err != nil {
			return err
		}
		for _, x := range source.VaultEnvironment {
			if wanted[x.ID] {
				out.VaultEnvironment = append(out.VaultEnvironment, x)
				delete(wanted, x.ID)
			}
		}
		if len(wanted) != 0 {
			return errPortableSecret
		}
	}
	if len(out.VaultEnvironment) == 0 && out.Version == 3 {
		out.Version = 2
		if !containsPortable(out.Included, "attachments") && len(out.Attachments)+len(out.MissingAttachments) == 0 {
			out.Version = 1
		}
	}
	out.Excluded = portableEnvironmentExclusions(containsPortable(out.Included, "attachments"), len(out.VaultEnvironment) > 0)
	return nil
}
func containsPortable(xs []string, target string) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
}
func portableExplicitIDs(ids []string) (map[string]bool, error) {
	if len(ids) == 0 || len(ids) > portableMaxEnvironmentRecords {
		return nil, errPortableSecret
	}
	out := map[string]bool{}
	for _, id := range ids {
		if !portableID(id) || out[id] {
			return nil, errPortableSecret
		}
		out[id] = true
	}
	return out, nil
}
func portableEnvironmentExclusions(attachments, environment bool) []string {
	out := portableExclusions(attachments)
	if environment {
		for i, x := range out {
			if x == "credentials" {
				out[i] = "unselected_vault_environment_and_other_credentials"
			}
		}
	}
	return out
}
func (s *Store) portableRecovery(ctx context.Context, id string) (portableEnvironment, error) {
	var raw []byte
	var size int
	if err := s.db.QueryRowContext(ctx, `SELECT capsule,size FROM portability_secret_recovery WHERE id=?`, id).Scan(&raw, &size); err != nil {
		return portableEnvironment{}, errPortableSecret
	}
	clear, err := s.portabilitySecrets.open("recovery", id, raw)
	if err != nil {
		return portableEnvironment{}, err
	}
	var x portableEnvironment
	if portableJSON(clear, &x) != nil || x.ID != id || len(x.Value) != size {
		return x, errPortableSecret
	}
	b := portableBundle{Version: 3, Kind: "account", VaultEnvironment: []portableEnvironment{x}}
	if b.validatePortableEnvironment(map[string]bool{portableEnvironmentCategory: true}, map[string]bool{}) != nil {
		return x, errPortableSecret
	}
	x.SourceCategory = "inactive_recovery"
	return x, nil
}
func (s *Store) portableEnvironmentInventory(ctx context.Context) ([]portableEnvironmentMetadata, error) {
	if !s.portabilitySecrets.available() {
		return nil, errPortableSecret
	}
	c := s.portabilitySecrets
	c.vault.mu.Lock()
	list := []portableEnvironmentMetadata{}
	for _, r := range c.vault.records {
		if r.Kind == "env" && r.RunID == "" && r.ConversationID == "" && r.BotID == "" && r.Status == "stored" && len(r.Ciphertext) > 0 && portableID(r.ID) {
			list = append(list, portableEnvironmentMetadata{ID: r.ID, Name: r.Name, Target: r.Target, SourceCategory: "active_vault", Status: "stored"})
		}
	}
	c.vault.mu.Unlock()
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM portability_secret_recovery ORDER BY id LIMIT ?`, portableMaxRecoveryRecords+1)
	if err != nil {
		return nil, errPortableSecret
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil || len(ids) > portableMaxRecoveryRecords {
		return nil, errPortableSecret
	}
	for _, id := range ids {
		x, err := s.portableRecovery(ctx, id)
		if err != nil {
			return nil, err
		}
		list = append(list, portableEnvironmentPublic(x))
	}
	sort.Slice(list, func(i, j int) bool { return list[i].SourceCategory+list[i].ID < list[j].SourceCategory+list[j].ID })
	return list, nil
}
func (s *Server) exportPortable(ctx context.Context, instance string, sel portableSelection, kind string) (portableBundle, error) {
	enabled := containsPortable(sel.Categories, portableEnvironmentCategory)
	if !enabled {
		if len(sel.VaultEnvironmentIDs)+len(sel.RecoveredEnvironmentIDs) > 0 {
			return portableBundle{}, errPortableSecret
		}
		return s.store.exportPortable(ctx, instance, sel, kind)
	}
	cat, selectionErr := portableSet(sel.Categories, portableCategories)
	if selectionErr != nil || !cat["bot_config"] || (kind != "" && kind != "account") {
		return portableBundle{}, errPortableSecret
	}
	if kind == "bot" || !s.store.portabilitySecrets.available() || len(sel.VaultEnvironmentIDs)+len(sel.RecoveredEnvironmentIDs) == 0 || len(sel.VaultEnvironmentIDs)+len(sel.RecoveredEnvironmentIDs) > portableMaxEnvironmentRecords {
		return portableBundle{}, errPortableSecret
	}
	records := []portableEnvironment{}
	seen := map[string]bool{}
	c := s.store.portabilitySecrets
	// Copy under the vault mutex, release it before decrypting/SQLite/Guest I/O.
	active := []secretRecord{}
	c.vault.mu.Lock()
	for _, id := range sel.VaultEnvironmentIDs {
		r, ok := c.vault.records[id]
		if !ok || seen[id] || !portableID(id) || r.Kind != "env" || r.RunID != "" || r.ConversationID != "" || r.BotID != "" || r.Status != "stored" || len(r.Ciphertext) == 0 {
			c.vault.mu.Unlock()
			return portableBundle{}, errPortableSecret
		}
		seen[id] = true
		r.Ciphertext = bytes.Clone(r.Ciphertext)
		active = append(active, r)
	}
	c.vault.mu.Unlock()
	for _, r := range active {
		value, err := c.vault.reveal(r)
		if err != nil {
			return portableBundle{}, errPortableSecret
		}
		records = append(records, portableEnvironment{ID: r.ID, Name: r.Name, Kind: "env", Target: r.Target, CreatedAt: r.CreatedAt, Value: value, SourceCategory: "active_vault", Origin: portableSecretOrigin{InstanceID: instance, RecordID: r.ID}})
	}
	for _, id := range sel.RecoveredEnvironmentIDs {
		if !portableID(id) || seen[id] {
			return portableBundle{}, errPortableSecret
		}
		seen[id] = true
		x, err := s.store.portableRecovery(ctx, id)
		if err != nil {
			return portableBundle{}, err
		}
		records = append(records, x)
	}
	check := portableBundle{Version: 3, Kind: "account", VaultEnvironment: records}
	if err := check.validatePortableEnvironment(map[string]bool{portableEnvironmentCategory: true}, map[string]bool{}); err != nil {
		return portableBundle{}, err
	}
	base := sel
	base.Categories = nil
	base.VaultEnvironmentIDs = nil
	base.RecoveredEnvironmentIDs = nil
	for _, category := range sel.Categories {
		if category != portableEnvironmentCategory {
			base.Categories = append(base.Categories, category)
		}
	}
	b, err := s.store.exportPortable(ctx, instance, base, kind)
	if err != nil {
		return b, err
	}
	b.Version = 3
	b.Included = append(b.Included, portableEnvironmentCategory)
	sort.Strings(b.Included)
	b.VaultEnvironment = records
	b.Excluded = portableEnvironmentExclusions(containsPortable(b.Included, "attachments"), true)
	b.Counts = b.counts()
	if err = b.validate(); err != nil {
		return b, err
	}
	raw, err := json.Marshal(b)
	if err != nil || len(raw) > portableMaxBytes {
		return portableBundle{}, errPortableSecret
	}
	return b, nil
}
func portableRecoveryCapacity(ctx context.Context, tx *sql.Tx, b portableBundle) error {
	if len(b.VaultEnvironment) == 0 {
		return nil
	}
	var count, total int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(size),0) FROM portability_secret_recovery`).Scan(&count, &total); err != nil {
		return errPortableSecret
	}
	for _, x := range b.VaultEnvironment {
		total += len(x.Value)
	}
	if count+len(b.VaultEnvironment) > portableMaxRecoveryRecords || total > portableMaxRecoveryBytes {
		return errPortableSecret
	}
	return nil
}
func (s *Store) portableSecretDestination(ctx context.Context, tx *sql.Tx, base string) (string, error) {
	if !s.portabilitySecrets.available() {
		return "", errPortableSecret
	}
	c := s.portabilitySecrets
	c.vault.mu.Lock()
	snapshots := []secretRecord{}
	for _, r := range c.vault.records {
		r.Ciphertext = bytes.Clone(r.Ciphertext)
		snapshots = append(snapshots, r)
	}
	c.vault.mu.Unlock()
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].ID < snapshots[j].ID })
	raw, _ := json.Marshal(snapshots)
	h := sha256.New()
	h.Write([]byte(base))
	h.Write(raw)
	rows, err := tx.QueryContext(ctx, `SELECT id,capsule FROM portability_secret_recovery ORDER BY id LIMIT ?`, portableMaxRecoveryRecords+1)
	if err != nil {
		return "", errPortableSecret
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id string
		var capsule []byte
		if err = rows.Scan(&id, &capsule); err != nil {
			return "", errPortableSecret
		}
		count++
		encoded, _ := json.Marshal(struct {
			ID      string
			Capsule []byte
		}{id, capsule})
		h.Write(encoded)
	}
	if rows.Err() != nil || count > portableMaxRecoveryRecords {
		return "", errPortableSecret
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (s *Store) portableActiveTargets() (map[string]bool, error) {
	if !s.portabilitySecrets.available() {
		return nil, errPortableSecret
	}
	c := s.portabilitySecrets
	c.vault.mu.Lock()
	defer c.vault.mu.Unlock()
	targets := map[string]bool{}
	for _, r := range c.vault.records {
		if r.Kind == "env" && r.RunID == "" {
			targets[r.Target] = true
		}
	}
	return targets, nil
}
func (s *Store) portableBoundDigest(ctx context.Context, tx *sql.Tx, b portableBundle, id, base string) (string, string, error) {
	if len(b.VaultEnvironment) == 0 {
		return portableDigest(b), base, nil
	}
	dest, err := s.portableSecretDestination(ctx, tx, base)
	if err != nil {
		return "", "", err
	}
	raw, _ := json.Marshal(struct{ Bundle, Destination string }{portableDigest(b), dest})
	sealed, err := s.portabilitySecrets.seal("preview", id, raw)
	if err != nil {
		return "", "", err
	}
	return "sealed-v3:" + base64.StdEncoding.EncodeToString(sealed), "sealed-v3", nil
}
func (s *Store) portableCheckDigest(ctx context.Context, tx *sql.Tx, b portableBundle, id, digest, destination string, applied bool) error {
	if len(b.VaultEnvironment) == 0 {
		if digest != portableDigest(b) {
			return errPortableSecret
		}
		return nil
	}
	if !strings.HasPrefix(digest, "sealed-v3:") || destination != "sealed-v3" {
		return errPortableSecret
	}
	sealed, err := base64.StdEncoding.Strict().DecodeString(strings.TrimPrefix(digest, "sealed-v3:"))
	if err != nil {
		return errPortableSecret
	}
	clear, err := s.portabilitySecrets.open("preview", id, sealed)
	if err != nil {
		return err
	}
	var bound struct{ Bundle, Destination string }
	if json.Unmarshal(clear, &bound) != nil || bound.Bundle != portableDigest(b) {
		return errPortableSecret
	}
	if !applied {
		base, _, err := portableDestination(tx)
		if err != nil {
			return errPortableSecret
		}
		dest, err := s.portableSecretDestination(ctx, tx, base)
		if err != nil || dest != bound.Destination {
			return errPortableSecret
		}
	}
	return nil
}
func (s *Server) portableSensitiveRequest(w http.ResponseWriter, r *http.Request) bool {
	authenticated := r.Context().Value(portableAccountAuthKey{}) == true
	if s.ownerAuth != nil {
		_, _, authenticated = s.ownerAuth.session(r)
	}
	if !authenticated {
		writeErr(w, 401, "auth_required", "Owner sign-in required")
		return false
	}
	secure := r.TLS != nil
	if !secure && s.store.portabilitySecrets != nil && s.store.portabilitySecrets.allowLoopback {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		secure = err == nil && net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
	}
	if !secure {
		writeErr(w, 403, "secure_transport_required", "Use HTTPS or explicitly enabled loopback ingress")
		return false
	}
	if !s.originOK(r) || r.Header.Get("Sec-Fetch-Site") == "cross-site" || (r.Method != http.MethodGet && r.Header.Get("Origin") == "") {
		writeErr(w, 403, "csrf", "Same-origin request required")
		return false
	}
	return true
}
