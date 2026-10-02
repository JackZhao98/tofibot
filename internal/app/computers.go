package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"net/http"
	"path"
	"runtime"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/google/uuid"
)

const (
	computerPairTTL      = 5 * time.Minute
	computerOnlineWindow = 30 * time.Second
	computerJobTTL       = 60 * time.Second
	computerWaitTimeout  = 45 * time.Second
	maxComputerResult    = 1 << 20
	maxPendingPairings   = 8
	maxOutstandingJobs   = 32
)

var computerActions = map[string]bool{
	"files.list": true, "files.read": true, "files.write": true,
	"screen.capture": true, "desktop.click": true, "desktop.type": true, "desktop.key": true,
	"sandbox.exec": true,
}

type Computer struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Platform     string   `json:"platform"`
	Kind         string   `json:"kind"`
	Online       bool     `json:"online"`
	Capabilities []string `json:"capabilities"`
	LastSeen     string   `json:"last_seen,omitempty"`
}

type ComputerJob struct {
	ID        string          `json:"id"`
	DeviceID  string          `json:"device_id"`
	BotID     string          `json:"bot_id,omitempty"`
	RunID     string          `json:"run_id,omitempty"`
	Action    string          `json:"action"`
	Args      json.RawMessage `json:"args"`
	ExpiresAt string          `json:"expires_at"`
	Status    string          `json:"status,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// ComputerJobStatus is the device-facing cancellation snapshot. Do not expose
// action arguments, results or other devices' jobs through this endpoint.
type ComputerJobStatus struct {
	ID        string `json:"id"`
	DeviceID  string `json:"device_id"`
	Status    string `json:"status"`
	ExpiresAt string `json:"expires_at"`
	LeaseMS   int64  `json:"lease_ms"`
}

func migrateComputers(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS computer_pairings(
 code_hash BLOB PRIMARY KEY, created_at TEXT NOT NULL, expires_at TEXT NOT NULL, used_at TEXT
);
CREATE INDEX IF NOT EXISTS computer_pairings_created ON computer_pairings(created_at);
CREATE TABLE IF NOT EXISTS computers(
 id TEXT PRIMARY KEY, name TEXT NOT NULL, platform TEXT NOT NULL CHECK(platform='darwin'),
 kind TEXT NOT NULL DEFAULT 'mac' CHECK(kind='mac'), token_hash BLOB NOT NULL,
 capabilities TEXT NOT NULL, last_seen TEXT NOT NULL, created_at TEXT NOT NULL, revoked_at TEXT
);
CREATE TABLE IF NOT EXISTS computer_jobs(
 id TEXT PRIMARY KEY, device_id TEXT NOT NULL, bot_id TEXT NOT NULL DEFAULT '', run_id TEXT NOT NULL DEFAULT '',
 action TEXT NOT NULL, args TEXT NOT NULL, status TEXT NOT NULL,
 result TEXT, error TEXT, created_at TEXT NOT NULL, expires_at TEXT NOT NULL,
 claimed_at TEXT, completed_at TEXT
);
CREATE INDEX IF NOT EXISTS computer_jobs_device_status ON computer_jobs(device_id,status,created_at);
CREATE INDEX IF NOT EXISTS computer_jobs_run ON computer_jobs(run_id,status);
`)
	if err != nil {
		return err
	}
	if err = ensureColumn(db, "computer_pairings", "pairing_id", `ALTER TABLE computer_pairings ADD COLUMN pairing_id TEXT`); err != nil {
		return err
	}
	if err = ensureColumn(db, "computer_pairings", "device_id", `ALTER TABLE computer_pairings ADD COLUMN device_id TEXT`); err != nil {
		return err
	}
	_, err = db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS computer_pairings_id ON computer_pairings(pairing_id) WHERE pairing_id IS NOT NULL`)
	return err
}

// recoverComputerJobs is called after OpenStore has changed uncertain running
// parent runs to interrupted. Claimed device actions are never reissued.
func recoverComputerJobs(db *sql.DB) error {
	t := now()
	if _, err := db.Exec(`UPDATE computer_jobs SET status='interrupted',error='service restarted after claim',completed_at=? WHERE status='running'`, t); err != nil {
		return err
	}
	_, err := db.Exec(`UPDATE computer_jobs SET status='cancelled',error='parent run is no longer active',completed_at=?
 WHERE status='pending' AND run_id<>'' AND NOT EXISTS(SELECT 1 FROM runs WHERE runs.id=computer_jobs.run_id AND runs.status='running')`, t)
	return err
}

func randomSecret(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func secretHash(v string) []byte {
	h := sha256.Sum256([]byte(v))
	return h[:]
}

func decodeStrict(r *http.Request, max int64, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, max+1))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request must contain one JSON value")
	}
	return nil
}

func (s *Store) createComputerPairing() (string, string, error) {
	_, code, expires, err := s.createComputerPairingWithID()
	return code, expires, err
}

func (s *Store) createComputerPairingWithID() (string, string, string, error) {
	code, err := randomSecret(18)
	if err != nil {
		return "", "", "", err
	}
	pairingID := uuid.NewString()
	t := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return "", "", "", err
	}
	defer tx.Rollback()
	// Retain a short issuance history so consuming a code cannot bypass the
	// per-minute bound. Old hashes contain no usable pairing secret.
	if _, err = tx.Exec(`DELETE FROM computer_pairings WHERE created_at<?`, t.Add(-24*time.Hour).Format(time.RFC3339Nano)); err != nil {
		return "", "", "", err
	}
	var pending, recent int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM computer_pairings WHERE used_at IS NULL AND expires_at>?`, t.Format(time.RFC3339Nano)).Scan(&pending); err != nil {
		return "", "", "", err
	}
	if err = tx.QueryRow(`SELECT COUNT(*) FROM computer_pairings WHERE created_at>?`, t.Add(-time.Minute).Format(time.RFC3339Nano)).Scan(&recent); err != nil {
		return "", "", "", err
	}
	if pending >= maxPendingPairings || recent >= maxPendingPairings {
		return "", "", "", errors.New("too many pending pairing requests")
	}
	expires := t.Add(computerPairTTL).Format(time.RFC3339Nano)
	if _, err = tx.Exec(`INSERT INTO computer_pairings(code_hash,created_at,expires_at,pairing_id) VALUES(?,?,?,?)`, secretHash(code), t.Format(time.RFC3339Nano), expires, pairingID); err != nil {
		return "", "", "", err
	}
	return pairingID, code, expires, tx.Commit()
}

