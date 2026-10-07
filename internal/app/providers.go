package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

// Model providers the server routes between. Codex is an OAuth sign-in; the
// OpenAI and Anthropic APIs use keys sealed in the secret vault.
const (
	providerCodex     = "openai_codex"
	providerOpenAI    = "openai"
	providerAnthropic = "anthropic"

	modelProviderKind        = "model_provider"
	providerKeyVerifyTimeout = 15 * time.Second
	providerCatalogTTL       = 5 * time.Minute

	codexTriageModel    = "codex-gpt-5.6-luna"
	codexReviewModel    = "codex-auto-review"
	openAISmallModel    = "gpt-5-mini"
	anthropicSmallModel = "claude-haiku-4-5"

	backgroundTriage = "triage"
	backgroundReview = "review"
)

var (
	apiKeyProviders          = []string{providerOpenAI, providerAnthropic}
	defaultProviderEndpoints = map[string]string{providerOpenAI: "https://api.openai.com/v1", providerAnthropic: "https://api.anthropic.com"}
	errProviderUnreachable   = errors.New("provider unreachable")
	datedModelSnapshot       = regexp.MustCompile(`-(\d{4}-\d{2}-\d{2}|\d{4})$`)
)

// managedProviderName reports whether the server owns routing for this
// configured provider (Codex, OpenAI API, Anthropic API).
func managedProviderName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", providerCodex, providerOpenAI, providerAnthropic, "claude":
		return true
	}
	return false
}

func normalizeProviderName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "claude" {
		return providerAnthropic
	}
	return name
}

func publicProviderID(name string) string {
	if name == providerCodex {
		return "codex"
	}
	return name
}

func providerLabel(name string) string {
	switch name {
	case providerCodex:
		return "Codex"
	case providerOpenAI:
		return "OpenAI"
	case providerAnthropic:
		return "Claude"
	}
	return name
}

func providerVaultID(name string) string { return "provider:" + name }

func keyHint(key string) string {
	key = strings.TrimSpace(key)
	if len(key) < 8 {
		return "…"
	}
	return "…" + key[len(key)-4:]
}

// providerUnconfiguredError carries the run_failure marker and the provider.
func providerUnconfiguredError(name string) error {
	return fmt.Errorf("model provider is not configured: %s", name)
}

type providerKeyState struct {
	Key, Hint, VerifiedAt, Error string
	Env                          bool
}

// providerKey returns the vault key, else the operator's environment key.
func (s *Server) providerKey(name string) (providerKeyState, bool) {
	if v := s.secretVault; v != nil {
		v.mu.Lock()
		record, ok := v.records[providerVaultID(name)]
		v.mu.Unlock()
		if ok && record.Kind == modelProviderKind {
			if key, err := v.reveal(record); err == nil && key != "" {
				return providerKeyState{Key: key, Hint: record.Label, VerifiedAt: record.VerifiedAt, Error: record.Error}, true
			}
		}
	}
	s.providerMu.Lock()
	defer s.providerMu.Unlock()
	if key := s.envProviderKeys[name]; key != "" {
		return providerKeyState{Key: key, Hint: keyHint(key), Error: s.envProviderErrors[name], Env: true}, true
	}
	return providerKeyState{}, false
}

// usableProviderKey excludes a key the provider rejected until it is replaced.
func (s *Server) usableProviderKey(name string) bool {
	state, ok := s.providerKey(name)
	return ok && state.Error == ""
}

func (s *Server) codexConnected() bool {
	return s.codex != nil && s.codex.Status().Connected
}

func (s *Server) anyProviderConfigured() bool {
	return s.codexConnected() || s.usableProviderKey(providerOpenAI) || s.usableProviderKey(providerAnthropic)
}

// activeProvider keeps the legacy single-provider field: the first configured.
func (s *Server) activeProvider() string {
	if !managedProviderName(s.provider) {
		return s.provider
	}
	if s.codexConnected() {
		return providerCodex
	}
	for _, name := range apiKeyProviders {
		if s.usableProviderKey(name) {
			return name
		}
	}
	return s.provider
}

