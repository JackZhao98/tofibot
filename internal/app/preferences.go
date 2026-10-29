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
	// Language is the Web UI language. Empty means automatic: the browser's
	// preferred languages decide.
	Language string `json:"language"`
	// ShowActivity puts finished runs' Activity rows (tool steps) in the chat
	// timeline. Off by default: the timeline shows the Bot's messages, and the
	// steps stay one tap away behind a message's "View activity" action.
	ShowActivity bool `json:"show_activity"`
	// StructuredOutput makes the say tool the only way text reaches a person:
	// plain assistant text between tool calls stays in the run's activity. On
	// by default; off restores the heuristic publication of plain text.
	StructuredOutput bool `json:"structured_output"`
	// NotifyChats and NotifyTasks gate the browser's desktop notifications for
	// a Bot's chat replies and for finished scheduled-task results. On by
	// default; the browser still needs its own permission.
	NotifyChats bool `json:"notify_chats"`
	NotifyTasks bool `json:"notify_tasks"`
	// Appearance is the theme: system, light or dark. Empty means nobody chose yet, which reads as system; a browser that still holds
	// a choice made before the server kept it hands it up once.
	Appearance string `json:"appearance"`
}

// appearances are the themes a person can choose.
var appearances = map[string]bool{"system": true, "light": true, "dark": true}

// uiLanguages are the Web UI catalogs (ui/src/i18n/languages.ts).
var uiLanguages = map[string]bool{"en": true, "zh-CN": true, "zh-TW": true, "ja": true, "ko": true, "de": true, "fr": true}

func migrateUserPreferences(db *sql.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS user_preferences(
id INTEGER PRIMARY KEY CHECK(id=1),
timezone TEXT NOT NULL DEFAULT '',
updated_at TEXT NOT NULL
)`)
	if err != nil {
		return err
	}
	if err = ensureColumn(db, "user_preferences", "language", `ALTER TABLE user_preferences ADD COLUMN language TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err = ensureColumn(db, "user_preferences", "show_activity", `ALTER TABLE user_preferences ADD COLUMN show_activity INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err = ensureColumn(db, "user_preferences", "structured_output", `ALTER TABLE user_preferences ADD COLUMN structured_output INTEGER NOT NULL DEFAULT 1`); err != nil {
		return err
	}
	for _, column := range []string{"notify_chats", "notify_tasks"} {
		if err = ensureColumn(db, "user_preferences", column, `ALTER TABLE user_preferences ADD COLUMN `+column+` INTEGER NOT NULL DEFAULT 1`); err != nil {
			return err
		}
	}
	if err = ensureColumn(db, "user_preferences", "appearance", `ALTER TABLE user_preferences ADD COLUMN appearance TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err = migratePrimaryBot(db); err != nil {
		return err
	}
	return migrateOnboardingState(db)
}

func (s *Store) userPreferences() (userPreferences, error) {
	var prefs userPreferences
	err := s.db.QueryRow(`SELECT timezone,language,show_activity<>0,structured_output<>0,notify_chats<>0,notify_tasks<>0,appearance FROM user_preferences WHERE id=1`).Scan(&prefs.Timezone, &prefs.Language, &prefs.ShowActivity, &prefs.StructuredOutput, &prefs.NotifyChats, &prefs.NotifyTasks, &prefs.Appearance)
	if err == sql.ErrNoRows {
		err = nil
		prefs.StructuredOutput = true
		prefs.NotifyChats, prefs.NotifyTasks = true, true
	}
	prefs.TimezoneConfigured = prefs.Timezone != ""
	return prefs, err
}

// putUserLanguage stores the UI language; "" returns to automatic.
func (s *Store) putUserLanguage(language string) error {
	if language != "" && !uiLanguages[language] {
		return errors.New("language is not supported")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO user_preferences(id,timezone,language,updated_at) VALUES(1,'',?,?)
ON CONFLICT(id) DO UPDATE SET language=excluded.language,updated_at=excluded.updated_at`, language, now()); err != nil {
		return err
	}
	if err = insertWorkspaceEventTx(tx, workspaceScopeConfig, now()); err != nil {
		return err
	}
	return tx.Commit()
}

// putUserAppearance stores the theme; "" returns to unchosen.
func (s *Store) putUserAppearance(theme string) error {
	if theme != "" && !appearances[theme] {
		return errors.New("appearance must be system, light or dark")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO user_preferences(id,timezone,appearance,updated_at) VALUES(1,'',?,?)
ON CONFLICT(id) DO UPDATE SET appearance=excluded.appearance,updated_at=excluded.updated_at`, theme, now()); err != nil {
		return err
	}
	if err = insertWorkspaceEventTx(tx, workspaceScopeConfig, now()); err != nil {
		return err
	}
	return tx.Commit()
}

// putUserShowActivity stores whether Activity rows appear in the chat timeline.
func (s *Store) putUserShowActivity(show bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO user_preferences(id,timezone,show_activity,updated_at) VALUES(1,'',?,?)
ON CONFLICT(id) DO UPDATE SET show_activity=excluded.show_activity,updated_at=excluded.updated_at`, show, now()); err != nil {
		return err
	}
	if err = insertWorkspaceEventTx(tx, workspaceScopeConfig, now()); err != nil {
		return err
	}
	return tx.Commit()
}

// putUserNotify stores the two notification switches; a nil field keeps its value.
func (s *Store) putUserNotify(chats, tasks *bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO user_preferences(id,timezone,updated_at) VALUES(1,'',?) ON CONFLICT(id) DO NOTHING`, now()); err != nil {
		return err
	}
	if chats != nil {
		if _, err = tx.Exec(`UPDATE user_preferences SET notify_chats=?,updated_at=? WHERE id=1`, *chats, now()); err != nil {
			return err
		}
	}
	if tasks != nil {
		if _, err = tx.Exec(`UPDATE user_preferences SET notify_tasks=?,updated_at=? WHERE id=1`, *tasks, now()); err != nil {
			return err
		}
	}
	if err = insertWorkspaceEventTx(tx, workspaceScopeConfig, now()); err != nil {
		return err
	}
	return tx.Commit()
}