type ComputerPairingStatus struct {
	Status   string `json:"status"`
	DeviceID string `json:"device_id,omitempty"`
	Name     string `json:"device_name,omitempty"`
}

func (s *Store) computerPairingStatus(id string) (ComputerPairingStatus, error) {
	var expires, used string
	var device, name sql.NullString
	err := s.db.QueryRow(`SELECT p.expires_at,COALESCE(p.used_at,''),p.device_id,c.name FROM computer_pairings p LEFT JOIN computers c ON c.id=p.device_id AND c.revoked_at IS NULL WHERE p.pairing_id=?`, id).Scan(&expires, &used, &device, &name)
	if err != nil {
		return ComputerPairingStatus{}, err
	}
	if used != "" {
		if device.Valid && name.Valid {
			return ComputerPairingStatus{Status: "paired", DeviceID: device.String, Name: name.String}, nil
		}
		return ComputerPairingStatus{Status: "expired"}, nil
	}
	if expires <= now() {
		return ComputerPairingStatus{Status: "expired"}, nil
	}
	return ComputerPairingStatus{Status: "pending"}, nil
}

func normalizeCapabilities(in []string) ([]string, error) {
	if len(in) > len(computerActions) {
		return nil, errors.New("too many capabilities")
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, capability := range in {
		if !computerActions[capability] {
			return nil, fmt.Errorf("unsupported capability %q", capability)
		}
		if !seen[capability] {
			seen[capability] = true
			out = append(out, capability)
		}
	}
	return out, nil
}

func (s *Store) pairComputer(code, name, platform string, capabilities []string) (string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 120 || platform != "darwin" {
		return "", "", errors.New("valid name and platform darwin are required")
	}
	caps, err := normalizeCapabilities(capabilities)
	if err != nil {
		return "", "", err
	}
	if len(caps) == 0 {
		return "", "", errors.New("capabilities must contain one or more supported actions")
	}
	if len(code) < 20 || len(code) > 128 {
		return "", "", errors.New("invalid or expired pairing code")
	}
	token, err := randomSecret(32)
	if err != nil {
		return "", "", err
	}
	id, t := uuid.NewString(), now()
	tx, err := s.db.Begin()
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE computer_pairings SET used_at=?,device_id=? WHERE code_hash=? AND used_at IS NULL AND expires_at>?`, t, id, secretHash(code), t)
	if err != nil {
		return "", "", err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return "", "", errors.New("invalid or expired pairing code")
	}
	capJSON, _ := json.Marshal(caps)
	if _, err = tx.Exec(`INSERT INTO computers(id,name,platform,kind,token_hash,capabilities,last_seen,created_at) VALUES(?,?,?,?,?,?,?,?)`, id, name, platform, "mac", secretHash(token), string(capJSON), t, t); err != nil {
		return "", "", err
	}
	return id, token, tx.Commit()
}

func (s *Store) listComputers(instanceID string) ([]Computer, error) {
	out := []Computer{{ID: "host:" + instanceID, Name: "Service host", Platform: runtime.GOOS, Kind: "host", Online: true, Capabilities: []string{"host.info"}}}
	rows, err := s.db.Query(`SELECT id,name,platform,capabilities,last_seen FROM computers WHERE revoked_at IS NULL ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cutoff := time.Now().UTC().Add(-computerOnlineWindow)
	for rows.Next() {
		var c Computer
		var raw string
		if err = rows.Scan(&c.ID, &c.Name, &c.Platform, &raw, &c.LastSeen); err != nil {
			return nil, err
		}
		c.Kind = "mac"
		_ = json.Unmarshal([]byte(raw), &c.Capabilities)
		last, parseErr := time.Parse(time.RFC3339Nano, c.LastSeen)
		c.Online = parseErr == nil && last.After(cutoff)
		out = append(out, c)
	}
	return out, rows.Err()
}

func bearerToken(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if !strings.HasPrefix(v, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(v, "Bearer "))
}

func authenticateDevice(q interface{ QueryRow(string, ...any) *sql.Row }, id, token string) error {
	if token == "" {
		return errors.New("unauthorized")
	}
	var stored []byte
	var revoked sql.NullString
	if err := q.QueryRow(`SELECT token_hash,revoked_at FROM computers WHERE id=?`, id).Scan(&stored, &revoked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("unauthorized")
		}
		return err
	}
	given := secretHash(token)
	if revoked.Valid || len(stored) != len(given) || subtle.ConstantTimeCompare(stored, given) != 1 {
		return errors.New("unauthorized")
	}
	return nil
}

