package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

const (
	syntheticOpenAIKey    = "sk-synthetic-openai-key-1234"
	syntheticAnthropicKey = "sk-ant-synthetic-anthropic-5678"
)

// fakeModelAPI serves the OpenAI or Anthropic model list and chat endpoints.
type fakeModelAPI struct {
	*httptest.Server
	mu       sync.Mutex
	key      string
	reply    string
	reject   bool // 401 on chat requests
	models   []string
	chatKeys []string
	chats    []string // requested model IDs
	// workspace, when set, makes the key unscoped: requests must name it.
	workspace string
}

func newFakeOpenAI(t *testing.T, reply string) *fakeModelAPI {
	f := &fakeModelAPI{key: syntheticOpenAIKey, reply: reply}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorized := r.Header.Get("Authorization") == "Bearer "+f.key
		switch r.URL.Path {
		case "/v1/models":
			if !authorized {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, `{"error":{"code":"invalid_api_key"}}`)
				return
			}
			_, _ = io.WriteString(w, `{"object":"list","data":[
				{"id":"gpt-4.1","created":100},
				{"id":"text-embedding-3-large","created":500},
				{"id":"gpt-5-mini","created":300},
				{"id":"gpt-6-luna","created":400},
				{"id":"gpt-6-luna-2026-05-01","created":401},
				{"id":"gpt-4o-audio-preview","created":402},
				{"id":"o4-mini","created":200},
				{"id":"dall-e-3","created":403}]}`)
		case "/v1/responses":
			body, _ := io.ReadAll(r.Body)
			var payload struct {
				Model  string `json:"model"`
				Stream bool   `json:"stream"`
			}
			_ = json.Unmarshal(body, &payload)
			f.mu.Lock()
			f.chatKeys = append(f.chatKeys, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			f.chats = append(f.chats, payload.Model)
			reject := f.reject
			f.mu.Unlock()
			if !authorized || reject {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, `{"error":{"code":"invalid_api_key","message":"Incorrect API key provided"}}`)
				return
			}
			text, _ := json.Marshal(f.reply)
			if payload.Stream {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":"+string(text)+"}\n\n")
				_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":3}}}\n\n")
				return
			}
			_, _ = io.WriteString(w, `{"output":[{"type":"message","content":[{"type":"output_text","text":`+string(text)+`}]}],"usage":{"input_tokens":5,"output_tokens":3}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func newFakeAnthropic(t *testing.T, reply string) *fakeModelAPI {
	f := &fakeModelAPI{key: syntheticAnthropicKey, reply: reply}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorized := r.Header.Get("x-api-key") == f.key && r.Header.Get("anthropic-version") != ""
		if authorized && r.URL.Path == "/v1/organizations/workspaces" {
			// The default workspace is listed only with include_default=true.
			data := `{"id":"wrkspc_OtherTeamWorkspace0","archived_at":null,"name":"Other"}`
			if r.URL.Query().Get("include_default") == "true" {
				data = `{"id":"` + f.workspace + `","archived_at":null,"name":"Default Workspace"},` + data
			}
			_, _ = io.WriteString(w, `{"data":[`+data+`],"has_more":false}`)
			return
		}
		if authorized && f.workspace != "" && r.Header.Get("anthropic-workspace-id") != f.workspace {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"This API key is not scoped to a workspace, so this request must include the anthropic-workspace-id header with the ID of the workspace to use."}}`)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			if !authorized {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
				return
			}
			_, _ = io.WriteString(w, `{"data":[
				{"type":"model","id":"claude-opus-5-5","display_name":"Claude Opus 5.5","created_at":"2026-08-01T00:00:00Z"},
				{"type":"model","id":"claude-haiku-4-5-20251001","display_name":"Claude Haiku 4.5","created_at":"2025-10-01T00:00:00Z"},
				{"type":"model","id":"claude-sonnet-5-5","display_name":"Claude Sonnet 5.5","created_at":"2026-08-02T00:00:00Z"}],"has_more":false}`)
		case "/v1/messages":
			body, _ := io.ReadAll(r.Body)
			var payload struct {
				Model  string `json:"model"`
				Stream bool   `json:"stream"`
			}
			_ = json.Unmarshal(body, &payload)
			f.mu.Lock()
			f.chatKeys = append(f.chatKeys, r.Header.Get("x-api-key"))
			f.chats = append(f.chats, payload.Model)
			reject := f.reject
			f.mu.Unlock()
			if !authorized || reject {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
				return
			}
			text, _ := json.Marshal(f.reply)
			if payload.Stream {
				w.Header().Set("Content-Type", "text/event-stream")
				for _, event := range []string{
					`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"` + payload.Model + `","usage":{"input_tokens":5,"output_tokens":1}}}`,
					`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
					`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":` + string(text) + `}}`,
					`{"type":"content_block_stop","index":0}`,
					`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
					`{"type":"message_stop"}`,
				} {
					var head struct {
						Type string `json:"type"`
					}
					_ = json.Unmarshal([]byte(event), &head)
					_, _ = io.WriteString(w, "event: "+head.Type+"\ndata: "+event+"\n\n")
				}
				return
			}
			_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":`+string(text)+`}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":3}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeModelAPI) requests() ([]string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.chats...), append([]string(nil), f.chatKeys...)
}

func providerServer(t *testing.T, openai, anthropic *fakeModelAPI) *Server {
	t.Helper()
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if !s.codexManaged || s.engine == nil {
		t.Fatal("fixture requires the server-owned routed engine")
	}
	if openai != nil {
		s.providerEndpoints[providerOpenAI] = openai.URL + "/v1"
	}
	if anthropic != nil {
		s.providerEndpoints[providerAnthropic] = anthropic.URL
	}
	return s
}

func providerRequest(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func putProviderKeyOK(t *testing.T, s *Server, name, key string) ProviderStatus {
	t.Helper()
	rec := providerRequest(t, s, http.MethodPut, "/api/providers/"+name+"/key", `{"key":"`+key+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT %s key status=%d body=%s", name, rec.Code, rec.Body.String())
	}
	var status ProviderStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	return status
}