// providerCredential is the routed engine's credential resolver.
func (s *Server) providerCredential(ctx context.Context, name string) (string, error) {
	switch name {
	case providerCodex:
		if s.codex == nil {
			return "", errors.New("Codex is not connected")
		}
		return s.codex.Credential(ctx)
	case providerOpenAI, providerAnthropic:
		if state, ok := s.providerKey(name); ok {
			return state.Key, nil
		}
		return "", providerUnconfiguredError(name)
	}
	return "", fmt.Errorf("unknown provider: %s", name)
}

func (s *Server) providerEndpoint(name string) string {
	s.providerMu.Lock()
	defer s.providerMu.Unlock()
	if u := s.providerEndpoints[name]; u != "" {
		return strings.TrimRight(u, "/")
	}
	return defaultProviderEndpoints[name]
}

func (s *Server) newRoutedEngine() runtime.Engine {
	engine, _ := runtime.New(runtime.Config{Model: s.defaultModel, Resolve: s.providerCredential, Endpoint: s.providerEndpoint, MaxDuration: 10 * time.Minute})
	return engine
}

// newModelProvider builds a direct provider (no retry) for a secondary call.
func (s *Server) newModelProvider(ctx context.Context, model string) (provider.Provider, error) {
	name := runtime.ModelProvider(model)
	credential, err := s.providerCredential(ctx, name)
	if err != nil {
		return nil, err
	}
	var opts []provider.Option
	if base := s.providerEndpoint(name); base != "" {
		opts = append(opts, provider.WithBaseURL(base))
	}
	return provider.New(name, credential, opts...)
}

// backgroundModel picks the model for secondary work (summaries, tool search
// expansion, reviewers): Codex first, then the OpenAI API, then Anthropic.
func (s *Server) backgroundModel(purpose string) string {
	if purpose == backgroundTriage && s.triageModel != "" {
		return s.triageModel
	}
	codexModel := codexTriageModel
	if purpose == backgroundReview {
		codexModel = codexReviewModel
	}
	if !managedProviderName(s.provider) {
		if purpose == backgroundTriage {
			return s.defaultModel
		}
		return codexModel
	}
	if s.codexConnected() {
		return codexModel
	}
	if s.usableProviderKey(providerOpenAI) {
		return s.smallProviderModel(providerOpenAI)
	}
	if s.usableProviderKey(providerAnthropic) {
		return s.smallProviderModel(providerAnthropic)
	}
	return codexModel
}

// backgroundProvider resolves the background model and its provider.
func (s *Server) backgroundProvider(ctx context.Context, purpose string) (provider.Provider, string, error) {
	if !managedProviderName(s.provider) {
		return nil, "", errors.New("background model unavailable")
	}
	model := s.backgroundModel(purpose)
	p, err := s.newModelProvider(ctx, model)
	if err != nil {
		return nil, "", err
	}
	return p, model, nil
}

// smallProviderModel prefers a fast model from the cached catalog; it never
// fetches, so background work cannot wait on catalog requests.
func (s *Server) smallProviderModel(name string) string {
	models := s.cachedProviderCatalog(name)
	if name == providerAnthropic {
		for _, m := range models {
			if strings.Contains(m.ID, "haiku") {
				return m.ID
			}
		}
		return anthropicSmallModel
	}
	for _, size := range []string{"-mini", "-nano"} {
		for _, m := range models {
			if len(m.ReasoningEfforts) > 0 && strings.Contains(m.ID, size) {
				return m.ID
			}
		}
	}
	return openAISmallModel
}

type providerCatalog struct {
	models []ModelOption
	at     time.Time
	source string
}

