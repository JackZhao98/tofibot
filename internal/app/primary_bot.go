package app

// The Primary Assistant is a fixed, account-level role: the Bot that owns the
// owner's general chores (and, from P1, the owner's mail). The id lives in
// user_preferences.primary_bot_id. It is self-healing: if the stored Bot is
// gone or archived, the first active Bot by creation order takes the role, so
// the role is never empty while an active Bot exists.

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
)

var (
	// ErrPrimaryReassignRequired is returned when removing the Primary while
	// another active Bot exists and no successor was named.
	ErrPrimaryReassignRequired = errors.New("the Primary Assistant must be reassigned first")
	// ErrPrimaryInvalid is returned for a successor that is missing, archived or
	// the Bot being removed.
	ErrPrimaryInvalid = errors.New("the new Primary Assistant must be another active Bot")
)

func migratePrimaryBot(db *sql.DB) error {
	if err := ensureColumn(db, "user_preferences", "primary_bot_id", `ALTER TABLE user_preferences ADD COLUMN primary_bot_id TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	// Existing accounts: the first Bot by creation order becomes Primary. The
	// row is only created when there is a Bot to point at.
	if _, err := db.Exec(`INSERT OR IGNORE INTO user_preferences(id,timezone,updated_at) SELECT 1,'',? WHERE EXISTS(SELECT 1 FROM bots WHERE archived=0)`, now()); err != nil {
		return err
	}
	_, err := db.Exec(`UPDATE user_preferences SET primary_bot_id=COALESCE((SELECT id FROM bots WHERE archived=0 ORDER BY created_at,id LIMIT 1),'') WHERE id=1 AND primary_bot_id='' AND NOT EXISTS(SELECT 1 FROM bots b WHERE b.id=user_preferences.primary_bot_id)`)
	return err
}

func firstActiveBotTx(tx *sql.Tx, except string) (string, error) {
	var id string
	err := tx.QueryRow(`SELECT id FROM bots WHERE archived=0 AND id<>? ORDER BY created_at,id LIMIT 1`, except).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

func writePrimaryTx(tx *sql.Tx, id string) error {
	_, err := tx.Exec(`INSERT INTO user_preferences(id,timezone,primary_bot_id,updated_at) VALUES(1,'',?,?)
ON CONFLICT(id) DO UPDATE SET primary_bot_id=excluded.primary_bot_id,updated_at=excluded.updated_at`, id, now())
	return err
}

// primaryBotTx returns the effective Primary, repairing the stored value when
// it no longer names an active Bot.
func primaryBotTx(tx *sql.Tx) (string, error) {
	var stored string
	err := tx.QueryRow(`SELECT primary_bot_id FROM user_preferences WHERE id=1`).Scan(&stored)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	if stored != "" {
		var archived int
		e := tx.QueryRow(`SELECT archived FROM bots WHERE id=?`, stored).Scan(&archived)
		if e == nil && archived == 0 {
			return stored, nil
		}
		if e != nil && e != sql.ErrNoRows {
			return "", e
		}
	}
	first, err := firstActiveBotTx(tx, "")
	if err != nil {
		return "", err
	}
	if first != stored {
		if err = writePrimaryTx(tx, first); err != nil {
			return "", err
		}
	}
	return first, nil
}

// claimPrimaryIfEmptyTx runs inside Bot-creation transactions: the first Bot of
// an account (or the first after the last one was deleted) becomes Primary.
// Called after the new Bot row exists.
func claimPrimaryIfEmptyTx(tx *sql.Tx, newBotID, at string) error {
	var stored string
	err := tx.QueryRow(`SELECT primary_bot_id FROM user_preferences WHERE id=1`).Scan(&stored)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if stored != "" {
		var archived int
		e := tx.QueryRow(`SELECT archived FROM bots WHERE id=?`, stored).Scan(&archived)
		if e == nil && archived == 0 {
			return nil
		}
		if e != nil && e != sql.ErrNoRows {
			return e
		}
	}
	// Prefer the earliest active Bot, which is the new one only when it is alone.
	first, err := firstActiveBotTx(tx, "")
	if err != nil {
		return err
	}
	if first == "" {
		first = newBotID
	}
	return writePrimaryTx(tx, first)
}

// PrimaryBotID returns the effective Primary Assistant id, or "" when the
// account has no active Bot.
func (s *Store) PrimaryBotID() (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	id, err := primaryBotTx(tx)
	if err != nil {
		return "", err
	}
	return id, tx.Commit()
}

// SetPrimaryBot designates an active Bot as the Primary Assistant.
func (s *Store) SetPrimaryBot(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = requireActivePrimaryCandidateTx(tx, id); err != nil {
		return err
	}
	if err = writePrimaryTx(tx, id); err != nil {
		return err
	}
	if err = insertWorkspaceEventTx(tx, workspaceScopeBots, now()); err != nil {
		return err
	}
	return tx.Commit()
}

func requireActivePrimaryCandidateTx(tx *sql.Tx, id string) error {
	var archived int
	err := tx.QueryRow(`SELECT archived FROM bots WHERE id=?`, id).Scan(&archived)
	if err == sql.ErrNoRows || (err == nil && archived != 0) {
		return ErrPrimaryInvalid
	}
	return err
}

// resolvePrimaryOnRemovalTx enforces the forced-reassign rule when Bot id is
// about to be deleted or archived through an owner-facing surface (strict):
// if id is the Primary and any other active Bot exists, a successor must be
// named; with no other active Bot the role becomes empty. The agent tool path
// passes no successor, so it can never remove the Primary: only the owner
// designates one, in the UI. Non-strict callers (internal Store helpers) leave
// the stored id alone and rely on the read-time repair to pick the next Bot.
func resolvePrimaryOnRemovalTx(tx *sql.Tx, id, successor string, strict bool) error {
	current, err := primaryBotTx(tx)
	if err != nil {
		return err
	}
	if current != id {
		return nil
	}
	others, err := firstActiveBotTx(tx, id)
	if err != nil {
		return err
	}
	if others == "" {
		return writePrimaryTx(tx, "")
	}
	if !strict {
		return nil
	}
	successor = strings.TrimSpace(successor)
	if successor == "" {
		return ErrPrimaryReassignRequired
	}
	if successor == id {
		return ErrPrimaryInvalid
	}
	if err = requireActivePrimaryCandidateTx(tx, successor); err != nil {
		return err
	}
	return writePrimaryTx(tx, successor)
}

func writePrimaryError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, ErrPrimaryReassignRequired):
		writeErr(w, http.StatusConflict, "primary_reassign_required", err.Error())
	case errors.Is(err, ErrPrimaryInvalid):
		writeErr(w, http.StatusBadRequest, "primary_invalid", err.Error())
	default:
		return false
	}
	return true
}

func (s *Server) routePrimaryAssistant(w http.ResponseWriter, r *http.Request, path string) bool {
	if path != "primary-assistant" {
		return false
	}
	switch r.Method {
	case http.MethodGet:
		id, err := s.store.PrimaryBotID()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "storage", "primary assistant unavailable")
			return true
		}
		writeJSON(w, http.StatusOK, map[string]string{"bot_id": id})
	case http.MethodPut:
		var body struct {
			BotID string `json:"bot_id"`
		}
		if decodeStrict(r, 4<<10, &body) != nil || strings.TrimSpace(body.BotID) == "" {
			writeErr(w, http.StatusBadRequest, "invalid_request", "bot_id is required")
			return true
		}
		if err := s.store.SetPrimaryBot(strings.TrimSpace(body.BotID)); err != nil {
			if !writePrimaryError(w, err) {
				writeErr(w, http.StatusInternalServerError, "storage", "primary assistant unavailable")
			}
			return true
		}
		writeJSON(w, http.StatusOK, map[string]string{"bot_id": strings.TrimSpace(body.BotID)})
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or PUT")
	}
	return true
}
