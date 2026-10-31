package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/JackZhao98/tofibot/internal/provider"
)

const syntheticCompatKey = "local-synthetic-key-0001"

type fakeCompatAPI struct {
	*httptest.Server
	mu     sync.Mutex
	models bool // serve /v1/models
	chats  []string
	auths  []string
}

func newFakeCompatAPI(t *testing.T, models bool, reply string) *fakeCompatAPI {
	f := &fakeCompatAPI{models: models}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			if !f.models {
				http.NotFound(w, r)
				return
			}
			_, _ = io.WriteString(w, `{"data":[
				{"id":"qwen3:8b","context_length":40960},
				{"id":"nomic-embed-text"},
				{"id":"llama3.1","max_model_len":131072},
				{"id":"plain"}]}`)
		case "/v1/chat/completions":
			raw, _ := io.ReadAll(r.Body)
			var body struct {
				Model  string `json:"model"`
				Stream bool   `json:"stream"`
			}
			_ = json.Unmarshal(raw, &body)
			f.mu.Lock()
			f.chats = append(f.chats, body.Model)
			f.auths = append(f.auths, r.Header.Get("Authorization"))
			f.mu.Unlock()
			text, _ := json.Marshal(reply)
			if body.Stream {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":`+string(text)+`}}]}`+"\n\n")
				_, _ = io.WriteString(w, `data: {"choices":[{"finish_reason":"stop","delta":{}}],"usage":{"prompt_tokens":9,"completion_tokens":5}}`+"\n\n")
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
				return
			}
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":`+string(text)+`}}],"usage":{"prompt_tokens":9,"completion_tokens":5}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func putCompat(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	return providerRequest(t, s, http.MethodPut, "/api/providers/compatible/config", body)
}