func (s *Server) cachedProviderCatalog(name string) []ModelOption {
	s.providerMu.Lock()
	entry, ok := s.providerCatalogs[name]
	s.providerMu.Unlock()
	if ok && len(entry.models) > 0 {
		return append([]ModelOption(nil), entry.models...)
	}
	models, _ := s.store.cachedProviderModels(name)
	return models
}

func (s *Server) forgetProviderCatalog(name string) {
	s.providerMu.Lock()
	delete(s.providerCatalogs, name)
	s.providerMu.Unlock()
}

// providerModels loads one API-key provider's catalog: memory (5 min), live,
// then the last stored snapshot.
func (s *Server) providerModels(ctx context.Context, name string) ([]ModelOption, string, string) {
	state, ok := s.providerKey(name)
	if !ok {
		return nil, "", ""
	}
	label := providerLabel(name)
	s.providerMu.Lock()
	entry, cached := s.providerCatalogs[name]
	s.providerMu.Unlock()
	if cached && time.Since(entry.at) < providerCatalogTTL && len(entry.models) > 0 {
		if entry.source == "live" {
			return append([]ModelOption(nil), entry.models...), "live", ""
		}
		return append([]ModelOption(nil), entry.models...), "cache", "live " + label + " model metadata is unavailable; showing the last successful snapshot"
	}
	models, status, err := s.fetchProviderModels(ctx, name, state.Key)
	if err == nil && len(models) > 0 {
		s.rememberProviderCatalog(name, models)
		if state.Error != "" {
			s.setProviderKeyError(name, "")
		}
		return models, "live", ""
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		s.noteProviderKeyRejected(name, status)
	}
	if models, e := s.store.cachedProviderModels(name); e == nil && len(models) > 0 {
		s.providerMu.Lock()
		s.providerCatalogs[name] = providerCatalog{models: models, at: time.Now(), source: "cache"}
		s.providerMu.Unlock()
		return models, "cache", "live " + label + " model metadata is unavailable; showing the last successful snapshot"
	}
	return nil, "fallback", label + " model metadata is unavailable"
}

func (s *Server) rememberProviderCatalog(name string, models []ModelOption) {
	if err := s.store.cacheProviderModels(name, models); err != nil {
		log.Printf("[providers] cache %s catalog: %v", name, err)
	}
	s.providerMu.Lock()
	s.providerCatalogs[name] = providerCatalog{models: append([]ModelOption(nil), models...), at: time.Now(), source: "live"}
	s.providerMu.Unlock()
}

// fetchProviderModels lists models with the key; it doubles as key
// verification. The status is the provider's HTTP status when it answered.
func (s *Server) fetchProviderModels(ctx context.Context, name, key string) ([]ModelOption, int, error) {
	base := s.providerEndpoint(name)
	ctx, cancel := context.WithTimeout(ctx, providerKeyVerifyTimeout)
	defer cancel()
	var req *http.Request
	var err error
	switch name {
	case providerOpenAI:
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	case providerAnthropic:
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/models?limit=1000", nil)
		if err == nil {
			req.Header.Set("x-api-key", key)
			req.Header.Set("anthropic-version", "2023-06-01")
		}
	default:
		return nil, 0, fmt.Errorf("unknown provider: %s", name)
	}
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "tofi/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, errProviderUnreachable
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, resp.StatusCode, errProviderUnreachable
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, fmt.Errorf("%s model list returned HTTP %d", providerLabel(name), resp.StatusCode)
	}
	if name == providerOpenAI {
		models, err := normalizeOpenAIModels(body)
		return models, resp.StatusCode, err
	}
	models, err := normalizeAnthropicModels(body)
	return models, resp.StatusCode, err
}

