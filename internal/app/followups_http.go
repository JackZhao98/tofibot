package app

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
)

type followupBotView struct {
	followupBotSettings
	Name        string `json:"name"`
	IsPrimary   bool   `json:"is_primary"`
	LastSweepAt string `json:"last_sweep_at,omitempty"`
}

type followupOverview struct {
	Account          followupAccountSettings `json:"account"`
	PrimaryBotID     string                  `json:"primary_bot_id"`
	DeliveryReady    bool                    `json:"delivery_available"`
	BudgetPaused     bool                    `json:"budget_paused"`
	Bots             []followupBotView       `json:"bots"`
	Stats            followupDayStats        `json:"stats"`
	Loops            []followupLoopView      `json:"loops"`
	ModelConfigured  bool                    `json:"model_configured"`
	OwnerLocalDayKey string                  `json:"day"`
}

func (s *Server) followupOverview() (followupOverview, error) {
	var out followupOverview
	account, err := s.store.followupAccount()
	if err != nil {
		return out, err
	}
	primary, err := s.store.PrimaryBotID()
	if err != nil {
		return out, err
	}
	bots, err := s.store.ListBots()
	if err != nil {
		return out, err
	}
	loc := s.followupLocation()
	nowT := time.Now().UTC()
	out = followupOverview{Account: account, PrimaryBotID: primary, Bots: []followupBotView{}, ModelConfigured: s.modelConfigured(), OwnerLocalDayKey: ownerLocalDay(nowT, loc)}
	for _, b := range bots {
		cfg, e := s.store.followupBotSettings(b.ID)
		if e != nil {
			return out, e
		}
		v := followupBotView{followupBotSettings: cfg, Name: b.Name, IsPrimary: b.ID == primary}
		var last sql.NullString
		if e = s.store.db.QueryRow(`SELECT MAX(started_at) FROM followup_sweeps WHERE bot_id=? AND error=''`, b.ID).Scan(&last); e != nil {
			return out, e
		}
		v.LastSweepAt = last.String
		out.Bots = append(out.Bots, v)
	}
	if out.Stats, err = s.store.followupStats(ownerDayStart(nowT, loc)); err != nil {
		return out, err
	}
	out.BudgetPaused = out.Stats.SpentUSD >= account.DailyBudgetUSD
	out.Loops, err = s.store.followupLoopViews(200)
	return out, err
}

func (s *Server) routeFollowups(w http.ResponseWriter, r *http.Request, path string) bool {
	if path != "followups" && !strings.HasPrefix(path, "followups/") {
		return false
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "followups"), "/"), "/")
	if len(parts) == 1 && parts[0] == "" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
			return true
		}
		v, err := s.followupOverview()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "storage", "follow-ups unavailable")
			return true
		}
		writeJSON(w, http.StatusOK, v)
		return true
	}
	switch {
	case len(parts) == 1 && parts[0] == "account" && r.Method == http.MethodPut:
		var body struct {
			MaxNudgesPerDay      *int     `json:"max_nudges_per_day"`
			QuietStart           *string  `json:"quiet_start"`
			QuietEnd             *string  `json:"quiet_end"`
			WeekendMinImportance *int     `json:"weekend_min_importance"`
			DigestWeekday        *string  `json:"digest_weekday"`
			DigestTime           *string  `json:"digest_time"`
			DailyBudgetUSD       *float64 `json:"daily_budget_usd"`
		}
		if decodeStrict(r, 4<<10, &body) != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", "invalid follow-up settings")
			return true
		}
		cfg, err := s.store.followupAccount()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "storage", "follow-ups unavailable")
			return true
		}
		if body.MaxNudgesPerDay != nil {
			cfg.MaxNudgesPerDay = *body.MaxNudgesPerDay
		}
		if body.QuietStart != nil {
			cfg.QuietStart = *body.QuietStart
		}
		if body.QuietEnd != nil {
			cfg.QuietEnd = *body.QuietEnd
		}
		if body.WeekendMinImportance != nil {
			cfg.WeekendMinImportance = *body.WeekendMinImportance
		}
		if body.DigestWeekday != nil {
			cfg.DigestWeekday = *body.DigestWeekday
		}
		if body.DigestTime != nil {
			cfg.DigestTime = *body.DigestTime
		}
		if body.DailyBudgetUSD != nil {
			cfg.DailyBudgetUSD = *body.DailyBudgetUSD
		}
		if err = s.store.putFollowupAccount(cfg); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_followup_settings", err.Error())
			return true
		}
		writeJSON(w, http.StatusOK, cfg)
	case len(parts) == 2 && parts[0] == "bots" && r.Method == http.MethodPut:
		var body struct {
			Mode        *string `json:"mode"`
			SourceChat  *bool   `json:"source_chat"`
			SourceEmail *bool   `json:"source_email"`
		}
		if decodeStrict(r, 4<<10, &body) != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", "invalid follow-up settings")
			return true
		}
		cfg, err := s.store.followupBotSettings(parts[1])
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "storage", "follow-ups unavailable")
			return true
		}
		if body.Mode != nil {
			cfg.Mode = *body.Mode
		}
		if body.SourceChat != nil {
			cfg.SourceChat = *body.SourceChat
		}
		if body.SourceEmail != nil {
			cfg.SourceEmail = *body.SourceEmail
		}
		if err = s.store.putFollowupBotSettings(cfg); err != nil {
			switch {
			case errors.Is(err, errFollowupDeliveryUnavailable):
				writeErr(w, http.StatusConflict, "followup_delivery_unavailable", err.Error())
			case errors.Is(err, sql.ErrNoRows):
				writeErr(w, http.StatusNotFound, "not_found", "bot not found")
			default:
				writeErr(w, http.StatusBadRequest, "invalid_followup_settings", err.Error())
			}
			return true
		}
		writeJSON(w, http.StatusOK, cfg)
	case len(parts) == 3 && parts[0] == "loops" && parts[2] == "feedback" && r.Method == http.MethodPost:
		var body struct {
			Verdict string `json:"verdict"`
		}
		if decodeStrict(r, 4<<10, &body) != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", "invalid feedback")
			return true
		}
		if err := s.store.addFollowupFeedback(parts[1], body.Verdict); err != nil {
			if errors.Is(err, errLoopNotFound) {
				writeErr(w, http.StatusNotFound, "not_found", err.Error())
			} else {
				writeErr(w, http.StatusBadRequest, "invalid_feedback", err.Error())
			}
			return true
		}
		writeJSON(w, http.StatusOK, map[string]string{"loop_id": parts[1], "verdict": body.Verdict})
	default:
		writeErr(w, http.StatusNotFound, "not_found", "unknown follow-up route")
	}
	return true
}
