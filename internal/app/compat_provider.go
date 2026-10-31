package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
)

// The OpenAI-compatible endpoint is one provider card whose endpoint the admin
// names: Ollama, LM Studio, vLLM, OpenRouter and the like. Its settings (base
// URL, optional key, typed model IDs) are sealed together in the secret vault
// under "provider:openai_compat", so the key never sits beside plain text.
//
// Models are routed by the "compat:" ID prefix (provider.CompatModelPrefix) and
// spoken to over Chat Completions with tool calling (provider.openaiCompat).
//
// Network rule (SSRF): the request leaves the App server for a host the admin
// typed. Loopback, private (RFC 1918, ULA) and carrier-grade NAT addresses are
// reachable only where the person setting the endpoint is the server's own
// administrator: a single-account install, or any install whose operator set
// TOFI_ALLOW_PRIVATE_MODEL_ENDPOINTS=1. In a multi-account gateway a member
// cannot aim the server at its internal network. Link-local addresses (cloud
// metadata at 169.254.169.254), unspecified and multicast addresses are never
// reachable. The rule is enforced when each connection is dialed, so a name
// that later resolves elsewhere, or a redirect, meets the same check.
const (
	compatMaxModels      = 100
	compatMaxModelIDLen  = 200
	compatMaxBaseURLLen  = 512
	compatMaxKeyLen      = 4096
	compatPrivateEnvFlag = "TOFI_ALLOW_PRIVATE_MODEL_ENDPOINTS"
)

var errCompatBlocked = errors.New("endpoint address is not allowed")

type compatConfig struct {
	BaseURL string   `json:"base_url"`
	Key     string   `json:"key,omitempty"`
	Models  []string `json:"models,omitempty"`
}

// compatSettings reads the sealed settings; ok is false when none are stored.
func (s *Server) compatSettings() (compatConfig, providerKeyState, bool) {
	state, ok := s.providerKey(providerCompat)
	if !ok {
		return compatConfig{}, providerKeyState{}, false
	}
	var cfg compatConfig
	if json.Unmarshal([]byte(state.Key), &cfg) != nil || cfg.BaseURL == "" {
		return compatConfig{}, state, false
	}
	return cfg, state, true
}

func parseCompatKey(sealed string) compatConfig {
	var cfg compatConfig
	_ = json.Unmarshal([]byte(sealed), &cfg)
	return cfg
}

// normalizeCompatBaseURL accepts http(s)://host[:port][/path] and returns it
// without a trailing slash. A bare host root gets "/v1", the path every server
// of this kind serves.
func normalizeCompatBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > compatMaxBaseURLLen {
		return "", errors.New("enter the endpoint's base URL")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", errors.New("the base URL must start with http:// or https://")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("the base URL cannot carry credentials, a query or a fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if u.Path == "" {
		u.Path = "/v1"
	}
	return u.String(), nil
}

func normalizeCompatModelIDs(in []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, id := range in {
		id = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(id), provider.CompatModelPrefix))
		if id == "" || seen[id] {
			continue
		}
		if len(id) > compatMaxModelIDLen || strings.ContainsAny(id, " \t\r\n\x00") {
			return nil, fmt.Errorf("%q is not a valid model ID", trimRunes(id, 40))
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) > compatMaxModels {
		return nil, fmt.Errorf("list at most %d models", compatMaxModels)
	}
	return out, nil
}

func (s *Server) compatPrivateAllowed() bool {
	return s.accountID == "" || os.Getenv(compatPrivateEnvFlag) == "1"
}

var carrierGradeNAT = netip.MustParsePrefix("100.64.0.0/10")

// compatAddrAllowed applies the network rule to one resolved address.
func compatAddrAllowed(addr netip.Addr, allowPrivate bool) bool {
	addr = addr.Unmap()
	switch {
	case !addr.IsValid(), addr.IsUnspecified(), addr.IsMulticast(), addr.IsLinkLocalUnicast(), addr.IsLinkLocalMulticast():
		return false
	case addr.IsLoopback(), addr.IsPrivate(), carrierGradeNAT.Contains(addr):
		return allowPrivate
	}
	return true
}

func (s *Server) compatDialControl(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return errCompatBlocked
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || !compatAddrAllowed(addr, s.compatPrivateAllowed()) {
		return errCompatBlocked
	}
	return nil
}

