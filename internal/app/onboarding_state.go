package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// First-run onboarding progress lives in the account's own preferences row, so
// it resumes on any device and is isolated per account (each account has its
// own workspace database).
type onboardingState struct {
	// Step is the last step the person reached: 0 never started, 1 welcome,
	// 2 connect a model, 3 connect services.
	Step        int    `json:"step"`
	Completed   bool   `json:"completed"`
	CompletedAt string `json:"completed_at"`
	Skipped     bool   `json:"skipped"`
	SkippedAt   string `json:"skipped_at"`
}

const onboardingLastStep = 3

func migrateOnboardingState(db *sql.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	for _, c := range []struct{ name, ddl string }{
		{"onboarding_step", `ALTER TABLE user_preferences ADD COLUMN onboarding_step INTEGER NOT NULL DEFAULT 0`},
		{"onboarding_completed_at", `ALTER TABLE user_preferences ADD COLUMN onboarding_completed_at TEXT NOT NULL DEFAULT ''`},
		{"onboarding_skipped_at", `ALTER TABLE user_preferences ADD COLUMN onboarding_skipped_at TEXT NOT NULL DEFAULT ''`},
	} {
		if err := ensureColumn(db, "user_preferences", c.name, c.ddl); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) onboardingState() (onboardingState, error) {
	var st onboardingState
	err := s.db.QueryRow(`SELECT onboarding_step,onboarding_completed_at,onboarding_skipped_at FROM user_preferences WHERE id=1`).Scan(&st.Step, &st.CompletedAt, &st.SkippedAt)
	if err == sql.ErrNoRows {
		err = nil
	}
	st.Completed, st.Skipped = st.CompletedAt != "", st.SkippedAt != ""
	return st, err
}

type onboardingUpdate struct {
	Step      *int  `json:"step"`
	Completed *bool `json:"completed"`
	Skipped   *bool `json:"skipped"`
}

// putOnboardingState applies a partial update. The step only moves forward;
// completing clears a pending skip, and skipping never un-completes.
func (s *Store) putOnboardingState(u onboardingUpdate) (onboardingState, error) {
	if u.Step != nil && (*u.Step < 1 || *u.Step > onboardingLastStep) {
		return onboardingState{}, errors.New("step must be 1 to 3")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return onboardingState{}, err
	}
	defer tx.Rollback()
	t := now()
	if _, err = tx.Exec(`INSERT INTO user_preferences(id,timezone,updated_at) VALUES(1,'',?) ON CONFLICT(id) DO NOTHING`, t); err != nil {
		return onboardingState{}, err
	}
	var step int
	var completedAt, skippedAt string
	if err = tx.QueryRow(`SELECT onboarding_step,onboarding_completed_at,onboarding_skipped_at FROM user_preferences WHERE id=1`).Scan(&step, &completedAt, &skippedAt); err != nil {
		return onboardingState{}, err
	}
	if u.Step != nil && *u.Step > step {
		step = *u.Step
	}
	if u.Completed != nil && *u.Completed && completedAt == "" {
		completedAt, skippedAt = t, ""
	}
	if u.Skipped != nil {
		switch {
		case *u.Skipped && completedAt == "":
			skippedAt = t
		case !*u.Skipped:
			skippedAt = ""
		}
	}
	if _, err = tx.Exec(`UPDATE user_preferences SET onboarding_step=?,onboarding_completed_at=?,onboarding_skipped_at=?,updated_at=? WHERE id=1`, step, completedAt, skippedAt, t); err != nil {
		return onboardingState{}, err
	}
	if err = insertWorkspaceEventTx(tx, workspaceScopeConfig, t); err != nil {
		return onboardingState{}, err
	}
	if err = tx.Commit(); err != nil {
		return onboardingState{}, err
	}
	return onboardingState{Step: step, Completed: completedAt != "", CompletedAt: completedAt, Skipped: skippedAt != "", SkippedAt: skippedAt}, nil
}

func (s *Server) routeOnboarding(w http.ResponseWriter, r *http.Request, path string) bool {
	switch path {
	case "onboarding":
		s.onboardingStateHTTP(w, r)
	case "onboarding/first-bot":
		s.onboardingFirstBot(w, r)
	default:
		return false
	}
	return true
}

func (s *Server) onboardingStateHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		st, err := s.store.onboardingState()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "onboarding_unavailable", "onboarding unavailable")
			return
		}
		writeJSON(w, http.StatusOK, st)
	case http.MethodPut:
		var body onboardingUpdate
		dec := json.NewDecoder(io.LimitReader(r.Body, 4*1024))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil || dec.Decode(new(any)) != io.EOF || (body.Step == nil && body.Completed == nil && body.Skipped == nil) {
			writeErr(w, http.StatusBadRequest, "invalid_request", "expected step, completed or skipped")
			return
		}
		st, err := s.store.putOnboardingState(body)
		if err != nil {
			if strings.HasPrefix(err.Error(), "step ") {
				writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
				return
			}
			writeErr(w, http.StatusInternalServerError, "onboarding_unavailable", "onboarding unavailable")
			return
		}
		writeJSON(w, http.StatusOK, st)
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or PUT")
	}
}

// onboardingFirstBot pre-creates the account's first Bot once a model works.
// It never creates a second Bot: when any Bot exists it returns the oldest.
func (s *Server) onboardingFirstBot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return
	}
	var body struct {
		ClientCreationID string `json:"client_creation_id"`
		Locale           string `json:"locale"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := decode(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", "invalid request")
			return
		}
	}
	if strings.TrimSpace(body.ClientCreationID) == "" {
		body.ClientCreationID = uuid.NewString()
	}
	existing := func() bool {
		bots, err := s.store.ListBots(true)
		if err != nil || len(bots) == 0 {
			return false
		}
		writeJSON(w, http.StatusOK, map[string]any{"bot": s.withEffectiveModel(r.Context(), bots[0]), "created": false})
		return true
	}
	if existing() {
		return
	}
	if !s.modelConfigured() {
		writeErr(w, http.StatusConflict, "model_required", "connect a model first")
		return
	}
	b, duplicate, err := s.store.createOnboardingBot(body.ClientCreationID, followGlobalModel, followGlobalModel, s.onboardingLocale(body.Locale), true)
	if errors.Is(err, errBotsExist) && existing() {
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	status := http.StatusCreated
	if duplicate {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"bot": s.withEffectiveModel(r.Context(), b), "created": !duplicate})
}
