package app

import (
	"embed"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// The completion page is served to a browser that has just come back from a provider, with no UI around it, so it
// carries its own copy for the seven UI languages. The catalogs are JSON (oauth_pages/<language>.json) so that the
// Go sources hold no display text; English is the fallback for any language or key that is missing.
//
//go:embed oauth_pages/*.json
var oauthPageFS embed.FS

var oauthPageCatalogs = func() map[string]map[string]string {
	catalogs := map[string]map[string]string{}
	for language := range uiLanguages {
		data, err := oauthPageFS.ReadFile("oauth_pages/" + language + ".json")
		if err != nil {
			panic("oauth page catalog missing: " + language)
		}
		catalog := map[string]string{}
		if err := json.Unmarshal(data, &catalog); err != nil {
			panic("oauth page catalog invalid: " + language)
		}
		catalogs[language] = catalog
	}
	return catalogs
}()

// oauthText looks a key up in a language, falling back to English, then to the key itself.
func oauthText(language, key string) string {
	if text, ok := oauthPageCatalogs[language][key]; ok {
		return text
	}
	if text, ok := oauthPageCatalogs["en"][key]; ok {
		return text
	}
	return key
}

// oauthTextWith fills {name} placeholders.
func oauthTextWith(language, key string, values map[string]string) string {
	text := oauthText(language, key)
	for name, value := range values {
		text = strings.ReplaceAll(text, "{"+name+"}", value)
	}
	return text
}

// oauthNormalLanguage maps a language tag onto one of the seven UI languages, or "" when none fits.
func oauthNormalLanguage(tag string) string {
	tag = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(tag), "_", "-"))
	switch {
	case tag == "":
		return ""
	case tag == "zh-tw" || strings.HasPrefix(tag, "zh-hant") || tag == "zh-hk" || tag == "zh-mo":
		return "zh-TW"
	case tag == "zh" || strings.HasPrefix(tag, "zh-"):
		return "zh-CN"
	}
	base, _, _ := strings.Cut(tag, "-")
	switch base {
	case "en", "ja", "ko", "de", "fr":
		return base
	}
	return ""
}

// oauthAcceptLanguage picks the first supported language of an Accept-Language header by weight, "" if none.
func oauthAcceptLanguage(header string) string {
	type choice struct {
		language string
		weight   float64
		order    int
	}
	var choices []choice
	for i, part := range strings.Split(header, ",") {
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		weight := 1.0
		if q, ok := strings.CutPrefix(strings.ReplaceAll(strings.TrimSpace(params), " ", ""), "q="); ok {
			if parsed, err := strconv.ParseFloat(q, 64); err == nil {
				weight = parsed
			}
		}
		if language := oauthNormalLanguage(tag); language != "" && weight > 0 {
			choices = append(choices, choice{language, weight, i})
		}
	}
	sort.SliceStable(choices, func(i, j int) bool { return choices[i].weight > choices[j].weight })
	if len(choices) == 0 {
		return ""
	}
	return choices[0].language
}

// oauthPageLanguage is the page's language: the account's saved UI language when it has one, else what the browser
// asks for, else English.
func (s *Server) oauthPageLanguage(r *http.Request) string {
	if prefs, err := s.store.userPreferences(); err == nil && uiLanguages[prefs.Language] {
		return prefs.Language
	}
	if language := oauthAcceptLanguage(r.Header.Get("Accept-Language")); language != "" {
		return language
	}
	return "en"
}
