package app

import (
	"context"
	"database/sql"
	"strings"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// followGlobalModel is stored in bots.model and bots.reasoning_effort to mean
// "use the workspace's global model setting". It is resolved at execution and
// never reaches a provider, a price table or a context-window lookup.
//
// An empty reasoning_effort stays distinct: it is legacy data that executes
// on the historical "medium" setting.
const followGlobalModel = "default"

// isFollowGlobalModel reports whether a stored model value defers to the
// global setting. An empty model has always meant "the workspace default".
func isFollowGlobalModel(model string) bool {
	model = strings.TrimSpace(model)
	return model == "" || model == followGlobalModel
}

// resolveBotModel returns the concrete model and reasoning effort a run of
// this Bot executes with now. runModel is the run row's model ("" or
// "default" defer to the Bot).
//
// Effort "default" means the global effort when the model comes from the
// global setting, and the model's own default reasoning when the Bot pins a
// model. Empty effort is legacy data and stays on "medium".
func (s *Server) resolveBotModel(ctx context.Context, runModel string, b Bot) (string, string) {
	globalModel, globalEffort := s.modelDefaults()
	model := strings.TrimSpace(runModel)
	if isFollowGlobalModel(model) {
		model = strings.TrimSpace(b.Model)
	}
	fromGlobal := false
	if isFollowGlobalModel(model) {
		model, fromGlobal = globalModel, true
	} else if isFollowGlobalModel(b.Model) && model == globalModel {
		// A run persisted while the Bot followed the global setting (retry,
		// follow-up, continuation) keeps following the global effort.
		fromGlobal = true
	}
	effort := strings.TrimSpace(b.ReasoningEffort)
	switch effort {
	case "":
		effort = "medium"
	case followGlobalModel:
		if fromGlobal {
			effort = globalEffort
		} else {
			effort = s.defaultReasoningForModel(ctx, model)
		}
		if effort == "" {
			if model == globalModel && globalEffort != "" {
				effort = globalEffort
			} else {
				effort = "medium"
			}
		}
	}
	return model, effort
}

// withEffectiveModels annotates Bots with the model and effort they execute
// with now, for clients that show "跟随全局（当前：X）".
func (s *Server) withEffectiveModels(ctx context.Context, bots ...Bot) []Bot {
	out := make([]Bot, len(bots))
	for i, b := range bots {
		b.EffectiveModel, b.EffectiveReasoningEffort = s.resolveBotModel(ctx, "", b)
		out[i] = b
	}
	return out
}

func (s *Server) withEffectiveModel(ctx context.Context, b Bot) Bot {
	return s.withEffectiveModels(ctx, b)[0]
}

// setRunModelIfFollowing persists the resolved model into a run that deferred
// to its Bot, so continuations, approval expiry, failure classification and
// usage see a real model ID. A run that already names a model is unchanged.
func (s *Store) setRunModelIfFollowing(runID, model string) error {
	if isFollowGlobalModel(model) {
		return nil
	}
	_, err := s.db.Exec(`UPDATE runs SET model=? WHERE id=? AND (model IS NULL OR model='' OR model=?)`, model, runID, followGlobalModel)
	return err
}

// normalizeBotModel maps the spellings clients and models use for "the
// workspace default" onto the stored sentinel; any other value is a pin.
func (s *Server) normalizeBotModel(model string) string {
	model = strings.TrimSpace(model)
	switch strings.ToLower(model) {
	case "", followGlobalModel, "auto", "inherit", "global":
		return followGlobalModel
	}
	return model
}

func migrateFollowGlobalMarker(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS model_follow_global_migration(
id INTEGER PRIMARY KEY CHECK(id=1),
switched INTEGER NOT NULL,
applied_at TEXT NOT NULL)`)
	return err
}

// sameModelID compares model IDs the way catalog validation does: an
// unprefixed OpenAI ID names the Codex model in a workspace without an OpenAI
// API key.
func (s *Server) sameModelID(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == b {
		return true
	}
	if strings.HasPrefix(a, "codex-") == strings.HasPrefix(b, "codex-") {
		return false
	}
	bare := a
	if strings.HasPrefix(a, "codex-") {
		bare = b
	}
	if runtime.ModelProvider(bare) != providerOpenAI {
		return false
	}
	if _, ok := s.providerKey(providerOpenAI); ok {
		return false
	}
	return strings.TrimPrefix(a, "codex-") == strings.TrimPrefix(b, "codex-")
}

// migrateBotsToFollowGlobal runs once per workspace: Bots that were created
// with a copy of the global setting (model and effort equal to it) switch to
// following it. Later pins that happen to equal the global setting are user
// choices and are never rewritten, because the marker row stops a re-run.
func (s *Server) migrateBotsToFollowGlobal() (int, error) {
	tx, err := s.store.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var done int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM model_follow_global_migration WHERE id=1`).Scan(&done); err != nil {
		return 0, err
	}
	if done != 0 {
		return 0, nil
	}
	saved := modelSettings{}
	if err = tx.QueryRow(`SELECT model,reasoning_effort FROM model_settings WHERE id=1`).Scan(&saved.Model, &saved.ReasoningEffort); err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	s.mu.Lock()
	globalModel, globalEffort := s.defaultModel, s.defaultReasoning
	s.mu.Unlock()
	if strings.TrimSpace(saved.Model) != "" {
		globalModel = strings.TrimSpace(saved.Model)
	}
	if strings.TrimSpace(saved.ReasoningEffort) != "" {
		globalEffort = strings.TrimSpace(saved.ReasoningEffort)
	}
	if globalEffort == "" {
		globalEffort = "medium"
	}
	rows, err := tx.Query(`SELECT id,model,reasoning_effort FROM bots`)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id, model, effort string
		if err = rows.Scan(&id, &model, &effort); err != nil {
			rows.Close()
			return 0, err
		}
		if isFollowGlobalModel(model) || globalModel == "" || !s.sameModelID(model, globalModel) {
			continue
		}
		// Empty effort is legacy "medium"; it equals a medium global setting.
		if effort == "" {
			effort = "medium"
		}
		if effort != globalEffort {
			continue
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	for _, id := range ids {
		if _, err = tx.Exec(`UPDATE bots SET model=?,reasoning_effort=? WHERE id=?`, followGlobalModel, followGlobalModel, id); err != nil {
			return 0, err
		}
	}
	stamp := now()
	if len(ids) > 0 {
		if err = insertWorkspaceEventTx(tx, workspaceScopeBots, stamp); err != nil {
			return 0, err
		}
	}
	if _, err = tx.Exec(`INSERT INTO model_follow_global_migration(id,switched,applied_at) VALUES(1,?,?)`, len(ids), stamp); err != nil {
		return 0, err
	}
	return len(ids), tx.Commit()
}