func (s *Store) revokeComputer(id string) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	t := now()
	res, err := tx.Exec(`UPDATE computers SET revoked_at=? WHERE id=? AND revoked_at IS NULL`, t, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return false, nil
	}
	if _, err = tx.Exec(`UPDATE computer_jobs SET status='cancelled',error='computer revoked',completed_at=? WHERE device_id=? AND status IN ('pending','running')`, t, id); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *Store) updateComputerCapabilities(id, token string, capabilities []string) ([]string, error) {
	caps, err := normalizeCapabilities(capabilities)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = authenticateDevice(tx, id, token); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(caps)
	if _, err = tx.Exec(`UPDATE computers SET capabilities=?,last_seen=? WHERE id=? AND revoked_at IS NULL`, string(raw), now(), id); err != nil {
		return nil, err
	}
	query := `UPDATE computer_jobs SET status='cancelled',error='computer capability no longer granted',completed_at=? WHERE device_id=? AND status IN ('pending','running')`
	args := []any{now(), id}
	if len(caps) > 0 {
		query += ` AND action NOT IN (` + strings.TrimRight(strings.Repeat("?,", len(caps)), ",") + `)`
		for _, capability := range caps {
			args = append(args, capability)
		}
	}
	if _, err = tx.Exec(query, args...); err != nil {
		return nil, err
	}
	return caps, tx.Commit()
}

func validateComputerArgs(action string, raw json.RawMessage) error {
	if len(raw) == 0 || len(raw) > maxComputerResult || !json.Valid(raw) {
		return errors.New("args must be a JSON object no larger than 1 MiB")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return errors.New("args must be a JSON object")
	}
	strict := func(v any) error {
		d := json.NewDecoder(strings.NewReader(string(raw)))
		d.DisallowUnknownFields()
		return d.Decode(v)
	}
	validPath := func(p string, optional bool) error {
		clean := path.Clean(p)
		if (!optional && p == "") || len(p) > 4096 || strings.IndexByte(p, 0) >= 0 || path.IsAbs(p) || clean == ".." || strings.HasPrefix(clean, "../") {
			return errors.New("invalid path")
		}
		return nil
	}
	switch action {
	case "files.list":
		var v struct {
			Path string `json:"path"`
		}
		if err := strict(&v); err != nil {
			return errors.New("files.list args require only optional path")
		}
		return validPath(v.Path, true)
	case "files.read":
		var v struct {
			Path string `json:"path"`
		}
		if err := strict(&v); err != nil {
			return errors.New("files.read args require path")
		}
		return validPath(v.Path, false)
	case "files.write":
		var v struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := strict(&v); err != nil || len(v.Content) > 256<<10 {
			return errors.New("files.write args require bounded path and content")
		}
		return validPath(v.Path, false)
	case "screen.capture":
		var v struct{}
		if err := strict(&v); err != nil {
			return errors.New("screen.capture takes no args")
		}
		return nil
	case "desktop.click":
		var v struct {
			X      *float64 `json:"x"`
			Y      *float64 `json:"y"`
			Button string   `json:"button"`
		}
		if err := strict(&v); err != nil || v.X == nil || v.Y == nil || *v.X < 0 || *v.Y < 0 || *v.X > 100000 || *v.Y > 100000 || (v.Button != "" && v.Button != "left" && v.Button != "right") {
			return errors.New("desktop.click requires bounded x, y, and optional button")
		}
		return nil
	case "desktop.type":
		var v struct {
			Text *string `json:"text"`
		}
		if err := strict(&v); err != nil || v.Text == nil || len(*v.Text) > 65536 {
			return errors.New("desktop.type requires bounded text")
		}
		return nil
	case "desktop.key":
		var v struct {
			Key       string   `json:"key"`
			Modifiers []string `json:"modifiers"`
		}
		if err := strict(&v); err != nil || v.Key == "" || len(v.Key) > 64 || len(v.Modifiers) > 8 {
			return errors.New("desktop.key requires key and optional modifiers")
		}
		for _, m := range v.Modifiers {
			if m != "cmd" && m != "ctrl" && m != "alt" && m != "shift" {
				return errors.New("unsupported key modifier")
			}
		}
		return nil
	case "sandbox.exec":
		var v struct {
			Command string `json:"command"`
		}
		if err := strict(&v); err != nil || strings.TrimSpace(v.Command) == "" || len(v.Command) > 16<<10 || strings.IndexByte(v.Command, 0) >= 0 {
			return errors.New("sandbox.exec args require only a non-empty command up to 16 KiB")
		}
		return nil
	default:
		return errors.New("unsupported action")
	}
}

func validateHostInfoArgs(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	var empty struct{}
	if err := d.Decode(&empty); err != nil {
		return errors.New("host.info takes no args")
	}
	return nil
}