func TestCompatEndpointConfigureListRunAndDelete(t *testing.T) {
	api := newFakeCompatAPI(t, true, "Hello from the local model.")
	s := providerServer(t, nil, nil)
	t.Cleanup(func() { provider.SetCompatContextWindows(nil) })

	rec := putCompat(t, s, `{"base_url":"`+api.URL+`/v1/","key":"`+syntheticCompatKey+`","models":["custom-typed"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), syntheticCompatKey) {
		t.Fatalf("the key must not come back: %s", rec.Body.String())
	}
	var status ProviderStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Configured || status.ID != "compatible" || status.BaseURL != api.URL+"/v1" || status.KeyHint == "" || len(status.Models) != 1 {
		t.Fatalf("status=%+v", status)
	}

	// The catalog: prefixed IDs, no embedding model, typed IDs merged, windows
	// reported by the server (context_length, max_model_len) and none otherwise.
	models, _, _ := s.loadModels(t.Context())
	byID := map[string]ModelOption{}
	for _, m := range models {
		byID[m.ID] = m
	}
	for _, id := range []string{"compat:qwen3:8b", "compat:llama3.1", "compat:plain", "compat:custom-typed"} {
		if m, ok := byID[id]; !ok || m.Provider != "compatible" || len(m.ReasoningEfforts) != 0 {
			t.Fatalf("catalog is missing %s or has it wrong: %+v", id, byID)
		}
	}
	if _, ok := byID["compat:nomic-embed-text"]; ok {
		t.Fatal("an embedding model is not an agent model")
	}
	if byID["compat:qwen3:8b"].ContextWindow != 40960 || byID["compat:llama3.1"].ContextWindow != 131072 || byID["compat:plain"].ContextWindow != 0 {
		t.Fatalf("windows = %d %d %d", byID["compat:qwen3:8b"].ContextWindow, byID["compat:llama3.1"].ContextWindow, byID["compat:plain"].ContextWindow)
	}
	if w := provider.GetContextWindow("compat:qwen3:8b"); w != 40960 || !provider.ContextWindowKnown("compat:qwen3:8b") {
		t.Fatalf("server-reported window did not reach the provider package: %d", w)
	}
	if provider.ContextWindowKnown("compat:plain") {
		t.Fatal("an unreported window must stay on the existing fallback")
	}

	// A run on a prefixed model reaches the endpoint with the bare ID and the key.
	run, messages := runBotThroughProvider(t, s, "compat:llama3.1", "Say hello.")
	if run.Status != "done" {
		t.Fatalf("run=%+v failure=%+v", run, run.failure())
	}
	if last := messages[len(messages)-1]; last.Role != "assistant" || !strings.Contains(last.Content, "Hello from the local model.") {
		t.Fatalf("assistant=%+v", last)
	}
	api.mu.Lock()
	chats, auths := append([]string(nil), api.chats...), append([]string(nil), api.auths...)
	api.mu.Unlock()
	if len(chats) == 0 || chats[0] != "llama3.1" || auths[0] != "Bearer "+syntheticCompatKey {
		t.Fatalf("endpoint saw models=%v auth=%v", chats, auths)
	}

	// The settings survive in the list and leave with DELETE.
	listed := providerRequest(t, s, http.MethodGet, "/api/providers", "")
	if !strings.Contains(listed.Body.String(), `"id":"compatible"`) || strings.Contains(listed.Body.String(), syntheticCompatKey) {
		t.Fatalf("providers list = %s", listed.Body.String())
	}
	del := providerRequest(t, s, http.MethodDelete, "/api/providers/compatible/config", "")
	if del.Code != http.StatusOK || strings.Contains(del.Body.String(), `"configured":true`) {
		t.Fatalf("DELETE status=%d body=%s", del.Code, del.Body.String())
	}
	models, _, _ = s.loadModels(t.Context())
	for _, m := range models {
		if strings.HasPrefix(m.ID, provider.CompatModelPrefix) {
			t.Fatalf("model %s outlived its endpoint", m.ID)
		}
	}
	if provider.ContextWindowKnown("compat:qwen3:8b") {
		t.Fatal("a removed endpoint must stop contributing windows")
	}
}

func TestCompatKeepsStoredKeyOnlyForTheSameEndpoint(t *testing.T) {
	api := newFakeCompatAPI(t, true, "ok")
	other := newFakeCompatAPI(t, true, "ok")
	s := providerServer(t, nil, nil)
	if rec := putCompat(t, s, `{"base_url":"`+api.URL+`/v1","key":"`+syntheticCompatKey+`"}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if rec := putCompat(t, s, `{"base_url":"`+api.URL+`/v1","models":["extra"]}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if cfg, _, _ := s.compatSettings(); cfg.Key != syntheticCompatKey || len(cfg.Models) != 1 {
		t.Fatalf("a blank key on the same endpoint keeps the stored key: %+v", cfg)
	}
	if rec := putCompat(t, s, `{"base_url":"`+other.URL+`/v1"}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if cfg, _, _ := s.compatSettings(); cfg.Key != "" {
		t.Fatalf("a saved key must never be sent to a different address: %+v", cfg)
	}
}

func TestCompatTypedModelsStandInWhenThereIsNoModelList(t *testing.T) {
	api := newFakeCompatAPI(t, false, "ok")
	s := providerServer(t, nil, nil)
	if rec := putCompat(t, s, `{"base_url":"`+api.URL+`/v1"}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no_models") {
		t.Fatalf("no list and no typed models: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := putCompat(t, s, `{"base_url":"`+api.URL+`/v1","models":["my-model", "compat:my-model"]}`); rec.Code != http.StatusOK {
		t.Fatalf("typed models: status=%d body=%s", rec.Code, rec.Body.String())
	}
	models, _, _ := s.loadModels(t.Context())
	if len(models) != 1 || models[0].ID != "compat:my-model" {
		t.Fatalf("catalog = %+v", models)
	}
}

func TestCompatProbeListsModelsWithoutSaving(t *testing.T) {
	api := newFakeCompatAPI(t, true, "ok")
	s := providerServer(t, nil, nil)
	rec := providerRequest(t, s, http.MethodPost, "/api/providers/compatible/models", `{"base_url":"`+api.URL+`"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"llama3.1"`) || !strings.Contains(rec.Body.String(), `"context_window":131072`) {
		t.Fatalf("probe status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, _, ok := s.compatSettings(); ok {
		t.Fatal("probing must not save anything")
	}
}

func TestCompatRejectsBadInput(t *testing.T) {
	s := providerServer(t, nil, nil)
	for body, code := range map[string]string{
		`{"base_url":""}`:                                             "invalid_base_url",
		`{"base_url":"ftp://host/v1"}`:                                "invalid_base_url",
		`{"base_url":"http://user:pw@host/v1"}`:                       "invalid_base_url",
		`{"base_url":"http://host/v1?x=1"}`:                           "invalid_base_url",
		`{"base_url":"http://127.0.0.1:1/v1","key":"a b"}`:            "invalid_key",
		`{"base_url":"http://127.0.0.1:1/v1","models":["a b"]}`:       "invalid_models",
		`{"base_url":"http://169.254.169.254/latest","models":["m"]}`: "endpoint_not_allowed",
		`{"base_url":"http://0.0.0.0:11434/v1","models":["m"]}`:       "endpoint_not_allowed",
	} {
		rec := putCompat(t, s, body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), code) {
			t.Errorf("%s: status=%d body=%s, want %s", body, rec.Code, rec.Body.String(), code)
		}
	}
}

// A member of a multi-account gateway cannot aim the server at its own
// network; the operator can allow it for the whole install.
func TestCompatPrivateAddressesNeedTheAdmin(t *testing.T) {
	api := newFakeCompatAPI(t, true, "ok") // 127.0.0.1
	s := providerServer(t, nil, nil)
	s.accountID = "acct_member"
	for _, path := range []string{"config", "models"} {
		method := http.MethodPut
		if path == "models" {
			method = http.MethodPost
		}
		rec := providerRequest(t, s, method, "/api/providers/compatible/"+path, `{"base_url":"`+api.URL+`/v1"}`)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "endpoint_not_allowed") {
			t.Fatalf("%s in a gateway workspace: status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}
	// The dial check holds even if a blocked address was stored earlier.
	if _, _, err := s.listCompatModels(t.Context(), compatConfig{BaseURL: api.URL + "/v1"}); err == nil {
		t.Fatal("the connection itself must be refused")
	}
	t.Setenv(compatPrivateEnvFlag, "1")
	if rec := putCompat(t, s, `{"base_url":"`+api.URL+`/v1"}`); rec.Code != http.StatusOK {
		t.Fatalf("operator flag: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCompatAddrAllowed(t *testing.T) {
	cases := []struct {
		addr               string
		private, forbidden bool
	}{
		{"8.8.8.8", false, false},
		{"2606:4700::1111", false, false},
		{"127.0.0.1", true, false},
		{"::1", true, false},
		{"10.0.0.5", true, false},
		{"192.168.1.20", true, false},
		{"172.16.3.4", true, false},
		{"fd00::1", true, false},
		{"100.64.0.9", true, false},
		{"::ffff:192.168.1.20", true, false},
		{"169.254.169.254", false, true},
		{"fe80::1", false, true},
		{"0.0.0.0", false, true},
		{"224.0.0.1", false, true},
	}
	for _, c := range cases {
		addr := netip.MustParseAddr(c.addr)
		if got := compatAddrAllowed(addr, true); got == c.forbidden {
			t.Errorf("%s with private allowed = %v", c.addr, got)
		}
		wantPlain := !c.private && !c.forbidden
		if got := compatAddrAllowed(addr, false); got != wantPlain {
			t.Errorf("%s with private not allowed = %v, want %v", c.addr, got, wantPlain)
		}
	}
}

// fakeOllama answers like a local Ollama: no key, an OpenAI layer that may lack
// a model list, the native tag list, version and show endpoints, and chat.
type fakeOllama struct {
	*httptest.Server
	mu    sync.Mutex
	auths []string
	chats []string
}

func newFakeOllama(t *testing.T, openAIModels bool) *fakeOllama {
	f := &fakeOllama{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.auths = append(f.auths, r.Header.Get("Authorization"))
		f.mu.Unlock()
		switch r.URL.Path {
		case "/v1/models":
			if !openAIModels {
				http.NotFound(w, r)
				return
			}
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"llama3.1:latest","object":"model"},{"id":"qwen3:8b","object":"model"}]}`)
		case "/api/tags":
			_, _ = io.WriteString(w, `{"models":[{"name":"qwen3:8b","model":"qwen3:8b"},{"name":"llama3.1:latest","model":"llama3.1:latest"},{"name":"nomic-embed-text:latest"}]}`)
		case "/api/version":
			_, _ = io.WriteString(w, `{"version":"0.12.0"}`)
		case "/api/show":
			raw, _ := io.ReadAll(r.Body)
			var body struct {
				Model string `json:"model"`
			}
			_ = json.Unmarshal(raw, &body)
			switch body.Model {
			case "qwen3:8b":
				_, _ = io.WriteString(w, `{"parameters":"num_ctx                        16384\nstop                           \"<|im_end|>\"","model_info":{"general.architecture":"qwen3","qwen3.context_length":40960}}`)
			case "llama3.1:latest":
				_, _ = io.WriteString(w, `{"parameters":"stop \"<|eot_id|>\"","model_info":{"general.architecture":"llama","llama.context_length":131072}}`)
			default:
				http.NotFound(w, r)
			}
		case "/v1/chat/completions":
			raw, _ := io.ReadAll(r.Body)
			var body struct {
				Model  string `json:"model"`
				Stream bool   `json:"stream"`
			}
			_ = json.Unmarshal(raw, &body)
			f.mu.Lock()
			f.chats = append(f.chats, body.Model)
			f.mu.Unlock()
			if body.Stream {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"role":"assistant","content":"Hello from Ollama."},"finish_reason":null}]}`+"\n\n")
				_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"role":"assistant","content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":4}}`+"\n\n")
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
				return
			}
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Hello from Ollama."}}],"usage":{"prompt_tokens":9,"completion_tokens":4}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func TestCompatOllamaWithoutKeyFallsBackToTagsAndReadsContext(t *testing.T) {
	ollama := newFakeOllama(t, false)
	s := providerServer(t, nil, nil)
	t.Cleanup(func() { provider.SetCompatContextWindows(nil) })
	// The address is Ollama's OpenAI layer; no key is entered.
	if rec := putCompat(t, s, `{"base_url":"`+ollama.URL+`/v1"}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", rec.Code, rec.Body.String())
	}
	models, _, _ := s.loadModels(t.Context())
	byID := map[string]ModelOption{}
	for _, m := range models {
		byID[m.ID] = m
	}
	if len(byID) != 2 || byID["compat:qwen3:8b"].ID == "" || byID["compat:llama3.1:latest"].ID == "" {
		t.Fatalf("the tag list should give the two chat models and skip the embedding model: %+v", byID)
	}
	// num_ctx wins when the model is set to run with one, else the model's own length.
	if byID["compat:qwen3:8b"].ContextWindow != 16384 || byID["compat:llama3.1:latest"].ContextWindow != 131072 {
		t.Fatalf("windows = %d %d", byID["compat:qwen3:8b"].ContextWindow, byID["compat:llama3.1:latest"].ContextWindow)
	}
	if w := provider.GetContextWindow("compat:qwen3:8b"); w != 16384 || !provider.ContextWindowKnown("compat:qwen3:8b") {
		t.Fatalf("window reached the provider package as %d", w)
	}

	run, messages := runBotThroughProvider(t, s, "compat:qwen3:8b", "Say hello.")
	if run.Status != "done" {
		t.Fatalf("run=%+v failure=%+v", run, run.failure())
	}
	if last := messages[len(messages)-1]; !strings.Contains(last.Content, "Hello from Ollama.") {
		t.Fatalf("assistant=%+v", last)
	}
	ollama.mu.Lock()
	defer ollama.mu.Unlock()
	if len(ollama.chats) == 0 || ollama.chats[0] != "qwen3:8b" {
		t.Fatalf("chat models = %v", ollama.chats)
	}
	for _, auth := range ollama.auths {
		if auth != "" {
			t.Fatalf("a key-less endpoint must receive no Authorization header, got %q", auth)
		}
	}
}

func TestCompatOllamaUsesItsOpenAIModelListWhenItHasOne(t *testing.T) {
	ollama := newFakeOllama(t, true)
	s := providerServer(t, nil, nil)
	t.Cleanup(func() { provider.SetCompatContextWindows(nil) })
	rec := providerRequest(t, s, http.MethodPost, "/api/providers/compatible/models", `{"base_url":"`+ollama.URL+`"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"qwen3:8b","context_window":16384`) || !strings.Contains(rec.Body.String(), `"id":"llama3.1:latest","context_window":131072`) {
		t.Fatalf("probe status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestOllamaRootAndShowParsing(t *testing.T) {
	for in, want := range map[string]string{
		"http://host:11434/v1":         "http://host:11434",
		"http://host:11434/v1/":        "http://host:11434",
		"http://host:11434":            "http://host:11434",
		"https://openrouter.ai/api/v1": "",
		"http://host:8000/other":       "",
	} {
		if got := ollamaRoot(in); got != want {
			t.Errorf("ollamaRoot(%q) = %q, want %q", in, got, want)
		}
	}
	if got := ollamaContextWindow([]byte(`{"parameters":"num_ctx 8192","model_info":{"llama.context_length":131072}}`)); got != 8192 {
		t.Errorf("num_ctx = %d", got)
	}
	if got := ollamaContextWindow([]byte(`{"model_info":{"general.architecture":"gemma3","gemma3.context_length":131072}}`)); got != 131072 {
		t.Errorf("context_length = %d", got)
	}
	if got := ollamaContextWindow([]byte(`{}`)); got != 0 {
		t.Errorf("empty = %d", got)
	}
}
