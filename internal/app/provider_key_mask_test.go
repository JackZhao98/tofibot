package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The key shows its first and last four characters and never the whole key;
// /api/config tells the composer whether dictation has a credential.
func TestProviderKeyMaskAndDictationConfigured(t *testing.T) {
	openai := newFakeOpenAI(t, "ok")
	s := providerServer(t, openai, newFakeAnthropic(t, "ok"))
	config := func() map[string]any {
		rec := providerRequest(t, s, http.MethodGet, "/api/config", "")
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if config()["dictation_configured"] != false {
		t.Fatal("no OpenAI method is connected yet")
	}
	status := putProviderKeyOK(t, s, "openai", syntheticOpenAIKey)
	if len(syntheticOpenAIKey) >= 24 && status.KeyPrefix != syntheticOpenAIKey[:4] || len(syntheticOpenAIKey) < 24 && status.KeyPrefix != "" {
		t.Fatalf("key_prefix=%q", status.KeyPrefix)
	}
	rec := providerRequest(t, s, http.MethodGet, "/api/providers", "")
	if strings.Contains(rec.Body.String(), syntheticOpenAIKey) || strings.Contains(rec.Body.String(), syntheticAnthropicKey) {
		t.Fatal("a provider response carried a whole key")
	}
	if config()["dictation_configured"] != true {
		t.Fatal("an OpenAI key makes dictation available")
	}
}