func normalizeOpenAIModels(body []byte) ([]ModelOption, error) {
	var raw struct {
		Data []struct {
			ID      string `json:"id"`
			Created int64  `json:"created"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode OpenAI model list: %w", err)
	}
	sort.SliceStable(raw.Data, func(i, j int) bool {
		if raw.Data[i].Created != raw.Data[j].Created {
			return raw.Data[i].Created > raw.Data[j].Created
		}
		return raw.Data[i].ID < raw.Data[j].ID
	})
	out := []ModelOption{}
	seen := map[string]bool{}
	for _, m := range raw.Data {
		id := strings.TrimSpace(m.ID)
		if !openAIChatModel(id) || seen[id] {
			continue
		}
		seen[id] = true
		option := ModelOption{ID: id, Name: prettyOpenAIModel(id), Provider: "openai", ReasoningEfforts: []string{}}
		if openAIReasoningModel(id) {
			option.ReasoningEfforts, option.DefaultReasoning = []string{"low", "medium", "high"}, "medium"
		}
		out = append(out, option)
	}
	return out, nil
}

// openAIChatModel keeps chat-capable gpt-*/o* aliases; dated snapshots and
// audio, image, embedding and realtime variants are not agent models.
func openAIChatModel(id string) bool {
	m := strings.ToLower(id)
	if !strings.HasPrefix(m, "gpt-") && !(len(m) > 1 && m[0] == 'o' && m[1] >= '0' && m[1] <= '9') {
		return false
	}
	if datedModelSnapshot.MatchString(m) {
		return false
	}
	for _, excluded := range []string{"audio", "realtime", "transcribe", "tts", "image", "search", "embedding", "instruct", "moderation", "whisper", "dall-e", "gpt-3"} {
		if strings.Contains(m, excluded) {
			return false
		}
	}
	return true
}

func openAIReasoningModel(id string) bool {
	m := strings.ToLower(id)
	if strings.Contains(m, "chat") {
		return false
	}
	if strings.HasPrefix(m, "gpt-5") || strings.HasPrefix(m, "gpt-6") || strings.HasPrefix(m, "gpt-7") {
		return true
	}
	return len(m) > 1 && m[0] == 'o' && m[1] >= '0' && m[1] <= '9'
}

func prettyOpenAIModel(id string) string {
	parts := strings.Split(id, "-")
	if len(parts) < 2 || parts[0] != "gpt" {
		return id
	}
	name := "GPT-" + parts[1]
	for _, part := range parts[2:] {
		if part == "" {
			continue
		}
		name += " " + strings.ToUpper(part[:1]) + part[1:]
	}
	return name
}

func normalizeAnthropicModels(body []byte) ([]ModelOption, error) {
	var raw struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			CreatedAt   string `json:"created_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode Anthropic model list: %w", err)
	}
	sort.SliceStable(raw.Data, func(i, j int) bool { return raw.Data[i].CreatedAt > raw.Data[j].CreatedAt })
	out := []ModelOption{}
	seen := map[string]bool{}
	for _, m := range raw.Data {
		id := strings.TrimSpace(m.ID)
		if !strings.HasPrefix(id, "claude") || seen[id] {
			continue
		}
		seen[id] = true
		name := strings.TrimSpace(m.DisplayName)
		if name == "" {
			name = id
		}
		out = append(out, ModelOption{ID: id, Name: name, Provider: "anthropic", ReasoningEfforts: []string{"low", "medium", "high", "xhigh", "max"}, DefaultReasoning: "medium"})
	}
	return out, nil
}

func migrateProviderCatalogCache(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS model_provider_catalog_cache(
provider TEXT PRIMARY KEY,
models_json TEXT NOT NULL,
updated_at TEXT NOT NULL)`)
	return err
}

func (s *Store) cachedProviderModels(name string) ([]ModelOption, error) {
	var raw string
	if err := s.db.QueryRow(`SELECT models_json FROM model_provider_catalog_cache WHERE provider=?`, name).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	var models []ModelOption
	if err := json.Unmarshal([]byte(raw), &models); err != nil {
		return nil, fmt.Errorf("decode %s model catalog cache: %w", name, err)
	}
	return models, nil
}

func (s *Store) cacheProviderModels(name string, models []ModelOption) error {
	raw, err := json.Marshal(models)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO model_provider_catalog_cache(provider,models_json,updated_at) VALUES(?,?,?)
ON CONFLICT(provider) DO UPDATE SET models_json=excluded.models_json,updated_at=excluded.updated_at`, name, string(raw), now())
	return err
}