// providerHTTPClient is the client every request to a compatible endpoint goes
// through; other providers keep their default transport. It ignores proxy
// environment variables so the dial check sees the real destination.
func (s *Server) providerHTTPClient(name string) *http.Client {
	if name != providerCompat {
		return nil
	}
	s.providerMu.Lock()
	defer s.providerMu.Unlock()
	if s.compatClient == nil {
		dialer := &net.Dialer{Timeout: 10 * time.Second, Control: s.compatDialControl}
		s.compatClient = &http.Client{
			Timeout: 10 * time.Minute,
			Transport: &http.Transport{
				Proxy:                 nil,
				DialContext:           dialer.DialContext,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 5 * time.Minute,
				MaxIdleConnsPerHost:   4,
				IdleConnTimeout:       60 * time.Second,
			},
		}
	}
	return s.compatClient
}

// compatHostCheck refuses at save time an endpoint whose addresses the rule
// forbids, with an error the card can name. The dial check still decides.
func (s *Server) compatHostCheck(ctx context.Context, baseURL string) error {
	u, err := url.Parse(baseURL)
	if err != nil {
		return errCompatBlocked
	}
	host := u.Hostname()
	if addr, err := netip.ParseAddr(host); err == nil {
		if !compatAddrAllowed(addr, s.compatPrivateAllowed()) {
			return errCompatBlocked
		}
		return nil
	}
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(lctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return errProviderUnreachable
	}
	for _, addr := range addrs {
		if !compatAddrAllowed(addr, s.compatPrivateAllowed()) {
			return errCompatBlocked
		}
	}
	return nil
}

func (s *Server) publishCompatWindows(models []ModelOption) {
	windows := map[string]int{}
	for _, m := range models {
		if m.ContextWindow > 0 {
			windows[m.ID] = m.ContextWindow
		}
	}
	provider.SetCompatContextWindows(windows)
}

// fetchCompatModels lists the endpoint's models and merges the typed IDs.
// Reachability problems are an error only when the typed list cannot stand in
// for the server's own; an HTTP 401/403 is always reported.
func (s *Server) fetchCompatModels(ctx context.Context, cfg compatConfig) ([]ModelOption, int, error) {
	listed, status, err := s.listCompatModels(ctx, cfg)
	if err != nil && status != http.StatusUnauthorized && status != http.StatusForbidden && len(cfg.Models) > 0 {
		listed, status, err = nil, http.StatusOK, nil
	}
	if err != nil {
		return nil, status, err
	}
	seen := map[string]bool{}
	out := []ModelOption{}
	for _, m := range listed {
		seen[m.ID] = true
		out = append(out, m)
	}
	for _, id := range cfg.Models {
		full := provider.CompatModelPrefix + id
		if !seen[full] {
			out = append(out, compatOption(id, 0))
		}
	}
	s.enrichOllamaWindows(ctx, cfg, out)
	return out, http.StatusOK, nil
}

func compatOption(id string, window int) ModelOption {
	return ModelOption{ID: provider.CompatModelPrefix + id, Name: id, Provider: "compatible", ReasoningEfforts: []string{}, ContextWindow: max(window, 0)}
}

// compatRequest sends one request to the endpoint through the guarded client.
// A key, when there is one, goes as a bearer token; a key-less endpoint such as
// a local Ollama gets no Authorization header at all.
func (s *Server) compatRequest(ctx context.Context, cfg compatConfig, method, target string, payload any, timeout time.Duration) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var reader io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		reader = strings.NewReader(string(raw))
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, 0, err
	}
	if cfg.Key != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Key)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "tofi/1.0")
	resp, err := s.providerHTTPClient(providerCompat).Do(req)
	if err != nil {
		if errors.Is(err, errCompatBlocked) || strings.Contains(err.Error(), errCompatBlocked.Error()) {
			return nil, 0, errCompatBlocked
		}
		return nil, 0, errProviderUnreachable
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, resp.StatusCode, errProviderUnreachable
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return body, resp.StatusCode, fmt.Errorf("endpoint returned HTTP %d: %s", resp.StatusCode, trimRunes(strings.TrimSpace(string(body)), 200))
	}
	return body, resp.StatusCode, nil
}