func (s *Store) queueComputerJob(r Run, deviceID, action string, args json.RawMessage) (ComputerJob, error) {
	if err := validateComputerArgs(action, args); err != nil {
		return ComputerJob{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return ComputerJob{}, err
	}
	defer tx.Rollback()
	if r.ID != "" {
		var status, botID string
		if err = tx.QueryRow(`SELECT status,bot_id FROM runs WHERE id=?`, r.ID).Scan(&status, &botID); err != nil || status != "running" || botID != r.BotID {
			return ComputerJob{}, errors.New("parent run is not active")
		}
	}
	var capsRaw, lastSeen string
	if err = tx.QueryRow(`SELECT capabilities,last_seen FROM computers WHERE id=? AND revoked_at IS NULL`, deviceID).Scan(&capsRaw, &lastSeen); err != nil {
		return ComputerJob{}, errors.New("computer not found")
	}
	last, _ := time.Parse(time.RFC3339Nano, lastSeen)
	if !last.After(time.Now().UTC().Add(-computerOnlineWindow)) {
		return ComputerJob{}, errors.New("computer is offline")
	}
	var caps []string
	_ = json.Unmarshal([]byte(capsRaw), &caps)
	granted := false
	for _, cap := range caps {
		if cap == action {
			granted = true
			break
		}
	}
	if !granted {
		return ComputerJob{}, errors.New("computer capability not granted")
	}
	var outstanding int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM computer_jobs WHERE device_id=? AND status IN ('pending','running')`, deviceID).Scan(&outstanding); err != nil {
		return ComputerJob{}, err
	}
	if outstanding >= maxOutstandingJobs {
		return ComputerJob{}, errors.New("computer has too many outstanding jobs")
	}
	t := time.Now().UTC()
	j := ComputerJob{ID: uuid.NewString(), DeviceID: deviceID, BotID: r.BotID, RunID: r.ID, Action: action, Args: append(json.RawMessage(nil), args...), ExpiresAt: t.Add(computerJobTTL).Format(time.RFC3339Nano), Status: "pending"}
	if _, err = tx.Exec(`INSERT INTO computer_jobs(id,device_id,bot_id,run_id,action,args,status,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?)`, j.ID, j.DeviceID, j.BotID, j.RunID, j.Action, string(j.Args), j.Status, t.Format(time.RFC3339Nano), j.ExpiresAt); err != nil {
		return ComputerJob{}, err
	}
	return j, tx.Commit()
}

func (s *Store) claimComputerJob(deviceID, token string) (*ComputerJob, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = authenticateDevice(tx, deviceID, token); err != nil {
		return nil, err
	}
	t := now()
	if _, err = tx.Exec(`UPDATE computers SET last_seen=? WHERE id=?`, t, deviceID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`UPDATE computer_jobs SET status='expired',error='job expired',completed_at=? WHERE device_id=? AND status='pending' AND expires_at<=?`, t, deviceID, t); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`UPDATE computer_jobs SET status='cancelled',error='parent run is no longer active',completed_at=? WHERE device_id=? AND status='pending' AND run_id<>'' AND NOT EXISTS(SELECT 1 FROM runs WHERE runs.id=computer_jobs.run_id AND runs.status='running')`, t, deviceID); err != nil {
		return nil, err
	}
	var j ComputerJob
	var args string
	err = tx.QueryRow(`SELECT id,device_id,bot_id,run_id,action,args,expires_at,status FROM computer_jobs WHERE device_id=? AND status='pending' ORDER BY created_at,id LIMIT 1`, deviceID).Scan(&j.ID, &j.DeviceID, &j.BotID, &j.RunID, &j.Action, &args, &j.ExpiresAt, &j.Status)
	if err == sql.ErrNoRows {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	j.Args = json.RawMessage(args)
	res, err := tx.Exec(`UPDATE computer_jobs SET status='running',claimed_at=? WHERE id=? AND status='pending'`, t, j.ID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil, errors.New("job was already claimed")
	}
	j.Status = "running"
	return &j, tx.Commit()
}

func validateComputerResult(action string, raw json.RawMessage) error {
	if len(raw) == 0 {
		raw = json.RawMessage("null")
	}
	if len(raw) > maxComputerResult || !json.Valid(raw) {
		return errors.New("result must be valid JSON no larger than 1 MiB")
	}
	if action != "screen.capture" {
		return nil
	}
	var v struct {
		ImageURL string `json:"image_url"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return errors.New("screen capture result requires image_url")
	}
	prefix := ""
	for _, p := range []string{"data:image/png;base64,", "data:image/jpeg;base64,"} {
		if strings.HasPrefix(v.ImageURL, p) {
			prefix = p
			break
		}
	}
	if prefix == "" {
		return errors.New("screen capture must be a PNG or JPEG data URL")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(v.ImageURL, prefix))
	if err != nil || len(decoded) == 0 || len(decoded) > 768<<10 {
		return errors.New("invalid or oversized screen capture")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(decoded))
	if err != nil || (format != "png" && format != "jpeg") || config.Width < 1 || config.Height < 1 || config.Width > 8192 || config.Height > 8192 {
		return errors.New("invalid or oversized screen capture dimensions")
	}
	return nil
}