func (s *Store) forgetProviderModels(name string) error {
	_, err := s.db.Exec(`DELETE FROM model_provider_catalog_cache WHERE provider=?`, name)
	return err
}

// storeProviderKey seals a verified key; it never enters any other surface.
func (s *Server) storeProviderKey(name, key string) (secretRecord, error) {
	v := s.secretVault
	if v == nil {
		return secretRecord{}, errors.New("secret storage unavailable")
	}
	id := providerVaultID(name)
	at := now()
	record := secretRecord{ID: id, Name: providerLabel(name) + " API key", Kind: modelProviderKind, Target: name, Label: keyHint(key), Status: "stored", CreatedAt: at, VerifiedAt: at}
	var err error
	if record.Ciphertext, err = v.seal(id, key); err != nil {
		return secretRecord{}, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	previous, existed := v.records[id]
	v.records[id] = record
	if err = v.saveLocked(); err != nil {
		if existed {
			v.records[id] = previous
		} else {
			delete(v.records, id)
		}
		return secretRecord{}, err
	}
	return record, nil
}

func (s *Server) deleteProviderKey(name string) error {
	v := s.secretVault
	if v == nil {
		return errors.New("secret storage unavailable")
	}
	id := providerVaultID(name)
	v.mu.Lock()
	defer v.mu.Unlock()
	previous, existed := v.records[id]
	if !existed {
		return nil
	}
	delete(v.records, id)
	if err := v.saveLocked(); err != nil {
		v.records[id] = previous
		return err
	}
	return nil
}

// setProviderKeyError records (or clears) why a stored key needs attention.
func (s *Server) setProviderKeyError(name, message string) bool {
	if v := s.secretVault; v != nil {
		id := providerVaultID(name)
		v.mu.Lock()
		record, ok := v.records[id]
		if ok && record.Kind == modelProviderKind {
			changed := record.Error != message
			if changed {
				record.Error = message
				v.records[id] = record
				_ = v.saveLocked()
			}
			v.mu.Unlock()
			return changed
		}
		v.mu.Unlock()
	}
	s.providerMu.Lock()
	defer s.providerMu.Unlock()
	if s.envProviderKeys[name] == "" || s.envProviderErrors[name] == message {
		return false
	}
	s.envProviderErrors[name] = message
	return true
}

func (s *Server) noteProviderKeyRejected(name string, status int) {
	message := fmt.Sprintf("%s rejected the stored API key (HTTP %d). Replace the key to continue.", providerLabel(name), status)
	if s.setProviderKeyError(name, message) {
		if _, err := s.store.WorkspaceEvent(workspaceScopeConfig); err != nil {
			log.Printf("[workspace-events] record %s key rejection: %v", name, err)
		}
	}
}

// ProviderStatus is the public shape of one model provider. Keys never leave
// the server; only the last four characters are shown.
type ProviderStatus struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Kind       string `json:"kind"`
	Configured bool   `json:"configured"`
	Status     string `json:"status,omitempty"`
	KeyHint    string `json:"key_hint,omitempty"`
	VerifiedAt string `json:"verified_at"`
	Error      string `json:"error"`
	Source     string `json:"source,omitempty"`
}

func (s *Server) providerStatus(name string) ProviderStatus {
	if name == providerCodex {
		out := ProviderStatus{ID: "codex", Label: "Codex", Kind: "oauth", Status: "disconnected"}
		if s.codex != nil {
			status := s.codex.Status()
			switch {
			case status.Connected:
				out.Status, out.Configured = "connected", true
			case status.NeedsReconnect:
				out.Status = "needs_reconnect"
			}
		}
		return out
	}
	out := ProviderStatus{ID: name, Label: providerLabel(name), Kind: "api_key"}
	if state, ok := s.providerKey(name); ok {
		out.Configured, out.KeyHint, out.VerifiedAt, out.Error = true, state.Hint, state.VerifiedAt, state.Error
		if state.Env {
			out.Source = "environment"
		}
	}
	return out
}