// listCompatModels reads GET {base}/models; when that fails for a reason other
// than a refused key, it tries Ollama's own list (GET {root}/api/tags), which
// every Ollama answers whatever its OpenAI layer does.
func (s *Server) listCompatModels(ctx context.Context, cfg compatConfig) ([]ModelOption, int, error) {
	body, status, err := s.compatRequest(ctx, cfg, http.MethodGet, strings.TrimRight(cfg.BaseURL, "/")+"/models", nil, providerKeyVerifyTimeout)
	if err == nil {
		models, e := normalizeCompatModels(body)
		if e == nil && len(models) > 0 {
			return models, status, nil
		}
		err = e
		if err == nil {
			err = errors.New("the endpoint listed no models")
		}
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden || errors.Is(err, errCompatBlocked) {
		return nil, status, err
	}
	if root := ollamaRoot(cfg.BaseURL); root != "" {
		if body, _, e := s.compatRequest(ctx, cfg, http.MethodGet, root+"/api/tags", nil, providerKeyVerifyTimeout); e == nil {
			if models := normalizeOllamaTags(body); len(models) > 0 {
				return models, http.StatusOK, nil
			}
		}
	}
	return nil, status, err
}

// ollamaRoot is the address of Ollama's native API for a base URL that could be
// its OpenAI layer ("http://host:11434/v1" gives "http://host:11434"); "" when
// the path says it is something else.
func ollamaRoot(baseURL string) string {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Host == "" {
		return ""
	}
	path := strings.TrimRight(u.Path, "/")
	if path != "" && path != "/v1" {
		return ""
	}
	u.Path, u.RawQuery, u.Fragment = "", "", ""
	return u.String()
}

// normalizeOllamaTags reads Ollama's GET /api/tags.
func normalizeOllamaTags(body []byte) []ModelOption {
	var raw struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if json.Unmarshal(body, &raw) != nil {
		return nil
	}
	out := []ModelOption{}
	seen := map[string]bool{}
	for _, m := range raw.Models {
		id := strings.TrimSpace(m.Name)
		if id == "" {
			id = strings.TrimSpace(m.Model)
		}
		lower := strings.ToLower(id)
		if id == "" || seen[id] || len(id) > compatMaxModelIDLen || strings.ContainsAny(id, " \t\r\n\x00") || strings.Contains(lower, "embed") || strings.Contains(lower, "rerank") {
			continue
		}
		seen[id] = true
		out = append(out, compatOption(id, 0))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

const (
	ollamaShowLimit       = 50
	ollamaShowParallelism = 4
)

// enrichOllamaWindows fills the context window of models that have none from
// Ollama's POST /api/show: the num_ctx the model is set to run with, else the
// model's own context_length. It does nothing for a server that is not Ollama
// (GET /api/version is how Ollama says so) and leaves a model alone when the
// server already reported its window.
func (s *Server) enrichOllamaWindows(ctx context.Context, cfg compatConfig, models []ModelOption) {
	root := ollamaRoot(cfg.BaseURL)
	if root == "" {
		return
	}
	var todo []int
	for i, m := range models {
		if m.ContextWindow == 0 && len(todo) < ollamaShowLimit {
			todo = append(todo, i)
		}
	}
	if len(todo) == 0 {
		return
	}
	body, _, err := s.compatRequest(ctx, cfg, http.MethodGet, root+"/api/version", nil, 4*time.Second)
	var version struct {
		Version string `json:"version"`
	}
	if err != nil || json.Unmarshal(body, &version) != nil || version.Version == "" {
		return
	}
	var wg sync.WaitGroup
	slots := make(chan struct{}, ollamaShowParallelism)
	for _, i := range todo {
		wg.Add(1)
		slots <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-slots }()
			name := strings.TrimPrefix(models[i].ID, provider.CompatModelPrefix)
			show, _, err := s.compatRequest(ctx, cfg, http.MethodPost, root+"/api/show", map[string]string{"model": name}, 6*time.Second)
			if err == nil {
				models[i].ContextWindow = ollamaContextWindow(show)
			}
		}(i)
	}
	wg.Wait()
}

var ollamaNumCtx = regexp.MustCompile(`(?m)^\s*num_ctx\s+(\d+)\s*$`)

// ollamaContextWindow reads an /api/show answer: the num_ctx parameter when the
// model is set to run with one, else the "<architecture>.context_length" of
// model_info. 0 when it says neither.
func ollamaContextWindow(body []byte) int {
	var raw struct {
		Parameters string         `json:"parameters"`
		ModelInfo  map[string]any `json:"model_info"`
	}
	if json.Unmarshal(body, &raw) != nil {
		return 0
	}
	if m := ollamaNumCtx.FindStringSubmatch(raw.Parameters); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			return n
		}
	}
	window := 0
	for key, value := range raw.ModelInfo {
		if !strings.HasSuffix(key, ".context_length") {
			continue
		}
		if f, ok := value.(float64); ok && f > 0 && int(f) > window {
			window = int(f)
		}
	}
	return window
}