func (s *Store) completeComputerJob(deviceID, token, jobID string, result json.RawMessage, resultErr string) error {
	if len(resultErr) > 4096 {
		return errors.New("error is too long")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = authenticateDevice(tx, deviceID, token); err != nil {
		return err
	}
	var action, runID, status, expires string
	if err = tx.QueryRow(`SELECT action,run_id,status,expires_at FROM computer_jobs WHERE id=? AND device_id=?`, jobID, deviceID).Scan(&action, &runID, &status, &expires); err != nil {
		return errors.New("job not found")
	}
	if status != "running" {
		return errors.New("job is not active")
	}
	t := now()
	if expires <= t {
		if _, err = tx.Exec(`UPDATE computer_jobs SET status='expired',error='job expired',completed_at=? WHERE id=? AND status='running'`, t, jobID); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		return errors.New("job expired")
	}
	if runID != "" {
		var runStatus string
		if err = tx.QueryRow(`SELECT status FROM runs WHERE id=?`, runID).Scan(&runStatus); err != nil || runStatus != "running" {
			if _, updateErr := tx.Exec(`UPDATE computer_jobs SET status='cancelled',error='parent run is no longer active',completed_at=? WHERE id=? AND status='running'`, t, jobID); updateErr != nil {
				return updateErr
			}
			if commitErr := tx.Commit(); commitErr != nil {
				return commitErr
			}
			return errors.New("parent run is no longer active")
		}
	}
	if resultErr != "" {
		if len(result) == 0 {
			result = json.RawMessage("null")
		}
		if len(result) > maxComputerResult || !json.Valid(result) {
			return errors.New("result must be valid JSON no larger than 1 MiB")
		}
	} else {
		if err = validateComputerResult(action, result); err != nil {
			return err
		}
	}
	finalStatus := "completed"
	if resultErr != "" {
		finalStatus = "failed"
	}
	res, err := tx.Exec(`UPDATE computer_jobs SET status=?,result=?,error=?,completed_at=? WHERE id=? AND device_id=? AND status='running'`, finalStatus, string(result), nullString(resultErr), t, jobID, deviceID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errors.New("job is not active")
	}
	return tx.Commit()
}

func (s *Store) computerJob(id, deviceID string) (ComputerJob, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return ComputerJob{}, err
	}
	defer tx.Rollback()
	j, err := computerJobInTx(tx, id, deviceID, time.Now().UTC())
	if err != nil {
		return ComputerJob{}, err
	}
	return j, tx.Commit()
}

// Owner and device reads apply the same expiry/parent-run rules within the
// caller's transaction; the device caller authenticates before reaching here.
func computerJobInTx(tx *sql.Tx, id, deviceID string, at time.Time) (ComputerJob, error) {
	var j ComputerJob
	var args string
	var result, jobErr sql.NullString
	err := tx.QueryRow(`SELECT id,device_id,bot_id,run_id,action,args,expires_at,status,result,error FROM computer_jobs WHERE id=? AND device_id=?`, id, deviceID).Scan(&j.ID, &j.DeviceID, &j.BotID, &j.RunID, &j.Action, &args, &j.ExpiresAt, &j.Status, &result, &jobErr)
	if err != nil {
		return j, err
	}
	j.Args = json.RawMessage(args)
	if result.Valid {
		j.Result = json.RawMessage(result.String)
	}
	j.Error = jobErr.String
	if j.Status != "pending" && j.Status != "running" {
		return j, nil
	}
	expires, err := time.Parse(time.RFC3339Nano, j.ExpiresAt)
	if err != nil {
		return j, fmt.Errorf("invalid computer job expiry: %w", err)
	}
	if !expires.After(at) {
		j.Status, j.Error = "expired", "job expired"
	} else if j.RunID != "" {
		var status string
		err = tx.QueryRow(`SELECT status FROM runs WHERE id=?`, j.RunID).Scan(&status)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return j, err
		}
		if status != "running" {
			j.Status, j.Error = "cancelled", "parent run is no longer active"
		}
	}
	if j.Status == "expired" || j.Status == "cancelled" {
		_, err = tx.Exec(`UPDATE computer_jobs SET status=?,error=?,completed_at=? WHERE id=? AND device_id=? AND status IN ('pending','running')`, j.Status, j.Error, at.Format(time.RFC3339Nano), id, deviceID)
	}
	return j, err
}

func (s *Store) deviceJobStatus(deviceID, token, jobID string) (ComputerJobStatus, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return ComputerJobStatus{}, err
	}
	defer tx.Rollback()
	if err = authenticateDevice(tx, deviceID, token); err != nil {
		return ComputerJobStatus{}, err
	}
	j, err := computerJobInTx(tx, jobID, deviceID, time.Now().UTC())
	if err != nil {
		return ComputerJobStatus{}, err
	}
	state := ComputerJobStatus{ID: j.ID, DeviceID: j.DeviceID, Status: j.Status, ExpiresAt: j.ExpiresAt}
	switch state.Status {
	case "running":
		expires, err := time.Parse(time.RFC3339Nano, j.ExpiresAt)
		if err != nil {
			return ComputerJobStatus{}, err
		}
		state.LeaseMS = min(time.Until(expires).Milliseconds(), int64(1000))
		if state.LeaseMS <= 0 {
			state.Status, state.LeaseMS = "expired", 0
			if _, err = tx.Exec(`UPDATE computer_jobs SET status='expired',error='job expired',completed_at=? WHERE id=? AND device_id=? AND status='running'`, now(), jobID, deviceID); err != nil {
				return ComputerJobStatus{}, err
			}
		}
	case "pending", "completed", "failed", "cancelled", "expired", "interrupted":
	default:
		return ComputerJobStatus{}, errors.New("invalid computer job status")
	}
	return state, tx.Commit()
}

