package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
)

// User preferences are workspace-scoped today. An empty timezone is
// deliberate: clients must initialize it rather than inheriting the server's
// local zone.
type userPreferences struct {
	Timezone           string `json:"timezone"`
	TimezoneConfigured bool   `json:"timezone_configured"`
}

func migrateUserPreferences(db *sql.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS user_preferences(
id INTEGER PRIMARY KEY CHECK(id=1),
timezone TEXT NOT NULL DEFAULT '',
updated_at TEXT NOT NULL
)`)
	return err
}

func (s *Store) userTimezone() (string, error) {
	var zone string
	err := s.db.QueryRow(`SELECT timezone FROM user_preferences WHERE id=1`).Scan(&zone)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return zone, err
}

func (s *Store) putUserTimezone(zone string, initializeOnly bool) (string, error) {
	if zone != "" {
		if _, err := time.LoadLocation(zone); err != nil {
			return "", errors.New("timezone must be a valid IANA timezone")
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	query := `INSERT INTO user_preferences(id,timezone,updated_at) VALUES(1,?,?)
ON CONFLICT(id) DO UPDATE SET timezone=excluded.timezone,updated_at=excluded.updated_at`
	if initializeOnly {
		query = `INSERT INTO user_preferences(id,timezone,updated_at) VALUES(1,?,?)
ON CONFLICT(id) DO UPDATE SET timezone=excluded.timezone,updated_at=excluded.updated_at
WHERE user_preferences.timezone=''`
	}
	if _, err = tx.Exec(query, zone, now()); err != nil {
		return "", err
	}
	var stored string
	if err = tx.QueryRow(`SELECT timezone FROM user_preferences WHERE id=1`).Scan(&stored); err != nil {
		return "", err
	}
	if initializeOnly && stored != zone {
		if err = tx.Rollback(); err != nil {
			return "", err
		}
		return stored, nil
	}
	if err = insertWorkspaceEventTx(tx, workspaceScopeConfig, now()); err != nil {
		return "", err
	}
	return stored, tx.Commit()
}

func (s *Server) routePreferences(w http.ResponseWriter, r *http.Request, path string) bool {
	if path != "preferences" {
		return false
	}
	s.preferences(w, r)
	return true
}

func (s *Server) preferences(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		zone, err := s.store.userTimezone()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "preferences_unavailable", "preferences unavailable")
			return
		}
		writeJSON(w, http.StatusOK, userPreferences{Timezone: zone, TimezoneConfigured: zone != ""})
	case http.MethodPut:
		if r.Body == nil || r.ContentLength > 16*1024 {
			writeErr(w, http.StatusBadRequest, "invalid_request", "invalid preferences request")
			return
		}
		var body struct {
			Timezone       string `json:"timezone"`
			InitializeOnly bool   `json:"initialize_only"`
		}
		dec := json.NewDecoder(io.LimitReader(r.Body, 16*1024))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil || dec.Decode(new(any)) != io.EOF {
			writeErr(w, http.StatusBadRequest, "invalid_request", "expected one preferences object")
			return
		}
		body.Timezone = strings.TrimSpace(body.Timezone)
		zone, err := s.store.putUserTimezone(body.Timezone, body.InitializeOnly)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_timezone", err.Error())
			return
		}
		guestSync := "not_configured"
		if s.microVM != nil {
			guestSync = "synced"
			if syncErr := s.syncGuestTimezone(context.Background(), true); syncErr != nil {
				guestSync = "pending"
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"timezone": zone, "timezone_configured": zone != "", "guest_sync": guestSync})
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or PUT")
	}
}

const preferenceSyncBotID = "00000000-0000-0000-0000-000000000000"

func (s *Server) syncGuestTimezone(parent context.Context, force bool) error {
	if s == nil || s.store == nil || s.microVM == nil {
		return errors.New("guest timezone sync unavailable")
	}
	s.guestTimezoneMu.Lock()
	defer s.guestTimezoneMu.Unlock()
	zone, err := s.store.userTimezone()
	if err != nil {
		return err
	}
	if !force && s.guestTimezoneSynced && s.guestTimezoneCached == zone {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	raw, err := json.Marshal(map[string]string{"timezone": zone})
	if err != nil {
		return err
	}
	result, err := s.microVM.Action(ctx, computer.Action{BotID: preferenceSyncBotID, RunID: "timezone-sync", Source: "human", Name: "timezone.set", Args: raw})
	if err != nil {
		return err
	}
	if !result.OK {
		return errors.New(result.Error)
	}
	s.guestTimezoneCached = zone
	s.guestTimezoneSynced = true
	return nil
}

// prepareGuestTimezoneForAction retries a pending preference sync only when a
// new process/session is about to start. Existing processes keep their own TZ.
func (s *Server) prepareGuestTimezoneForAction(ctx context.Context, action string) {
	if s == nil || s.store == nil || s.microVM == nil || (action != "shell.exec" && action != "terminal.open" && action != "desktop.start") {
		return
	}
	_ = s.syncGuestTimezone(ctx, action == "desktop.start")
}