func listProviders(t *testing.T, s *Server) map[string]ProviderStatus {
	t.Helper()
	rec := providerRequest(t, s, http.MethodGet, "/api/providers", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET providers status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Providers []ProviderStatus `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	out := map[string]ProviderStatus{}
	var order []string
	for _, p := range body.Providers {
		out[p.ID] = p
		order = append(order, p.ID)
	}
	if strings.Join(order, ",") != "codex,openai,anthropic" {
		t.Fatalf("provider order=%v", order)
	}
	return out
}

func TestModelProviderRouting(t *testing.T) {
	for model, want := range map[string]string{
		"codex-gpt-6-luna":          providerCodex,
		"codex-auto-review":         providerCodex,
		"claude-opus-5-5":           providerAnthropic,
		"claude-haiku-4-5-20251001": providerAnthropic,
		"gpt-6-luna":                providerOpenAI,
		"o4-mini":                   providerOpenAI,
		"custom-model":              providerOpenAI,
	} {
		if got := runtime.ModelProvider(model); got != want {
			t.Errorf("%s routed to %s, want %s", model, got, want)
		}
	}
}

func TestProvidersAPIConfigureVerifyAndDelete(t *testing.T) {
	openai, anthropic := newFakeOpenAI(t, "ok"), newFakeAnthropic(t, "ok")
	s := providerServer(t, openai, anthropic)
	initial := listProviders(t, s)
	if initial["codex"].Kind != "oauth" || initial["codex"].Status != "disconnected" || initial["codex"].Configured {
		t.Fatalf("codex=%+v", initial["codex"])
	}
	if initial["openai"].Configured || initial["anthropic"].Configured || initial["anthropic"].Label != "Claude" || initial["openai"].Kind != "api_key" {
		t.Fatalf("initial providers=%+v", initial)
	}
	if s.modelConfigured() {
		t.Fatal("no provider is configured yet")
	}

	rejected := providerRequest(t, s, http.MethodPut, "/api/providers/openai/key", `{"key":"sk-wrong-key-0000"}`)
	if rejected.Code != http.StatusBadRequest || !strings.Contains(rejected.Body.String(), `"invalid_key"`) || strings.Contains(rejected.Body.String(), "sk-wrong") {
		t.Fatalf("rejected key status=%d body=%s", rejected.Code, rejected.Body.String())
	}
	if _, ok := s.providerKey(providerOpenAI); ok {
		t.Fatal("a rejected key was stored")
	}
	if rec := providerRequest(t, s, http.MethodPut, "/api/providers/gemini/key", `{"key":"x"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown provider status=%d", rec.Code)
	}

	status := putProviderKeyOK(t, s, "openai", syntheticOpenAIKey)
	if !status.Configured || status.KeyHint != "…1234" || status.VerifiedAt == "" || status.Error != "" {
		t.Fatalf("openai status=%+v", status)
	}
	putProviderKeyOK(t, s, "anthropic", syntheticAnthropicKey)
	if !s.modelConfigured() {
		t.Fatal("an API key provider must configure the model")
	}
	var config map[string]any
	_ = json.Unmarshal(providerRequest(t, s, http.MethodGet, "/api/config", "").Body.Bytes(), &config)
	if config["model_configured"] != true || config["provider"] != providerOpenAI {
		t.Fatalf("config=%v", config)
	}
	for _, path := range []string{"/api/providers", "/api/models", "/api/config", "/api/computer/credentials"} {
		body := providerRequest(t, s, http.MethodGet, path, "").Body.String()
		if strings.Contains(body, syntheticOpenAIKey) || strings.Contains(body, syntheticAnthropicKey) || strings.Contains(body, "ciphertext") {
			t.Fatalf("%s leaked a key: %s", path, body)
		}
	}

	if rec := providerRequest(t, s, http.MethodDelete, "/api/providers/openai/key", ""); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"configured":true`) {
		t.Fatalf("delete status=%d body=%s", rec.Code, rec.Body.String())
	}
	after := listProviders(t, s)
	if after["openai"].Configured || !after["anthropic"].Configured {
		t.Fatalf("after delete=%+v", after)
	}
	if _, err := s.providerCredential(context.Background(), providerOpenAI); err == nil || !strings.Contains(err.Error(), "model provider is not configured: openai") {
		t.Fatalf("deleted provider credential err=%v", err)
	}
}

func TestProviderKeyNeverReachesComputerCredentials(t *testing.T) {
	openai := newFakeOpenAI(t, "ok")
	s := providerServer(t, openai, nil)
	putProviderKeyOK(t, s, "openai", syntheticOpenAIKey)
	list := providerRequest(t, s, http.MethodGet, "/api/computer/credentials", "")
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), "provider:openai") || strings.Contains(list.Body.String(), modelProviderKind) {
		t.Fatalf("credentials list=%d %s", list.Code, list.Body.String())
	}
	b, _ := s.store.CreateBot("vault", "", "gpt-6-luna")
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/computer/credentials/provider:openai/apply", `{"bot_id":"` + b.ID + `"}`},
		{http.MethodDelete, "/api/computer/credentials/provider:openai", ""},
		{http.MethodPost, "/api/secret-inputs/provider:openai", `{"value":"replacement"}`},
	} {
		if rec := providerRequest(t, s, tc.method, tc.path, tc.body); rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s status=%d body=%s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
	if state, ok := s.providerKey(providerOpenAI); !ok || state.Key != syntheticOpenAIKey {
		t.Fatal("credential routes changed the provider key")
	}
}

func TestMergedModelCatalogOrderAndShape(t *testing.T) {
	openai, anthropic := newFakeOpenAI(t, "ok"), newFakeAnthropic(t, "ok")
	s := providerServer(t, openai, anthropic)
	if err := s.codex.SaveAccessOnlyCredential("synthetic-access", "", time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	s.modelCatalog = []ModelOption{{ID: "codex-gpt-6-luna", Name: "GPT-6 Luna", ReasoningEfforts: []string{"low", "medium"}, DefaultReasoning: "medium"}}
	s.modelCatalogAt, s.modelCatalogSource = time.Now(), "live"
	putProviderKeyOK(t, s, "openai", syntheticOpenAIKey)
	putProviderKeyOK(t, s, "anthropic", syntheticAnthropicKey)

	rec := providerRequest(t, s, http.MethodGet, "/api/models", "")
	var body struct {
		Models  []ModelOption `json:"models"`
		Source  string        `json:"source"`
		Warning string        `json:"warning"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var ids []string
	byID := map[string]ModelOption{}
	for _, m := range body.Models {
		ids = append(ids, m.Provider+":"+m.ID)
		byID[m.ID] = m
	}
	want := "codex:codex-gpt-6-luna,openai:gpt-6-luna,openai:gpt-5-mini,openai:o4-mini,openai:gpt-4.1,anthropic:claude-sonnet-5-5,anthropic:claude-opus-5-5,anthropic:claude-haiku-4-5-20251001"
	if strings.Join(ids, ",") != want || body.Source != "live" || body.Warning != "" {
		t.Fatalf("models=%s source=%s warning=%q", strings.Join(ids, ","), body.Source, body.Warning)
	}
	if byID["codex-gpt-6-luna"].Name != "Codex · GPT-6 Luna" || byID["gpt-6-luna"].Name != "GPT-6 Luna" || byID["claude-opus-5-5"].Name != "Claude Opus 5.5" {
		t.Fatalf("names=%+v", byID)
	}
	if got := strings.Join(byID["gpt-6-luna"].ReasoningEfforts, ","); got != "low,medium,high" || byID["gpt-6-luna"].DefaultReasoning != "medium" {
		t.Fatalf("openai reasoning=%+v", byID["gpt-6-luna"])
	}
	if len(byID["gpt-4.1"].ReasoningEfforts) != 0 || byID["gpt-4.1"].DefaultReasoning != "" {
		t.Fatalf("non-reasoning model=%+v", byID["gpt-4.1"])
	}
	if got := strings.Join(byID["claude-opus-5-5"].ReasoningEfforts, ","); got != "low,medium,high,xhigh,max" {
		t.Fatalf("anthropic reasoning=%s", got)
	}
	if !strings.Contains(rec.Body.String(), `"reasoning_efforts":[]`) {
		t.Fatalf("empty efforts must encode as []: %s", rec.Body.String())
	}

	// Per-provider cache: a dead endpoint serves the stored snapshot.
	openai.Close()
	s.forgetProviderCatalog(providerOpenAI)
	models, source, warning := s.loadModels(context.Background())
	if source != "cache" || !strings.Contains(warning, "OpenAI") || len(models) != len(body.Models) {
		t.Fatalf("cached models=%d source=%s warning=%q", len(models), source, warning)
	}
}

func TestValidationAcceptsMergedCatalogModels(t *testing.T) {
	openai, anthropic := newFakeOpenAI(t, "ok"), newFakeAnthropic(t, "ok")
	s := providerServer(t, openai, anthropic)
	putProviderKeyOK(t, s, "openai", syntheticOpenAIKey)
	putProviderKeyOK(t, s, "anthropic", syntheticAnthropicKey)
	ctx := context.Background()
	for _, tc := range []struct {
		model, effort string
		ok            bool
	}{
		{"claude-opus-5-5", "max", true},
		{"claude-opus-5-5", "minimal", false},
		{"gpt-6-luna", "high", true},
		{"gpt-6-luna", "max", false},
		{"gpt-4.1", "medium", true}, // no reasoning controls: effort ignored
		{"gpt-nonexistent", "", false},
		{"codex-gpt-6-luna", "", false}, // Codex is not connected and has no catalog here
	} {
		err := s.validateModelChoice(ctx, tc.model, tc.effort)
		if (err == nil) != tc.ok {
			t.Errorf("validate %s/%s err=%v", tc.model, tc.effort, err)
		}
	}
	if got := s.defaultReasoningForModel(ctx, "claude-sonnet-5-5"); got != "medium" {
		t.Fatalf("default reasoning=%q", got)
	}
	rec := providerRequest(t, s, http.MethodPut, "/api/model-settings", `{"model":"claude-sonnet-5-5"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"reasoning_effort":"medium"`) {
		t.Fatalf("model settings=%d %s", rec.Code, rec.Body.String())
	}
	rec = providerRequest(t, s, http.MethodPost, "/api/bots", `{"name":"claude bot","model":"claude-opus-5-5","reasoning_effort":"xhigh"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("bot create=%d %s", rec.Code, rec.Body.String())
	}
	var created Bot
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if rec := providerRequest(t, s, http.MethodPatch, "/api/bots/"+created.ID, `{"model":"gpt-nonexistent"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown model patch=%d %s", rec.Code, rec.Body.String())
	}
	if rec := providerRequest(t, s, http.MethodPatch, "/api/bots/"+created.ID, `{"model":"o4-mini","reasoning_effort":"low"}`); rec.Code != http.StatusOK {
		t.Fatalf("openai model patch=%d %s", rec.Code, rec.Body.String())
	}
	if err := s.validateWorkspaceModel(ctx, "gpt-5-mini"); err != nil {
		t.Fatalf("workspace model from catalog: %v", err)
	}
	if err := s.validateWorkspaceModel(ctx, "gpt-nonexistent"); err == nil {
		t.Fatal("workspace accepted an unknown model")
	}
}

func TestCodexModelEquivalenceStaysWithoutOpenAIKey(t *testing.T) {
	s := providerServer(t, nil, nil)
	models := []ModelOption{{ID: "codex-gpt-6-luna", Provider: "codex"}}
	if _, ok := s.matchModel(models, "gpt-6-luna"); !ok {
		t.Fatal("Codex-only workspaces keep the unprefixed Codex equivalence")
	}
	if _, ok := s.matchModel(models, "claude-opus-5-5"); ok {
		t.Fatal("an Anthropic ID never matches a Codex entry")
	}
}

func TestBackgroundModelPreferenceOrder(t *testing.T) {
	openai, anthropic := newFakeOpenAI(t, "ok"), newFakeAnthropic(t, "ok")
	s := providerServer(t, openai, anthropic)
	if s.backgroundModel(backgroundTriage) != codexTriageModel || s.backgroundModel(backgroundReview) != codexReviewModel {
		t.Fatal("an unconfigured workspace keeps the Codex defaults")
	}
	putProviderKeyOK(t, s, "anthropic", syntheticAnthropicKey)
	if got := s.backgroundModel(backgroundReview); got != "claude-haiku-4-5-20251001" {
		t.Fatalf("anthropic background=%s", got)
	}
	putProviderKeyOK(t, s, "openai", syntheticOpenAIKey)
	if got := s.backgroundModel(backgroundTriage); got != "gpt-5-mini" {
		t.Fatalf("openai background=%s", got)
	}
	if err := s.codex.SaveAccessOnlyCredential("synthetic-access", "", time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if s.backgroundModel(backgroundTriage) != codexTriageModel || s.backgroundModel(backgroundReview) != codexReviewModel {
		t.Fatal("Codex is preferred when connected")
	}
	s.triageModel = "gpt-6-luna"
	if s.triageModelName() != "gpt-6-luna" || s.backgroundModel(backgroundReview) != codexReviewModel {
		t.Fatal("TOFI_TRIAGE_MODEL overrides triage only")
	}
}

func TestBackgroundReviewerUsesAPIKeyProviderWithoutCodex(t *testing.T) {
	anthropic := newFakeAnthropic(t, `{"decision":"allow","reason":"Read-only page navigation."}`)
	s := providerServer(t, nil, anthropic)
	putProviderKeyOK(t, s, "anthropic", syntheticAnthropicKey)
	allow, _ := s.reviewAction(context.Background(), actionReview{Kind: "click", Effect: "navigate", Element: "Next"})
	chats, keys := anthropic.requests()
	if !allow || len(chats) != 1 || chats[0] != "claude-haiku-4-5-20251001" || keys[0] != syntheticAnthropicKey {
		t.Fatalf("allow=%v chats=%v", allow, chats)
	}
	terms, err := s.expandToolSearchQuery(context.Background(), "calendar")
	if err == nil || terms != nil {
		// The fake reply is not a JSON array, so expansion must fail closed.
		t.Fatalf("terms=%v err=%v", terms, err)
	}
	if chats, _ := anthropic.requests(); len(chats) != 2 {
		t.Fatalf("tool search expansion did not use the Anthropic key: %v", chats)
	}
}

func runBotThroughProvider(t *testing.T, s *Server, model, prompt string) (Run, []Message) {
	t.Helper()
	rec := providerRequest(t, s, http.MethodPost, "/api/bots", `{"name":"routed","model":"`+model+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("bot create=%d %s", rec.Code, rec.Body.String())
	}
	var bot Bot
	if err := json.Unmarshal(rec.Body.Bytes(), &bot); err != nil {
		t.Fatal(err)
	}
	c, err := s.store.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	_, r, _, err := s.store.AddUserRun(c.ID, bot.ID, prompt, "client-"+model)
	if err != nil {
		t.Fatal(err)
	}
	s.execute(c, r)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := s.store.GetRun(r.ID)
		if got.Status == "done" || got.Status == "failed" {
			messages, _, _ := s.store.Messages(c.ID, 0, 50)
			return got, messages
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, _ := s.store.GetRun(r.ID)
	t.Fatalf("run did not finish: %+v", got)
	return got, nil
}

func TestRunWithOpenAIKeyModelEndToEnd(t *testing.T) {
	openai := newFakeOpenAI(t, "Hello from the OpenAI API.")
	s := providerServer(t, openai, nil)
	putProviderKeyOK(t, s, "openai", syntheticOpenAIKey)
	run, messages := runBotThroughProvider(t, s, "gpt-6-luna", "Say hello.")
	if run.Status != "done" {
		t.Fatalf("run=%+v failure=%+v", run, run.failure())
	}
	last := messages[len(messages)-1]
	if last.Role != "assistant" || !strings.Contains(last.Content, "Hello from the OpenAI API.") {
		t.Fatalf("assistant=%+v", last)
	}
	chats, keys := openai.requests()
	if len(chats) == 0 || chats[0] != "gpt-6-luna" || keys[0] != syntheticOpenAIKey {
		t.Fatalf("openai chats=%v", chats)
	}
}

func TestRunWithAnthropicModelEndToEnd(t *testing.T) {
	anthropic := newFakeAnthropic(t, "Hello from Claude.")
	s := providerServer(t, nil, anthropic)
	putProviderKeyOK(t, s, "anthropic", syntheticAnthropicKey)
	run, messages := runBotThroughProvider(t, s, "claude-opus-5-5", "Say hello.")
	if run.Status != "done" {
		t.Fatalf("run=%+v failure=%+v", run, run.failure())
	}
	last := messages[len(messages)-1]
	if last.Role != "assistant" || !strings.Contains(last.Content, "Hello from Claude.") {
		t.Fatalf("assistant=%+v", last)
	}
	chats, keys := anthropic.requests()
	if len(chats) == 0 || chats[0] != "claude-opus-5-5" || keys[0] != syntheticAnthropicKey {
		t.Fatalf("anthropic chats=%v keys=%d", chats, len(keys))
	}
}

func TestRunForUnconfiguredProviderFailsTyped(t *testing.T) {
	openai := newFakeOpenAI(t, "ok")
	s := providerServer(t, openai, nil)
	putProviderKeyOK(t, s, "openai", syntheticOpenAIKey)
	// Created while Claude was configured; the key is now gone.
	b, err := s.store.CreateBot("claude", "", "claude-opus-5-5")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, r, _, err := s.store.AddUserRun(c.ID, b.ID, "hello", "client-unconfigured")
	if err != nil {
		t.Fatal(err)
	}
	s.execute(c, r)
	got := waitRun(t, s, r.ID, "failed")
	failure := got.failure()
	if failure == nil || failure.Code != "model_unconfigured" || !strings.Contains(failure.Message, "Claude API key") {
		t.Fatalf("failure=%+v error=%q", failure, got.Error)
	}
}

func TestAPIKeyRejectionMarksProviderNotCodex(t *testing.T) {
	anthropic := newFakeAnthropic(t, "ok")
	s := providerServer(t, nil, anthropic)
	if err := s.codex.SaveAccessOnlyCredential("synthetic-access", "", time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	putProviderKeyOK(t, s, "anthropic", syntheticAnthropicKey)
	anthropic.mu.Lock()
	anthropic.reject = true
	anthropic.mu.Unlock()
	run, _ := runBotThroughProvider(t, s, "claude-opus-5-5", "hello")
	failure := run.failure()
	if run.Status != "failed" || failure == nil || failure.Code != "model_auth_invalid" || !strings.Contains(failure.Message, "Claude rejected the stored API key") {
		t.Fatalf("run=%+v failure=%+v", run, failure)
	}
	providers := listProviders(t, s)
	if !providers["anthropic"].Configured || providers["anthropic"].Error == "" {
		t.Fatalf("anthropic=%+v", providers["anthropic"])
	}
	if status := s.codex.Status(); !status.Connected || status.NeedsReconnect {
		t.Fatalf("codex status changed: %+v", status)
	}
	if s.usableProviderKey(providerAnthropic) {
		t.Fatal("a rejected key must not count as usable")
	}
	// Replacing the key clears the attention state.
	anthropic.mu.Lock()
	anthropic.reject = false
	anthropic.mu.Unlock()
	if status := putProviderKeyOK(t, s, "anthropic", syntheticAnthropicKey); status.Error != "" {
		t.Fatalf("replaced key status=%+v", status)
	}
}

func TestCodexDisconnectKeepsAPIKeyEngine(t *testing.T) {
	openai := newFakeOpenAI(t, "still here")
	s := providerServer(t, openai, nil)
	if err := s.codex.SaveAccessOnlyCredential("synthetic-access", "", time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	putProviderKeyOK(t, s, "openai", syntheticOpenAIKey)
	if rec := providerRequest(t, s, http.MethodDelete, "/api/auth/codex", ""); rec.Code != http.StatusOK {
		t.Fatalf("disconnect=%d %s", rec.Code, rec.Body.String())
	}
	if s.engine == nil || !s.modelConfigured() {
		t.Fatal("Codex disconnect removed the engine while OpenAI is configured")
	}
	run, _ := runBotThroughProvider(t, s, "gpt-5-mini", "hello")
	if run.Status != "done" {
		t.Fatalf("run=%+v", run)
	}
}

func TestRunFailureMessagesNameProvider(t *testing.T) {
	for _, tc := range []struct{ model, err, code, want string }{
		{"claude-opus-5-5", "LLM call failed: model provider is not configured: anthropic", "model_unconfigured", "Claude API key"},
		{"gpt-6-luna", "LLM call failed: openai API error (HTTP 401): {}", "model_auth_invalid", "OpenAI rejected"},
		{"codex-gpt-6-luna", "LLM call failed: openai API error (HTTP 401): {}", "model_auth_invalid", "Codex"},
		{"codex-gpt-6-luna", "LLM call failed: Codex is not connected", "model_unconfigured", "Codex account"},
		{"", "provider is required", "model_unconfigured", "Connect Codex or add an OpenAI or Claude API key"},
		{"claude-opus-5-5", "anthropic API error (HTTP 400): credit balance is too low", "model_quota_exhausted", "Claude account"},
	} {
		failure := Run{Status: "failed", Model: tc.model, Error: tc.err}.failure()
		if failure == nil || failure.Code != tc.code || !strings.Contains(failure.Message, tc.want) {
			t.Errorf("%s/%s failure=%+v", tc.model, tc.err, failure)
		}
	}
}

func TestDictationAcceptsOpenAIKey(t *testing.T) {
	openai := newFakeOpenAI(t, "ok")
	s := providerServer(t, openai, nil)
	if s.dictationAuthSource() != "" {
		t.Fatal("no dictation credential expected")
	}
	putProviderKeyOK(t, s, "openai", syntheticOpenAIKey)
	if s.dictationAuthSource() != "openai" {
		t.Fatalf("auth source=%q", s.dictationAuthSource())
	}
	key, err := s.dictationCredential(context.Background())
	if err != nil || key != syntheticOpenAIKey {
		t.Fatalf("dictation credential err=%v", err)
	}
	s.transcriptionURL = "https://gateway.example/v1/audio/transcriptions"
	if _, err := s.dictationCredential(context.Background()); err == nil {
		t.Fatal("the OpenAI key must never be sent to a custom gateway")
	}
}

func TestEnvironmentKeySeedsProviderAndVaultOverrides(t *testing.T) {
	openai := newFakeOpenAI(t, "ok")
	s, err := NewServer(Config{DataDir: t.TempDir(), Provider: "openai", ProviderAPIKey: "sk-env-only-9999", ProviderBaseURL: openai.URL + "/v1", DefaultModel: "gpt-6-luna"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if key, err := s.providerCredential(context.Background(), providerOpenAI); err != nil || key != "sk-env-only-9999" {
		t.Fatalf("env key err=%v", err)
	}
	if st := s.providerStatus(providerOpenAI); !st.Configured || st.Source != "environment" || st.KeyHint != "…9999" {
		t.Fatalf("env status=%+v", st)
	}
	putProviderKeyOK(t, s, "openai", syntheticOpenAIKey)
	if key, _ := s.providerCredential(context.Background(), providerOpenAI); key != syntheticOpenAIKey {
		t.Fatal("vault key must override the environment key")
	}
	isolated, err := NewServer(Config{DataDir: t.TempDir(), Provider: "openai", ProviderAPIKey: "sk-env-only-9999", IsolatedWorkspace: true})
	if err != nil {
		t.Fatal(err)
	}
	defer isolated.Close()
	if _, err := isolated.providerCredential(context.Background(), providerOpenAI); err == nil {
		t.Fatal("isolated workspaces never inherit process credentials")
	}
}

func TestProviderKeySurvivesRestart(t *testing.T) {
	openai := newFakeOpenAI(t, "ok")
	dir := t.TempDir()
	s, err := NewServer(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	s.providerEndpoints[providerOpenAI] = openai.URL + "/v1"
	putProviderKeyOK(t, s, "openai", syntheticOpenAIKey)
	s.Close()
	s, err = NewServer(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	status := s.providerStatus(providerOpenAI)
	if !status.Configured || status.KeyHint != "…1234" || status.VerifiedAt == "" {
		t.Fatalf("status after restart=%+v", status)
	}
	if !s.modelConfigured() {
		t.Fatal("restored key must configure the model")
	}
}

func TestAccountGateDispatchesProviderRoutesToWorkspace(t *testing.T) {
	g := accountFixture(t)
	admin, err := g.create(context.Background(), "admin", "admin@example.test", "SyntheticPassword123!", true)
	if err != nil {
		t.Fatal(err)
	}
	cookie := accountCookie(t, g, admin)
	if w := accountRequest(g, http.MethodGet, "/api/providers", "", cookie); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"id":"anthropic"`) {
		t.Fatalf("providers through account gate %d %s", w.Code, w.Body.String())
	}
	if w := accountRequest(g, http.MethodDelete, "/api/providers/openai/key", "", cookie); w.Code != http.StatusOK {
		t.Fatalf("provider key delete through account gate %d %s", w.Code, w.Body.String())
	}
}

func TestUnscopedAnthropicKeyNeedsWorkspaceID(t *testing.T) {
	anthropic := newFakeAnthropic(t, "Hello from a workspace.")
	anthropic.workspace = "wrkspc_01JwQvzr7rXLA5AGx3HKfFUJ"
	s := providerServer(t, nil, anthropic)
	// Without a workspace ID the org's Default Workspace is found and used.
	auto := providerRequest(t, s, http.MethodPut, "/api/providers/anthropic/key", `{"key":"`+syntheticAnthropicKey+`"}`)
	if auto.Code != http.StatusOK {
		t.Fatalf("unscoped key should resolve the default workspace: %d %s", auto.Code, auto.Body.String())
	}
	if run, _ := runBotThroughProvider(t, s, "claude-opus-5-5", "Say hello."); run.Status != "done" {
		t.Fatalf("run with discovered workspace=%+v", run)
	}
	bad := providerRequest(t, s, http.MethodPut, "/api/providers/anthropic/key", `{"key":"`+syntheticAnthropicKey+`","workspace_id":"not-a-workspace"}`)
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "invalid_workspace") {
		t.Fatalf("malformed workspace: %d %s", bad.Code, bad.Body.String())
	}
	ok := providerRequest(t, s, http.MethodPut, "/api/providers/anthropic/key", `{"key":"`+syntheticAnthropicKey+`","workspace_id":"wrkspc_01JwQvzr7rXLA5AGx3HKfFUJ"}`)
	if ok.Code != http.StatusOK || strings.Contains(ok.Body.String(), "wrkspc_") && strings.Contains(ok.Body.String(), "key_hint\":\"…FUJ") {
		t.Fatalf("workspace key: %d %s", ok.Code, ok.Body.String())
	}
	if hint := listProviders(t, s)["anthropic"].KeyHint; hint != keyHint(syntheticAnthropicKey) {
		t.Fatalf("hint %q must come from the key, not the workspace", hint)
	}
	run, _ := runBotThroughProvider(t, s, "claude-opus-5-5", "Say hello.")
	if run.Status != "done" {
		t.Fatalf("run=%+v failure=%+v", run, run.failure())
	}
}
