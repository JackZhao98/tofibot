package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ModelOption is the normalized model metadata exposed to clients. It is
// derived from the connected provider when possible and otherwise from the
// last successful metadata snapshot.
type ModelOption struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	ReasoningEfforts []string `json:"reasoning_efforts"`
	DefaultReasoning string   `json:"default_reasoning"`
}

type modelSettings struct {
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
}

func (s *Server) modelDefaults() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.defaultModel, s.defaultReasoning
}

func migrateModelSettings(db *sql.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS model_settings(
id INTEGER PRIMARY KEY CHECK(id=1),
model TEXT NOT NULL,
reasoning_effort TEXT NOT NULL,
updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS model_catalog_cache(
id INTEGER PRIMARY KEY CHECK(id=1),
models_json TEXT NOT NULL,
updated_at TEXT NOT NULL);`)
	return err
}

func (s *Store) getModelSettings() (modelSettings, error) {
	var x modelSettings
	err := s.db.QueryRow(`SELECT model,reasoning_effort FROM model_settings WHERE id=1`).Scan(&x.Model, &x.ReasoningEffort)
	if err == sql.ErrNoRows {
		return modelSettings{}, nil
	}
	return x, err
}

func (s *Store) putModelSettings(x modelSettings) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO model_settings(id,model,reasoning_effort,updated_at) VALUES(1,?,?,?)
ON CONFLICT(id) DO UPDATE SET model=excluded.model,reasoning_effort=excluded.reasoning_effort,updated_at=excluded.updated_at`, x.Model, x.ReasoningEffort, now()); err != nil {
		return err
	}
	if err = insertWorkspaceEventTx(tx, workspaceScopeConfig, now()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) cachedModels() ([]ModelOption, error) {
	var raw string
	if err := s.db.QueryRow(`SELECT models_json FROM model_catalog_cache WHERE id=1`).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	var models []ModelOption
	if err := json.Unmarshal([]byte(raw), &models); err != nil {
		return nil, fmt.Errorf("decode model catalog cache: %w", err)
	}
	return models, nil
}

func (s *Store) cacheModels(models []ModelOption) error {
	raw, err := json.Marshal(models)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO model_catalog_cache(id,models_json,updated_at) VALUES(1,?,?)
ON CONFLICT(id) DO UPDATE SET models_json=excluded.models_json,updated_at=excluded.updated_at`, string(raw), now())
	return err
}

// modelSettings is deliberately a small HTTP surface: model metadata is
// provider state, while the selected default is workspace state.
func (s *Server) modelSettings(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/model-settings":
		x, err := s.store.getModelSettings()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "storage", err.Error())
			return
		}
		defaultModel, defaultReasoning := s.modelDefaults()
		if x.Model == "" {
			x.Model = defaultModel
		}
		if x.ReasoningEffort == "" {
			x.ReasoningEffort = defaultReasoning
		}
		writeJSON(w, http.StatusOK, x)
	case r.Method == http.MethodPut && r.URL.Path == "/api/model-settings":
		var x modelSettings
		if err := decode(r, &x); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", "invalid request")
			return
		}
		x.Model = strings.TrimSpace(x.Model)
		x.ReasoningEffort = strings.TrimSpace(x.ReasoningEffort)
		if x.Model == "" {
			writeErr(w, http.StatusBadRequest, "invalid_request", "model is required")
			return
		}
		if x.ReasoningEffort == "" {
			_, x.ReasoningEffort = s.modelDefaults()
			if models, _, _ := s.loadModels(r.Context()); len(models) > 0 {
				for _, option := range models {
					if option.ID == x.Model || strings.TrimPrefix(option.ID, "codex-") == strings.TrimPrefix(x.Model, "codex-") {
						if option.DefaultReasoning != "" {
							x.ReasoningEffort = option.DefaultReasoning
						}
						break
					}
				}
			}
		}
		if err := s.validateModelChoice(r.Context(), x.Model, x.ReasoningEffort); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_model_settings", err.Error())
			return
		}
		if err := s.store.putModelSettings(x); err != nil {
			writeErr(w, http.StatusInternalServerError, "storage", err.Error())
			return
		}
		s.mu.Lock()
		s.defaultModel, s.defaultReasoning = x.Model, x.ReasoningEffort
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, x)
	default:
		writeErr(w, http.StatusNotFound, "not_found", "not found")
	}
}

func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.Path != "/api/models" {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	models, source, warning := s.loadModels(r.Context())
	resp := map[string]any{"models": models, "source": source}
	if warning != "" {
		resp["warning"] = warning
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) loadModels(ctx context.Context) ([]ModelOption, string, string) {
	if !strings.EqualFold(s.provider, "openai_codex") {
		return []ModelOption{}, "fallback", "model metadata is available only for the connected Codex provider"
	}
	s.modelCatalogMu.Lock()
	if len(s.modelCatalog) > 0 && time.Since(s.modelCatalogAt) < 5*time.Minute {
		models := append([]ModelOption(nil), s.modelCatalog...)
		source := s.modelCatalogSource
		s.modelCatalogMu.Unlock()
		if source == "live" {
			return models, source, ""
		}
		return models, "cache", "live model metadata is unavailable; showing the last successful snapshot"
	}
	s.modelCatalogMu.Unlock()
	if s.codex != nil {
		if credential, err := s.codex.CredentialReadOnly(ctx); err == nil {
			if models, err := fetchCodexModels(ctx, credential); err == nil && len(models) > 0 {
				if err := s.store.cacheModels(models); err == nil {
					s.modelCatalogMu.Lock()
					s.modelCatalog, s.modelCatalogAt, s.modelCatalogSource = append([]ModelOption(nil), models...), time.Now(), "live"
					s.modelCatalogMu.Unlock()
					return models, "live", ""
				}
			}
		}
	}
	if models, err := s.store.cachedModels(); err == nil && len(models) > 0 {
		s.modelCatalogMu.Lock()
		s.modelCatalog, s.modelCatalogAt, s.modelCatalogSource = append([]ModelOption(nil), models...), time.Now(), "cache"
		s.modelCatalogMu.Unlock()
		return models, "cache", "live model metadata is unavailable; showing the last successful snapshot"
	}
	if models, err := readLocalCodexModelCache(); err == nil && len(models) > 0 {
		_ = s.store.cacheModels(models)
		s.modelCatalogMu.Lock()
		s.modelCatalog, s.modelCatalogAt, s.modelCatalogSource = append([]ModelOption(nil), models...), time.Now(), "cache"
		s.modelCatalogMu.Unlock()
		return models, "cache", "live model metadata is unavailable; showing the local Codex metadata snapshot"
	}
	return []ModelOption{}, "fallback", "model metadata is unavailable; connect the configured provider to load available models"
}

func (s *Server) validateModelChoice(ctx context.Context, model, effort string) error {
	if err := s.validateModelID(ctx, model); err != nil {
		return err
	}
	if strings.TrimSpace(effort) == "" {
		return nil
	}
	if s.engine != nil && !s.codexManaged {
		return nil
	}
	models, _, _ := s.loadModels(ctx)
	for _, option := range models {
		if option.ID != model && strings.TrimPrefix(option.ID, "codex-") != strings.TrimPrefix(model, "codex-") {
			continue
		}
		for _, supported := range option.ReasoningEfforts {
			if supported == effort {
				return nil
			}
		}
		return fmt.Errorf("reasoning_effort %q is not supported by model %q", effort, model)
	}
	if len(models) == 0 {
		if !strings.EqualFold(s.provider, "openai_codex") {
			return nil
		}
		defaultModel, defaultReasoning := s.modelDefaults()
		if model == defaultModel && effort == defaultReasoning {
			return nil
		}
		return errors.New("available model metadata is required to validate this model and reasoning effort")
	}
	// A disconnected provider cannot prove availability. Preserve existing
	// local/custom provider workflows; Codex settings remain conservative.
	if strings.EqualFold(s.provider, "openai_codex") {
		return fmt.Errorf("model %q is not present in available model metadata", model)
	}
	return nil
}

func (s *Server) validateModelID(ctx context.Context, model string) error {
	model = strings.TrimSpace(model)
	if model == "" || (s.engine != nil && !s.codexManaged) || !strings.EqualFold(s.provider, "openai_codex") {
		return nil
	}
	models, _, _ := s.loadModels(ctx)
	for _, option := range models {
		if option.ID == model || strings.TrimPrefix(option.ID, "codex-") == strings.TrimPrefix(model, "codex-") {
			return nil
		}
	}
	if len(models) == 0 {
		defaultModel, _ := s.modelDefaults()
		if model == defaultModel {
			return nil
		}
		return errors.New("available model metadata is required to validate this model")
	}
	return fmt.Errorf("model %q is not present in available model metadata", model)
}

func (s *Server) defaultReasoningForModel(ctx context.Context, model string) string {
	models, _, _ := s.loadModels(ctx)
	for _, option := range models {
		if option.ID == model || strings.TrimPrefix(option.ID, "codex-") == strings.TrimPrefix(model, "codex-") {
			return option.DefaultReasoning
		}
	}
	return ""
}

type codexModelsResponse struct {
	Models []struct {
		Slug               string `json:"slug"`
		ID                 string `json:"id"`
		DisplayName        string `json:"display_name"`
		DefaultReasoning   string `json:"default_reasoning_level"`
		SupportedReasoning []struct {
			Effort string `json:"effort"`
		} `json:"supported_reasoning_levels"`
	} `json:"models"`
}

func fetchCodexModels(ctx context.Context, credential string) ([]ModelOption, error) {
	parts := strings.SplitN(credential, "\x00", 2)
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return nil, errors.New("Codex credential is empty")
	}
	// Keep the catalog request aligned with the installed Codex model cache.
	// The older client version omitted GPT-6 Sol and Luna for this account.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://chatgpt.com/backend-api/codex/models?client_version=0.155.0", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+parts[0])
	req.Header.Set("originator", "tofi")
	req.Header.Set("User-Agent", "tofi/1.0")
	if len(parts) == 2 && parts[1] != "" {
		req.Header.Set("ChatGPT-Account-Id", parts[1])
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("model metadata request returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	var raw codexModelsResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	return normalizeCodexModels(raw), nil
}

func readLocalCodexModelCache() ([]ModelOption, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(filepath.Join(home, ".codex", "models_cache.json"))
	if err != nil {
		return nil, err
	}
	var raw codexModelsResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	return normalizeCodexModels(raw), nil
}

func normalizeCodexModels(raw codexModelsResponse) []ModelOption {
	out := make([]ModelOption, 0, len(raw.Models))
	for _, m := range raw.Models {
		id := m.Slug
		if id == "" {
			id = m.ID
		}
		if id == "" {
			continue
		}
		id = "codex-" + strings.TrimPrefix(id, "codex-")
		option := ModelOption{ID: id, Name: m.DisplayName, DefaultReasoning: m.DefaultReasoning}
		if option.Name == "" {
			option.Name = id
		}
		for _, level := range m.SupportedReasoning {
			if level.Effort != "" {
				option.ReasoningEfforts = append(option.ReasoningEfforts, level.Effort)
			}
		}
		if len(option.ReasoningEfforts) > 0 {
			out = append(out, option)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