func (s *Server) routeComputers(w http.ResponseWriter, r *http.Request, path string) bool {
	if path != "computers" && !strings.HasPrefix(path, "computers/") {
		return false
	}
	rel := strings.TrimPrefix(path, "computers")
	if rel == "" && r.Method == http.MethodGet {
		items, err := s.listComputers(r.Context())
		if err != nil {
			writeErr(w, 500, "storage", err.Error())
			return true
		}
		writeJSON(w, 200, map[string]any{"computers": items})
		return true
	}
	if rel == "/pairings" && r.Method == http.MethodPost {
		pairingID, code, expires, err := s.store.createComputerPairingWithID()
		if err != nil {
			writeErr(w, 429, "pairing_limit", err.Error())
			return true
		}
		writeJSON(w, 201, map[string]any{"pairing_id": pairingID, "code": code, "expires_at": expires})
		return true
	}
	if strings.HasPrefix(rel, "/pairings/") && r.Method == http.MethodGet {
		id := strings.TrimPrefix(rel, "/pairings/")
		if _, err := uuid.Parse(id); err != nil {
			writeErr(w, 404, "not_found", "pairing not found")
			return true
		}
		status, err := s.store.computerPairingStatus(id)
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, 404, "not_found", "pairing not found")
			return true
		}
		if err != nil {
			writeErr(w, 500, "storage", err.Error())
			return true
		}
		writeJSON(w, 200, status)
		return true
	}
	if rel == "/pair" && r.Method == http.MethodPost {
		var v struct {
			Code         string   `json:"code"`
			Name         string   `json:"name"`
			Platform     string   `json:"platform"`
			Capabilities []string `json:"capabilities"`
		}
		if err := decodeStrict(r, 32<<10, &v); err != nil {
			writeErr(w, 400, "invalid_request", "invalid pairing request")
			return true
		}
		id, token, err := s.store.pairComputer(v.Code, v.Name, v.Platform, v.Capabilities)
		if err != nil {
			writeErr(w, 400, "invalid_pairing", err.Error())
			return true
		}
		writeJSON(w, 201, map[string]any{"id": id, "token": token})
		return true
	}
	parts := strings.Split(strings.TrimPrefix(rel, "/"), "/")
	if len(parts) == 2 && parts[0] == microVMComputerID && parts[1] == "info" && r.Method == http.MethodGet {
		if s.microVM == nil {
			writeErr(w, http.StatusNotFound, "not_configured", "computer VM is not configured")
			return true
		}
		info, err := s.microVMInfo(r.Context())
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "computer_unavailable", err.Error())
			return true
		}
		writeJSON(w, http.StatusOK, info)
		return true
	}
	if len(parts) == 2 && parts[0] == microVMComputerID && parts[1] == "retry" && r.Method == http.MethodPost {
		if s.microVM == nil {
			writeErr(w, http.StatusNotFound, "not_configured", "computer VM is not configured")
			return true
		}
		if err := s.microVM.Retry(r.Context()); err != nil {
			writeErr(w, http.StatusBadGateway, "computer_retry_failed", err.Error())
		} else {
			writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true})
		}
		return true
	}
	if len(parts) == 1 && parts[0] != "" && r.Method == http.MethodDelete {
		ok, err := s.store.revokeComputer(parts[0])
		if err != nil {
			writeErr(w, 500, "storage", err.Error())
		} else if !ok {
			writeErr(w, 404, "not_found", "computer not found")
		} else {
			writeJSON(w, 200, map[string]bool{"revoked": true})
		}
		return true
	}
	if len(parts) == 2 && parts[1] == "capabilities" && r.Method == http.MethodPatch {
		var v struct {
			Capabilities []string `json:"capabilities"`
		}
		if err := decodeStrict(r, 16<<10, &v); err != nil {
			writeErr(w, 400, "invalid_request", "invalid capabilities")
			return true
		}
		caps, err := s.store.updateComputerCapabilities(parts[0], bearerToken(r), v.Capabilities)
		if err != nil {
			if err.Error() == "unauthorized" {
				writeErr(w, 401, "unauthorized", "invalid device token")
			} else {
				writeErr(w, 400, "capabilities_rejected", err.Error())
			}
			return true
		}
		writeJSON(w, 200, map[string]any{"capabilities": caps})
		return true
	}
	if len(parts) == 2 && parts[1] == "jobs" && r.Method == http.MethodGet {
		job, err := s.store.claimComputerJob(parts[0], bearerToken(r))
		if err != nil {
			if err.Error() == "unauthorized" {
				writeErr(w, 401, "unauthorized", "invalid device token")
			} else {
				writeErr(w, 500, "storage", err.Error())
			}
			return true
		}
		writeJSON(w, 200, map[string]any{"job": job})
		return true
	}
	if len(parts) == 4 && parts[0] != "" && parts[1] == "jobs" && parts[2] != "" && parts[3] == "status" && r.Method == http.MethodGet {
		w.Header().Set("Cache-Control", "no-store")
		state, err := s.store.deviceJobStatus(parts[0], bearerToken(r), parts[2])
		if err != nil {
			switch {
			case err.Error() == "unauthorized":
				writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid device token")
			case errors.Is(err, sql.ErrNoRows):
				writeErr(w, http.StatusNotFound, "not_found", "computer job not found")
			default:
				writeErr(w, http.StatusInternalServerError, "storage", "cannot read computer job status")
			}
			return true
		}
		writeJSON(w, http.StatusOK, state)
		return true
	}
	if len(parts) == 4 && parts[1] == "jobs" && parts[3] == "result" && r.Method == http.MethodPost {
		var v struct {
			Result json.RawMessage `json:"result"`
			Error  string          `json:"error,omitempty"`
		}
		if err := decodeStrict(r, maxComputerResult+8192, &v); err != nil {
			writeErr(w, 400, "invalid_request", "invalid result")
			return true
		}
		if len(v.Result) == 0 {
			v.Result = json.RawMessage("null")
		}
		err := s.store.completeComputerJob(parts[0], bearerToken(r), parts[2], v.Result, v.Error)
		if err != nil {
			code := 409
			if err.Error() == "unauthorized" {
				code = 401
			}
			writeErr(w, code, "job_rejected", err.Error())
			return true
		}
		writeJSON(w, 200, map[string]bool{"accepted": true})
		return true
	}
	if len(parts) == 2 && parts[1] == "actions" && r.Method == http.MethodPost {
		var v struct {
			Action string          `json:"action"`
			Args   json.RawMessage `json:"args"`
			BotID  string          `json:"bot_id"`
			RunID  string          `json:"run_id"`
		}
		if err := decodeStrict(r, maxComputerResult+8192, &v); err != nil {
			writeErr(w, 400, "invalid_request", "invalid action")
			return true
		}
		if parts[0] == microVMComputerID {
			if s.microVM == nil {
				writeErr(w, http.StatusNotFound, "not_configured", "computer VM is not configured")
				return true
			}
			if strings.TrimSpace(v.BotID) == "" {
				writeErr(w, http.StatusBadRequest, "invalid_request", "bot_id is required")
				return true
			}
			if _, err := s.store.GetBot(v.BotID); err != nil {
				writeErr(w, http.StatusNotFound, "bot_not_found", "bot not found")
				return true
			}
			if strings.HasPrefix(v.Action, "terminal.") {
				if v.RunID != "" {
					writeErr(w, 400, "invalid_request", "terminal clients cannot supply run_id")
				} else {
					s.handleTerminalAction(w, r, v.BotID, v.Action, v.Args)
				}
				return true
			}
			if strings.HasPrefix(v.Action, "desktop.control.") {
				if v.RunID != "" {
					writeErr(w, 400, "invalid_request", "control sessions do not accept run_id")
				} else {
					s.handleComputerControl(w, r, v.BotID, v.Action, v.Args)
				}
				return true
			}
			if isGraphicAction(v.Action) && v.RunID != "" {
				writeErr(w, 400, "invalid_request", "Graphical clients cannot supply run_id")
				return true
			}
			var active int
			if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE bot_id=? AND status IN ('queued','running')`, v.BotID).Scan(&active); err != nil {
				writeErr(w, http.StatusInternalServerError, "storage", err.Error())
				return true
			}
			readOnly := isReadOnlyMicroVMAction(v.Action)
			if active > 0 && !readOnly && !isGraphicAction(v.Action) {
				writeErr(w, http.StatusConflict, "computer_busy", "Bot 正在工作，暂时不能接管电脑")
				return true
			}
			if v.RunID != "" {
				var status, runBot string
				if err := s.store.db.QueryRow(`SELECT status,bot_id FROM runs WHERE id=?`, v.RunID).Scan(&status, &runBot); err != nil || runBot != v.BotID || status != "running" {
					writeErr(w, http.StatusConflict, "stale_run", "run is no longer active")
					return true
				}
			}
			runID := v.RunID
			if runID == "" {
				// The guest protocol requires a run_id. Human actions use a
				// request correlation id rather than pretending to be a durable run.
				runID = "human-" + uuid.NewString()
			}
			lease := s.terminalLease(v.BotID)
			if isGraphicAction(v.Action) {
				lease = s.computerLease(v.BotID)
			}
			if !lease.TryLock() {
				log.Printf("[desktop] action denied bot=%s action=%s reason=computer-lease-busy", v.BotID, v.Action)
				writeErr(w, http.StatusConflict, "computer_busy", "电脑正在被使用，请稍后重试")
				return true
			}
			defer lease.Unlock()
			// Read-only inspection can share the Bot owner held by an active
			// model run. Mutations must claim ownership for the whole run so a
			// second DM/group sequence cannot interleave desktop operations.
			if !readOnly && isGraphicAction(v.Action) {
				if !s.claimComputerOwner(v.BotID, runID) {
					log.Printf("[desktop] action denied bot=%s action=%s run=%s reason=owner-conflict", v.BotID, v.Action, runID)
					writeErr(w, http.StatusConflict, "computer_busy", "电脑正在被另一个 Bot 运行使用")
					return true
				}
				defer s.releaseComputerOwner(v.BotID, runID)
			}
			// Recheck after taking the lease so a queued run cannot begin between
			// the first check and this human action.
			if activeNow := s.botHasActiveRun(v.BotID); activeNow && !readOnly && !isGraphicAction(v.Action) {
				writeErr(w, http.StatusConflict, "computer_busy", "Bot 正在工作，暂时不能接管电脑")
				return true
			}
			source := "human"
			if readOnly {
				source = "viewer"
			}
			result, err := s.microVMActionFromSource(r.Context(), Run{BotID: v.BotID, ID: runID}, v.Action, v.Args, source)
			if err != nil {
				writeErr(w, http.StatusBadGateway, "computer_action_failed", err.Error())
			} else {
				var decoded any
				if json.Unmarshal([]byte(result), &decoded) != nil {
					decoded = result
				}
				writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": decoded})
			}
			return true
		}
		if strings.HasPrefix(parts[0], "host:") && v.Action == "host.info" {
			if parts[0] != "host:"+s.instance.ID {
				writeErr(w, 404, "not_found", "computer not found")
				return true
			}
			if err := validateHostInfoArgs(v.Args); err != nil {
				writeErr(w, 400, "action_rejected", err.Error())
				return true
			}
			t := time.Now().UTC()
			j := ComputerJob{ID: uuid.NewString(), DeviceID: parts[0], Action: v.Action, Args: v.Args, Status: "completed", Result: json.RawMessage(fmt.Sprintf(`{"os":%q,"arch":%q}`, runtime.GOOS, runtime.GOARCH)), ExpiresAt: t.Add(computerJobTTL).Format(time.RFC3339Nano)}
			if len(j.Args) == 0 {
				j.Args = json.RawMessage(`{}`)
			}
			_, err := s.store.db.Exec(`INSERT INTO computer_jobs(id,device_id,action,args,status,result,created_at,expires_at,completed_at) VALUES(?,?,?,?,?,?,?,?,?)`, j.ID, j.DeviceID, j.Action, string(j.Args), j.Status, string(j.Result), t.Format(time.RFC3339Nano), j.ExpiresAt, t.Format(time.RFC3339Nano))
			if err != nil {
				writeErr(w, 500, "storage", err.Error())
				return true
			}
			writeJSON(w, 201, j)
			return true
		}
		j, err := s.store.queueComputerJob(Run{}, parts[0], v.Action, v.Args)
		if err != nil {
			writeErr(w, 400, "action_rejected", err.Error())
			return true
		}
		writeJSON(w, 201, j)
		return true
	}
	if len(parts) == 3 && parts[1] == "actions" && r.Method == http.MethodGet {
		j, err := s.store.computerJob(parts[2], parts[0])
		if err != nil {
			writeErr(w, 404, "not_found", "job not found")
		} else {
			writeJSON(w, 200, j)
		}
		return true
	}
	writeErr(w, 405, "method", "method not allowed")
	return true
}

func (s *Server) computerTools(r Run) []Tool {
	list := Tool{Name: "list_computers", Description: "List the service host and paired Mac computers with current online status and explicitly granted capabilities. Reuse prior conversation facts for explanatory answers; call this when current availability or grants matter for an operation, not automatically on every turn.", Parameters: objectSchema(map[string]any{}, nil), Execute: func(ctx context.Context, _ json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		items, err := s.listComputers(ctx)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(map[string]any{"computers": items})
		return string(b), nil
	}}
	action := Tool{Name: "computer_action", Description: "Run one explicitly granted action on a paired computer or on the Bot's fixed Firecracker computer. Use only capabilities currently advertised by that computer; an action name does not grant access or enable an unavailable runtime. Callers cannot override local grants or execution policy. When a paired Mac advertises sandbox.exec, pass only args.command: it runs for at most 30 seconds in the locally authorized directory and directly changes its files, with no automatic rollback. It does not grant network, SSH keys, Keychain or arbitrary host toolchains. A stopped managed process group does not prove every detached descendant stopped. Inspect exitCode, stderr and cancellation before claiming success; do not retry uncertain writes automatically. Use host.info on the service host only to read its OS and architecture.", Parameters: objectSchema(map[string]any{"computer_id": map[string]any{"type": "string"}, "action": map[string]any{"type": "string"}, "args": map[string]any{"type": "object"}}, []string{"computer_id", "action", "args"}), Identity: func(raw json.RawMessage) tooloutcome.Identity {
		var in struct {
			ComputerID string          `json:"computer_id"`
			Action     string          `json:"action"`
			Args       json.RawMessage `json:"args"`
		}
		_ = json.Unmarshal(raw, &in)
		return computerRecoveryIdentity(r.BotID, in.ComputerID, in.Action, in.Args)
	}, Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var v struct {
			ComputerID string          `json:"computer_id"`
			Action     string          `json:"action"`
			Args       json.RawMessage `json:"args"`
		}
		d := json.NewDecoder(strings.NewReader(string(raw)))
		d.DisallowUnknownFields()
		if err := d.Decode(&v); err != nil {
			return "", errors.New("invalid computer action")
		}
		if v.ComputerID == "host:"+s.instance.ID {
			if v.Action != "host.info" {
				return "", errors.New("service host only supports host.info")
			}
			if err := validateHostInfoArgs(v.Args); err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"os":%q,"arch":%q}`, runtime.GOOS, runtime.GOARCH), nil
		}
		if v.ComputerID == microVMComputerID {
			return s.microVMAction(ctx, r, v.Action, v.Args)
		}
		j, err := s.store.queueComputerJob(r, v.ComputerID, v.Action, v.Args)
		if err != nil {
			return "", err
		}
		waitCtx, cancel := context.WithTimeout(ctx, computerWaitTimeout)
		defer cancel()
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-waitCtx.Done():
				t := now()
				if _, updateErr := s.store.db.Exec(`UPDATE computer_jobs SET status='cancelled',error=?,completed_at=? WHERE id=? AND status IN ('pending','running')`, waitCtx.Err().Error(), t, j.ID); updateErr != nil {
					return "", fmt.Errorf("%w (could not persist cancellation: %v)", waitCtx.Err(), updateErr)
				}
				return "", waitCtx.Err()
			case <-ticker.C:
				current, e := s.store.computerJob(j.ID, j.DeviceID)
				if e != nil {
					return "", e
				}
				switch current.Status {
				case "completed":
					return string(current.Result), nil
				case "failed", "cancelled", "expired", "interrupted":
					if current.Error == "" {
						current.Error = current.Status
					}
					return "", errors.New(current.Error)
				}
			}
		}
	}}
	return []Tool{list, action}
}