// normalizeCompatModels reads a /v1/models answer. Servers report the context
// window under different names (OpenRouter context_length, vLLM max_model_len,
// others context_window or max_context_length); the first positive one counts.
// Embedding and reranking models are not agent models and are left out.
func normalizeCompatModels(body []byte) ([]ModelOption, error) {
	var raw struct {
		Data []struct {
			ID               string `json:"id"`
			ContextLength    int    `json:"context_length"`
			MaxModelLen      int    `json:"max_model_len"`
			ContextWindow    int    `json:"context_window"`
			MaxContextLength int    `json:"max_context_length"`
			Meta             struct {
				NCtxTrain int `json:"n_ctx_train"`
			} `json:"meta"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode model list: %w", err)
	}
	out := []ModelOption{}
	seen := map[string]bool{}
	for _, m := range raw.Data {
		id := strings.TrimSpace(m.ID)
		lower := strings.ToLower(id)
		if id == "" || seen[id] || len(id) > compatMaxModelIDLen || strings.ContainsAny(id, " \t\r\n\x00") || strings.Contains(lower, "embed") || strings.Contains(lower, "rerank") {
			continue
		}
		seen[id] = true
		window := 0
		for _, w := range []int{m.ContextLength, m.MaxModelLen, m.ContextWindow, m.MaxContextLength, m.Meta.NCtxTrain} {
			if w > 0 {
				window = w
				break
			}
		}
		out = append(out, compatOption(id, window))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > compatMaxModels*5 {
		out = out[:compatMaxModels*5]
	}
	return out, nil
}

// compatBody is the card's request: the endpoint, an optional key, typed IDs.
type compatBody struct {
	BaseURL string   `json:"base_url"`
	Key     string   `json:"key"`
	Models  []string `json:"models"`
}

// compatFromBody validates a request into a config. A blank key keeps the
// stored one only while the endpoint is unchanged, so a saved key is never
// sent to a different address.
func (s *Server) compatFromBody(in compatBody) (compatConfig, string, string) {
	base, err := normalizeCompatBaseURL(in.BaseURL)
	if err != nil {
		return compatConfig{}, "invalid_base_url", err.Error()
	}
	models, err := normalizeCompatModelIDs(in.Models)
	if err != nil {
		return compatConfig{}, "invalid_models", err.Error()
	}
	key := strings.TrimSpace(in.Key)
	if len(key) > compatMaxKeyLen || strings.ContainsAny(key, " \t\r\n\x00") {
		return compatConfig{}, "invalid_key", "Enter the full API key, or leave it empty."
	}
	if key == "" {
		if stored, _, ok := s.compatSettings(); ok && stored.BaseURL == base {
			key = stored.Key
		}
	}
	return compatConfig{BaseURL: base, Key: key, Models: models}, "", ""
}

func (s *Server) routeCompatProvider(w http.ResponseWriter, r *http.Request, sub string) {
	switch {
	case sub == "config" && r.Method == http.MethodPut:
		s.putCompatConfig(w, r)
	case sub == "config" && r.Method == http.MethodDelete:
		if err := s.deleteProviderKey(providerCompat); err != nil {
			writeErr(w, http.StatusInternalServerError, "storage", "could not remove the endpoint")
			return
		}
		s.forgetProviderCatalog(providerCompat)
		_ = s.store.forgetProviderModels(providerCompat)
		provider.SetCompatContextWindows(nil)
		if _, err := s.store.WorkspaceEvent(workspaceScopeConfig); err != nil {
			log.Printf("[workspace-events] record compatible endpoint removal: %v", err)
		}
		writeJSON(w, http.StatusOK, s.providerStatus(providerCompat))
	case sub == "config":
		w.Header().Set("Allow", "PUT, DELETE")
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use PUT or DELETE")
	case sub == "models" && r.Method == http.MethodPost:
		s.probeCompatModels(w, r)
	case sub == "models":
		w.Header().Set("Allow", http.MethodPost)
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
	default:
		writeErr(w, http.StatusNotFound, "not_found", "not found")
	}
}

// compatFailure maps a failed model list to the card's stable error code.
func compatFailure(w http.ResponseWriter, status int, err error) {
	switch {
	case errors.Is(err, errCompatBlocked):
		writeErr(w, http.StatusBadRequest, "endpoint_not_allowed", "This server may not connect to that address. A private or local address is allowed only on a single-account install, or when the operator sets "+compatPrivateEnvFlag+"=1.")
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		writeErr(w, http.StatusBadRequest, "invalid_key", fmt.Sprintf("The endpoint rejected this API key (HTTP %d).", status))
	case errors.Is(err, errProviderUnreachable) || status >= 500 || status == http.StatusTooManyRequests:
		writeErr(w, http.StatusBadGateway, "provider_unreachable", "The endpoint could not be reached from this server; check the address, and add the model IDs by hand if it has no model list.")
	default:
		writeErr(w, http.StatusBadRequest, "no_models", "The endpoint did not return a model list; add the model IDs by hand.")
	}
}

func (s *Server) probeCompatModels(w http.ResponseWriter, r *http.Request) {
	var in compatBody
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "invalid request")
		return
	}
	cfg, code, message := s.compatFromBody(in)
	if code != "" {
		writeErr(w, http.StatusBadRequest, code, message)
		return
	}
	cfg.Models = nil
	if err := s.compatHostCheck(r.Context(), cfg.BaseURL); err != nil {
		compatFailure(w, 0, err)
		return
	}
	models, status, err := s.listCompatModels(r.Context(), cfg)
	if err != nil {
		compatFailure(w, status, err)
		return
	}
	s.enrichOllamaWindows(r.Context(), cfg, models)
	type entry struct {
		ID            string `json:"id"`
		ContextWindow int    `json:"context_window,omitempty"`
	}
	out := make([]entry, 0, len(models))
	for _, m := range models {
		out = append(out, entry{ID: strings.TrimPrefix(m.ID, provider.CompatModelPrefix), ContextWindow: m.ContextWindow})
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": out})
}

func (s *Server) putCompatConfig(w http.ResponseWriter, r *http.Request) {
	var in compatBody
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "invalid request")
		return
	}
	cfg, code, message := s.compatFromBody(in)
	if code != "" {
		writeErr(w, http.StatusBadRequest, code, message)
		return
	}
	if s.secretVault == nil {
		writeErr(w, http.StatusServiceUnavailable, "unavailable", "Secret storage unavailable")
		return
	}
	if err := s.compatHostCheck(r.Context(), cfg.BaseURL); err != nil && (errors.Is(err, errCompatBlocked) || len(cfg.Models) == 0) {
		compatFailure(w, 0, err)
		return
	}
	models, status, err := s.fetchCompatModels(r.Context(), cfg)
	if err != nil {
		log.Printf("[providers] compatible endpoint verification failed: status=%d err=%v", status, err)
		compatFailure(w, status, err)
		return
	}
	if len(models) == 0 {
		compatFailure(w, status, nil)
		return
	}
	sealed, err := json.Marshal(cfg)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "storage", "could not save the endpoint")
		return
	}
	u, _ := url.Parse(cfg.BaseURL)
	if _, err := s.storeProviderSecret(providerCompat, string(sealed), u.Host, "OpenAI-compatible endpoint"); err != nil {
		writeErr(w, http.StatusInternalServerError, "storage", "could not save the endpoint")
		return
	}
	s.rememberProviderCatalog(providerCompat, models)
	if _, err := s.store.WorkspaceEvent(workspaceScopeConfig); err != nil {
		log.Printf("[workspace-events] record compatible endpoint: %v", err)
	}
	s.wakeConversationWorkers()
	writeJSON(w, http.StatusOK, s.providerStatus(providerCompat))
}