// structuredOutputOn reports the workspace's structured-output setting. A
// missing row or an unreadable store means the default: on.
func (s *Store) structuredOutputOn() bool {
	var on int
	if err := s.db.QueryRow(`SELECT structured_output FROM user_preferences WHERE id=1`).Scan(&on); err != nil {
		return true
	}
	return on != 0
}

// putUserStructuredOutput stores whether the say protocol governs what a Bot
// shows. Runs already in flight keep the mode they started with.
func (s *Store) putUserStructuredOutput(on bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO user_preferences(id,timezone,structured_output,updated_at) VALUES(1,'',?,?)
ON CONFLICT(id) DO UPDATE SET structured_output=excluded.structured_output,updated_at=excluded.updated_at`, on, now()); err != nil {
		return err
	}
	if err = insertWorkspaceEventTx(tx, workspaceScopeConfig, now()); err != nil {
		return err
	}
	return tx.Commit()
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
		prefs, err := s.store.userPreferences()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "preferences_unavailable", "preferences unavailable")
			return
		}
		writeJSON(w, http.StatusOK, prefs)
	case http.MethodPut:
		if r.Body == nil || r.ContentLength > 16*1024 {
			writeErr(w, http.StatusBadRequest, "invalid_request", "invalid preferences request")
			return
		}
		// Each field is optional; an absent field keeps its stored value.
		var body struct {
			Timezone       *string `json:"timezone"`
			InitializeOnly bool    `json:"initialize_only"`
			Language       *string `json:"language"`
			ShowActivity   *bool   `json:"show_activity"`
			// StructuredOutput switches the say protocol for new runs.
			StructuredOutput *bool   `json:"structured_output"`
			NotifyChats      *bool   `json:"notify_chats"`
			NotifyTasks      *bool   `json:"notify_tasks"`
			Appearance       *string `json:"appearance"`
		}
		dec := json.NewDecoder(io.LimitReader(r.Body, 16*1024))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil || dec.Decode(new(any)) != io.EOF {
			writeErr(w, http.StatusBadRequest, "invalid_request", "expected one preferences object")
			return
		}
		if body.Timezone == nil && body.Language == nil && body.ShowActivity == nil && body.StructuredOutput == nil && body.NotifyChats == nil && body.NotifyTasks == nil && body.Appearance == nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", "expected timezone, language, show_activity, structured_output, notify_chats, notify_tasks or appearance")
			return
		}
		if body.Language != nil {
			language := strings.TrimSpace(*body.Language)
			if language != "" && !uiLanguages[language] {
				writeErr(w, http.StatusBadRequest, "invalid_language", "language is not supported")
				return
			}
			if err := s.store.putUserLanguage(language); err != nil {
				writeErr(w, http.StatusInternalServerError, "preferences_unavailable", "preferences unavailable")
				return
			}
		}
		if body.ShowActivity != nil {
			if err := s.store.putUserShowActivity(*body.ShowActivity); err != nil {
				writeErr(w, http.StatusInternalServerError, "preferences_unavailable", "preferences unavailable")
				return
			}
		}
		if body.NotifyChats != nil || body.NotifyTasks != nil {
			if err := s.store.putUserNotify(body.NotifyChats, body.NotifyTasks); err != nil {
				writeErr(w, http.StatusInternalServerError, "preferences_unavailable", "preferences unavailable")
				return
			}
		}
		if body.Appearance != nil {
			theme := strings.TrimSpace(*body.Appearance)
			if theme != "" && !appearances[theme] {
				writeErr(w, http.StatusBadRequest, "invalid_appearance", "appearance must be system, light or dark")
				return
			}
			if err := s.store.putUserAppearance(theme); err != nil {
				writeErr(w, http.StatusInternalServerError, "preferences_unavailable", "preferences unavailable")
				return
			}
		}
		if body.StructuredOutput != nil {
			if err := s.store.putUserStructuredOutput(*body.StructuredOutput); err != nil {
				writeErr(w, http.StatusInternalServerError, "preferences_unavailable", "preferences unavailable")
				return
			}
		}
		prefs, err := s.store.userPreferences()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "preferences_unavailable", "preferences unavailable")
			return
		}
		response := map[string]any{"timezone": prefs.Timezone, "timezone_configured": prefs.TimezoneConfigured, "language": prefs.Language, "show_activity": prefs.ShowActivity, "structured_output": prefs.StructuredOutput, "notify_chats": prefs.NotifyChats, "notify_tasks": prefs.NotifyTasks, "appearance": prefs.Appearance}
		if body.Timezone != nil {
			zone, err := s.store.putUserTimezone(strings.TrimSpace(*body.Timezone), body.InitializeOnly)
			if err != nil {
				writeErr(w, http.StatusBadRequest, "invalid_timezone", err.Error())
				return
			}
			guestSync := "not_configured"
			if s.computer() != nil {
				guestSync = "synced"
				if syncErr := s.syncGuestTimezone(context.Background(), true); syncErr != nil {
					guestSync = "pending"
				}
			}
			// The stored winner, so initialize_only callers see the existing zone.
			response["timezone"], response["timezone_configured"], response["guest_sync"] = zone, zone != "", guestSync
		}
		writeJSON(w, http.StatusOK, response)
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or PUT")
	}
}

const preferenceSyncBotID = "00000000-0000-0000-0000-000000000000"

func (s *Server) syncGuestTimezone(parent context.Context, force bool) error {
	if s == nil || s.store == nil || s.computer() == nil {
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
	result, err := s.computer().Action(ctx, computer.Action{BotID: preferenceSyncBotID, RunID: "timezone-sync", Source: "human", Name: "timezone.set", Args: raw})
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
	if s == nil || s.store == nil || s.computer() == nil || (action != "shell.exec" && action != "terminal.open" && action != "desktop.start") {
		return
	}
	_ = s.syncGuestTimezone(ctx, action == "desktop.start")
}