func (s *Server) routeProviders(w http.ResponseWriter, r *http.Request, p string) bool {
	if p != "providers" && !strings.HasPrefix(p, "providers/") {
		return false
	}
	if p == "providers" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
			return true
		}
		list := []ProviderStatus{s.providerStatus(providerCodex)}
		for _, name := range apiKeyProviders {
			list = append(list, s.providerStatus(name))
		}
		writeJSON(w, http.StatusOK, map[string]any{"providers": list})
		return true
	}
	parts := strings.Split(strings.TrimPrefix(p, "providers/"), "/")
	if len(parts) != 2 || parts[1] != "key" || (parts[0] != providerOpenAI && parts[0] != providerAnthropic) {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return true
	}
	name := parts[0]
	switch r.Method {
	case http.MethodPut:
		s.putProviderKey(w, r, name)
	case http.MethodDelete:
		if err := s.deleteProviderKey(name); err != nil {
			writeErr(w, http.StatusInternalServerError, "storage", "could not remove the API key")
			return true
		}
		s.forgetProviderCatalog(name)
		_ = s.store.forgetProviderModels(name)
		if _, err := s.store.WorkspaceEvent(workspaceScopeConfig); err != nil {
			log.Printf("[workspace-events] record %s key removal: %v", name, err)
		}
		writeJSON(w, http.StatusOK, s.providerStatus(name))
	default:
		w.Header().Set("Allow", "PUT, DELETE")
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use PUT or DELETE")
	}
	return true
}

func (s *Server) putProviderKey(w http.ResponseWriter, r *http.Request, name string) {
	var in struct {
		Key string `json:"key"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "invalid request")
		return
	}
	key := strings.TrimSpace(in.Key)
	if key == "" || len(key) > 4096 || strings.ContainsAny(key, " \t\r\n\x00") {
		writeErr(w, http.StatusBadRequest, "invalid_key", "Enter the full API key.")
		return
	}
	if s.secretVault == nil {
		writeErr(w, http.StatusServiceUnavailable, "unavailable", "Secret storage unavailable")
		return
	}
	models, status, err := s.fetchProviderModels(r.Context(), name, key)
	if err != nil || len(models) == 0 {
		log.Printf("[providers] %s key verification failed: status=%d models=%d err=%v", name, status, len(models), err)
		switch {
		case errors.Is(err, errProviderUnreachable) || status >= 500 || status == http.StatusTooManyRequests:
			writeErr(w, http.StatusBadGateway, "provider_unreachable", fmt.Sprintf("%s could not be reached to verify the key; try again.", providerLabel(name)))
		case status == http.StatusUnauthorized || status == http.StatusForbidden:
			writeErr(w, http.StatusBadRequest, "invalid_key", fmt.Sprintf("%s rejected this API key (HTTP %d).", providerLabel(name), status))
		case err == nil:
			writeErr(w, http.StatusBadRequest, "invalid_key", fmt.Sprintf("This %s API key has no usable models.", providerLabel(name)))
		default:
			writeErr(w, http.StatusBadRequest, "invalid_key", fmt.Sprintf("%s did not accept this API key.", providerLabel(name)))
		}
		return
	}
	if _, err := s.storeProviderKey(name, key); err != nil {
		writeErr(w, http.StatusInternalServerError, "storage", "could not save the API key")
		return
	}
	s.rememberProviderCatalog(name, models)
	if _, err := s.store.WorkspaceEvent(workspaceScopeConfig); err != nil {
		log.Printf("[workspace-events] record %s key: %v", name, err)
	}
	s.wakeConversationWorkers()
	writeJSON(w, http.StatusOK, s.providerStatus(name))
}
