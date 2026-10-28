package extensions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The agentic-client requirement is guidance in every UI language: the setup
// step and the hint must exist and name the console checkbox.
func TestGoogleAgenticGuidanceExistsInEveryLocale(t *testing.T) {
	for _, locale := range []string{"en", "zh-CN", "zh-TW", "ja", "ko", "de", "fr"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "ui", "src", "locales", locale, "extensions.json"))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Catalog struct {
				GoogleSetup map[string]string `json:"google_setup"`
			} `json:"catalog"`
			Google map[string]any `json:"google"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		for name, text := range map[string]string{"catalog.google_setup.agentic_client": doc.Catalog.GoogleSetup["agentic_client"], "google.agentic_hint": str(doc.Google["agentic_hint"])} {
			if !strings.Contains(text, "AI-powered agent") || !strings.Contains(text, "2026-11-12") {
				t.Errorf("%s %s: %q", locale, name, text)
			}
		}
		for name, text := range map[string]string{"catalog.google_setup.preview_access": doc.Catalog.GoogleSetup["preview_access"], "google.preview_hint": str(doc.Google["preview_hint"])} {
			if !strings.Contains(text, "Developer Preview Program") {
				t.Errorf("%s %s: %q", locale, name, text)
			}
		}
	}
}

// str reads a string leaf from a locale section that also holds nested groups.
func str(v any) string {
	s, _ := v.(string)
	return s
}
