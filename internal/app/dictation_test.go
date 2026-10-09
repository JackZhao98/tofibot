package app

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDictationSettingsAndTranscription(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("authorization header = %q", r.Header.Get("Authorization"))
		}
		if err := r.ParseMultipartForm(maxDictationBytes); err != nil {
			t.Fatalf("parse provider form: %v", err)
		}
		if got := r.FormValue("model"); got != "gpt-4o-transcribe" {
			t.Fatalf("provider model = %q", got)
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("provider file: %v", err)
		}
		defer file.Close()
		content, _ := io.ReadAll(file)
		if string(content) != "audio" {
			t.Fatalf("provider audio = %q", content)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"hello from dictate"}`))
	}))
	defer provider.Close()

	server, err := NewServer(Config{DataDir: t.TempDir(), TranscriptionAPIKey: "test-key", TranscriptionURL: provider.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	initial := httptest.NewRecorder()
	server.Handler().ServeHTTP(initial, httptest.NewRequest(http.MethodGet, "/api/dictation-settings", nil))
	if initial.Code != http.StatusOK || !strings.Contains(initial.Body.String(), "gpt-4o-mini-transcribe") || !strings.Contains(initial.Body.String(), "gpt-4o-transcribe") {
		t.Fatalf("initial settings: %d %s", initial.Code, initial.Body.String())
	}
	if strings.Contains(initial.Body.String(), `"description"`) || strings.Contains(initial.Body.String(), `"cost"`) {
		t.Fatalf("settings must not carry display copy (the UI renders it from i18n): %s", initial.Body.String())
	}

	save := httptest.NewRecorder()
	server.Handler().ServeHTTP(save, httptest.NewRequest(http.MethodPut, "/api/dictation-settings", strings.NewReader(`{"model":"gpt-4o-transcribe"}`)))
	if save.Code != http.StatusOK {
		t.Fatalf("save settings: %d %s", save.Code, save.Body.String())
	}

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "dictation.webm")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write([]byte("audio")); err != nil {
		t.Fatal(err)
	}
	if err = form.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/dictate", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	result := httptest.NewRecorder()
	server.Handler().ServeHTTP(result, req)
	if result.Code != http.StatusOK {
		t.Fatalf("dictate: %d %s", result.Code, result.Body.String())
	}
	var response struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil || response.Text != "hello from dictate" {
		t.Fatalf("dictate response: %s err=%v", result.Body.String(), err)
	}
}

func TestDictateRequiresAuthentication(t *testing.T) {
	server, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	result := httptest.NewRecorder()
	server.Handler().ServeHTTP(result, httptest.NewRequest(http.MethodPost, "/api/dictate", nil))
	if result.Code != http.StatusServiceUnavailable || !strings.Contains(result.Body.String(), "dictation_unconfigured") {
		t.Fatalf("missing key: %d %s", result.Code, result.Body.String())
	}
}

// These cases use only synthetic credentials and an in-memory HTTP transport.
func TestDictationAuthentication(t *testing.T) {
	for _, tc := range []struct {
		name, key, endpoint, model, source, code                       string
		expired, accessOnly, refreshFailure, redirect, providerFailure bool
		status                                                         int
	}{
		{name: "codex", model: "gpt-4o-mini-transcribe", source: "codex", status: 200},
		{name: "refresh", expired: true, model: "gpt-4o-transcribe", source: "codex", status: 200},
		{name: "explicit_key_priority", key: "explicit-test-key", expired: true, endpoint: "https://gateway.example/transcribe", model: "gpt-4o-transcribe", source: "api_key", status: 200},
		{name: "expired_snapshot", expired: true, accessOnly: true, status: 503, code: "dictation_unconfigured"},
		{name: "custom_endpoint_rejects_codex", endpoint: "https://gateway.example/transcribe", status: 503, code: "dictation_unconfigured"},
		{name: "refresh_failure_redacted", expired: true, refreshFailure: true, source: "codex", status: 503, code: "dictation_auth_unavailable"},
		{name: "provider_failure_redacted", providerFailure: true, source: "codex", status: 502, code: "dictation_provider_error"},
		{name: "redirect_not_followed", redirect: true, source: "codex", status: 502, code: "dictation_provider_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TOFI_TRANSCRIPTION_API_KEY", "")
			t.Setenv("TOFI_TRANSCRIPTION_URL", "")
			dir := t.TempDir()
			expires := time.Now().Add(time.Hour).UnixMilli()
			if tc.expired {
				expires = time.Now().Add(-time.Hour).UnixMilli()
			}
			refresh := "synthetic-refresh-secret"
			if tc.accessOnly {
				refresh = ""
			}
			stored, _ := json.Marshal(map[string]any{"access_token": "synthetic-access-secret", "refresh_token": refresh, "expires_at": expires, "account_id": "synthetic-account-id", "access_only": tc.accessOnly})
			if err := os.WriteFile(filepath.Join(dir, "codex-credentials.json"), stored, 0600); err != nil {
				t.Fatal(err)
			}
			server, err := NewServer(Config{DataDir: dir, TranscriptionAPIKey: tc.key, TranscriptionURL: tc.endpoint})
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			if tc.model != "" {
				if err := server.store.putDictationModel(tc.model); err != nil {
					t.Fatal(err)
				}
			}
			refreshCalls, audioCalls := 0, 0
			transport := http.DefaultTransport
			http.DefaultTransport = dictationTestTransport(func(r *http.Request) (*http.Response, error) {
				status, body := 200, `{"text":"synthetic transcript"}`
				headers := make(http.Header)
				if r.URL.String() == "https://auth.openai.com/oauth/token" {
					refreshCalls++
					if err := r.ParseForm(); err != nil {
						t.Fatal(err)
					}
					if r.FormValue("grant_type") != "refresh_token" || r.FormValue("refresh_token") != refresh {
						t.Error("incorrect refresh request")
					}
					body = `{"access_token":"synthetic-renewed-secret","refresh_token":"synthetic-renewed-refresh","expires_in":3600}`
					if tc.refreshFailure {
						status = 401
						body = `{"error":"synthetic-refresh-secret"}`
					}
				} else {
					audioCalls++
					expectedEndpoint := defaultTranscriptionURL
					if tc.endpoint != "" {
						expectedEndpoint = tc.endpoint
					}
					if r.URL.String() != expectedEndpoint {
						t.Fatalf("unexpected outbound destination: %s", r.URL.Host)
					}
					expectedKey := "synthetic-access-secret"
					if tc.expired {
						expectedKey = "synthetic-renewed-secret"
					}
					if tc.key != "" {
						expectedKey = tc.key
					}
					if r.Header.Get("Authorization") != "Bearer "+expectedKey {
						t.Error("incorrect bearer credential or account-id separator leaked")
					}
					if err := r.ParseMultipartForm(maxDictationBytes); err != nil {
						t.Fatal(err)
					}
					model := tc.model
					if model == "" {
						model = defaultDictationModel
					}
					if r.FormValue("model") != model {
						t.Error("saved model was not used")
					}
					if tc.providerFailure {
						status = 401
						body = `{"error":"synthetic-access-secret"}`
					}
					if tc.redirect {
						status = 307
						headers.Set("Location", "https://other.example/collect")
					}
				}
				return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			defer func() { http.DefaultTransport = transport }()
			settings := httptest.NewRecorder()
			server.Handler().ServeHTTP(settings, httptest.NewRequest(http.MethodGet, "/api/dictation-settings", nil))
			var state struct {
				Configured bool   `json:"configured"`
				Source     string `json:"auth_source"`
			}
			if err := json.Unmarshal(settings.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			if state.Source != tc.source || state.Configured != (tc.source != "") {
				t.Fatalf("unexpected auth state: %+v", state)
			}
			if refreshCalls != 0 || audioCalls != 0 {
				t.Fatal("reading settings made an outbound request")
			}
			for _, secret := range []string{"synthetic-access-secret", "synthetic-refresh-secret", "synthetic-account-id"} {
				if strings.Contains(settings.Body.String(), secret) {
					t.Fatal("settings leaked a credential")
				}
			}
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			part, _ := form.CreateFormFile("file", "test.wav")
			_, _ = part.Write([]byte("synthetic audio"))
			_ = form.Close()
			req := httptest.NewRequest(http.MethodPost, "/api/dictate", &body)
			req.Header.Set("Content-Type", form.FormDataContentType())
			result := httptest.NewRecorder()
			server.Handler().ServeHTTP(result, req)
			if result.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", result.Code, tc.status, result.Body.String())
			}
			if tc.code != "" && !strings.Contains(result.Body.String(), tc.code) {
				t.Fatalf("missing error code %s", tc.code)
			}
			for _, secret := range []string{"synthetic-access-secret", "synthetic-refresh-secret", "synthetic-renewed-secret", "synthetic-account-id"} {
				if strings.Contains(result.Body.String(), secret) {
					t.Fatal("response leaked a credential")
				}
			}
			wantRefresh := 0
			if tc.expired && !tc.accessOnly && tc.key == "" {
				wantRefresh = 1
			}
			if refreshCalls != wantRefresh {
				t.Fatalf("refresh calls=%d want=%d", refreshCalls, wantRefresh)
			}
			wantAudio := 0
			if tc.status == 200 || tc.providerFailure || tc.redirect {
				wantAudio = 1
			}
			if audioCalls != wantAudio {
				t.Fatalf("audio calls=%d want=%d", audioCalls, wantAudio)
			}
		})
	}
}

type dictationTestTransport func(*http.Request) (*http.Response, error)

func (f dictationTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
