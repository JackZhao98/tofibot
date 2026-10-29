// Package manual holds the product manual and the words the screen shows. The reference pages and the term tables are generated from the
// settings registry and the locale catalogs (go generate ./internal/app); the guides are written by hand.
package manual

import (
	"embed"
	"encoding/json"
	"strings"
	"sync"
)

//go:embed generated
var generatedFS embed.FS

var (
	termsOnce sync.Once
	termsByLn map[string]map[string]string
)

func loadTerms() {
	termsByLn = map[string]map[string]string{}
	entries, err := generatedFS.ReadDir("generated/terms")
	if err != nil {
		return
	}
	for _, e := range entries {
		raw, err := generatedFS.ReadFile("generated/terms/" + e.Name())
		if err != nil {
			continue
		}
		var terms map[string]string
		if json.Unmarshal(raw, &terms) == nil {
			termsByLn[strings.TrimSuffix(e.Name(), ".json")] = terms
		}
	}
}

// Terms returns the screen's words for a UI language ("namespace:key" -> text). An unknown or empty language gives English.
func Terms(lang string) map[string]string {
	termsOnce.Do(loadTerms)
	if t, ok := termsByLn[lang]; ok {
		return t
	}
	return termsByLn["en"]
}

// Term is one screen word in a language, falling back to English, then to the key itself.
func Term(lang, key string) string {
	if v, ok := Terms(lang)[key]; ok {
		return v
	}
	if v, ok := Terms("en")[key]; ok {
		return v
	}
	return key
}
