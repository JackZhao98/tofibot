// Package manual holds the product manual and the words the screen shows. The reference pages, the release notes and the term tables
// are generated from the settings registry, the locale catalogs and release-notes/ (go generate ./internal/app); the guides and the
// failure pages are written by hand. Everything ships in the binary, so the manual is the manual of the version that runs.
package manual

import (
	"embed"
	"encoding/json"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

//go:embed generated pages
var contentFS embed.FS

// ---- terms: the words the screen shows

var (
	termsOnce sync.Once
	termsByLn map[string]map[string]string
)

func loadTerms() {
	termsByLn = map[string]map[string]string{}
	entries, err := contentFS.ReadDir("generated/terms")
	if err != nil {
		return
	}
	for _, e := range entries {
		raw, err := contentFS.ReadFile("generated/terms/" + e.Name())
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

var uiPattern = regexp.MustCompile(`\{\{ui:([a-z]+:[A-Za-z0-9_.]+)\}\}`)

// Localize puts the person's words for every {{ui:key}} placeholder into text.
func Localize(lang, text string) string {
	return uiPattern.ReplaceAllStringFunc(text, func(m string) string {
		return Term(lang, uiPattern.FindStringSubmatch(m)[1])
	})
}

// UIKeys lists the catalog keys a text names with {{ui:key}}.
func UIKeys(text string) []string {
	var out []string
	for _, m := range uiPattern.FindAllStringSubmatch(text, -1) {
		out = append(out, m[1])
	}
	return out
}

// ---- pages

// Page is one manual page. Title, Summary and Body hold {{ui:key}} placeholders until Localize is applied.
type Page struct {
	ID      string
	Title   string
	Summary string
	Link    string
	Kind    string // guide, reference, failure, whats-new
	Since   string
	SeeAlso []string
	Body    string
}

var (
	pagesOnce sync.Once
	pages     map[string]Page
)

func loadPages() {
	pages = map[string]Page{}
	for _, root := range []string{"pages", "generated"} {
		_ = fs.WalkDir(contentFS, root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
				return nil
			}
			raw, err := contentFS.ReadFile(path)
			if err != nil {
				return nil
			}
			page, ok := parsePage(string(raw))
			if !ok {
				return nil
			}
			if page.ID == "" {
				page.ID = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(path, root+"/"), ""), ".md")
			}
			pages[page.ID] = page
			return nil
		})
	}
}

// parsePage reads the front matter block (key: value lines between ---) and the body.
func parsePage(text string) (Page, bool) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return Page{}, false
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		return Page{}, false
	}
	p := Page{Body: strings.TrimLeft(text[4+end+5:], "\n")}
	for _, line := range strings.Split(text[4:4+end], "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "id":
			p.ID = value
		case "title":
			p.Title = value
		case "summary":
			p.Summary = value
		case "link":
			p.Link = value
		case "kind":
			p.Kind = value
		case "since":
			p.Since = value
		case "see_also":
			for _, id := range strings.Split(value, ",") {
				if id = strings.TrimSpace(id); id != "" {
					p.SeeAlso = append(p.SeeAlso, id)
				}
			}
		}
	}
	return p, p.ID != "" || p.Title != ""
}

func allPages() map[string]Page {
	pagesOnce.Do(loadPages)
	return pages
}

// Get returns the page with this ID, unlocalized.
func Get(id string) (Page, bool) {
	p, ok := allPages()[id]
	return p, ok
}

// IDs lists every page ID, sorted.
func IDs() []string {
	ids := make([]string, 0, len(allPages()))
	for id := range allPages() {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Pages lists every page, sorted by ID.
func Pages() []Page {
	out := make([]Page, 0, len(allPages()))
	for _, id := range IDs() {
		out = append(out, allPages()[id])
	}
	return out
}

// Index lists the pages a person asks about: guides and reference pages. Failure pages are reached with Explain and release notes with
// ReleaseNotes, so the list stays short.
func Index() []Page {
	var out []Page
	for _, p := range Pages() {
		if p.Kind == "guide" || p.Kind == "reference" {
			out = append(out, p)
		}
	}
	return out
}

// Explain returns the failure page for a stable code.
func Explain(code string) (Page, bool) {
	p, ok := Get("failures/" + strings.TrimSpace(code))
	return p, ok
}

// NearestFailure suggests the failure pages whose code shares the longest prefix with an unknown code.
func NearestFailure(code string) []string {
	code = strings.TrimSpace(code)
	best, bestLen := []string(nil), 0
	for id := range allPages() {
		name, ok := strings.CutPrefix(id, "failures/")
		if !ok {
			continue
		}
		n := 0
		for n < len(name) && n < len(code) && name[n] == code[n] {
			n++
		}
		if n > bestLen {
			best, bestLen = []string{name}, n
		} else if n == bestLen && n > 0 {
			best = append(best, name)
		}
	}
	sort.Strings(best)
	if len(best) > 5 {
		best = best[:5]
	}
	return best
}

// Hit is one search result.
type Hit struct {
	ID      string
	Title   string
	Snippet string
}

// Search matches a text against the localized title and body of every page, case-insensitively, and returns the best limit hits. A title
// match ranks above a body match. There are no embeddings.
func Search(lang, query string, limit int) []Hit {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" || limit <= 0 {
		return nil
	}
	type scored struct {
		hit   Hit
		score int
	}
	var found []scored
	for _, p := range Pages() {
		title, body := Localize(lang, p.Title), Localize(lang, p.Body)
		lowTitle, lowBody := strings.ToLower(title), strings.ToLower(body)
		score := 0
		if strings.Contains(lowTitle, query) {
			score += 10
		}
		if i := strings.Index(lowBody, query); i >= 0 {
			score++
			found = append(found, scored{Hit{p.ID, title, snippet(body, i, len(query))}, score})
			continue
		}
		if score > 0 {
			found = append(found, scored{Hit{p.ID, title, Localize(lang, p.Summary)}, score})
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].score > found[j].score })
	if len(found) > limit {
		found = found[:limit]
	}
	out := make([]Hit, len(found))
	for i, f := range found {
		out[i] = f.hit
	}
	return out
}

func snippet(body string, at, n int) string {
	runes := []rune(body)
	// at is a byte offset into the lower-cased text, which can differ from the original in length; clamp to the rune count.
	start := len([]rune(body[:min(at, len(body))])) - 60
	if start < 0 {
		start = 0
	}
	end := start + 160 + n
	if end > len(runes) {
		end = len(runes)
	}
	return strings.Join(strings.Fields(string(runes[start:end])), " ")
}

// ---- release notes

var versionPattern = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)(?:-rc\.(\d+))?$`)

// versionKey orders release tags: a final release sorts after its release candidates.
func versionKey(v string) ([5]int, bool) {
	m := versionPattern.FindStringSubmatch(v)
	if m == nil {
		return [5]int{}, false
	}
	var key [5]int
	for i := 0; i < 3; i++ {
		key[i], _ = strconv.Atoi(m[i+1])
	}
	if m[4] == "" {
		key[3] = 1
	} else {
		key[4], _ = strconv.Atoi(m[4])
	}
	return key, true
}

// Newer says whether version a is newer than version b. Unknown versions are never newer.
func Newer(a, b string) bool {
	ka, ok1 := versionKey(a)
	kb, ok2 := versionKey(b)
	if !ok1 || !ok2 {
		return false
	}
	for i := range ka {
		if ka[i] != kb[i] {
			return ka[i] > kb[i]
		}
	}
	return false
}

// ReleaseNotes lists the release notes pages, newest first.
func ReleaseNotes() []Page {
	var out []Page
	for _, p := range Pages() {
		if p.Kind == "whats-new" {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return Newer(out[i].Since, out[j].Since) })
	return out
}

// WhatsNew returns the notes of the installed version first, then those between since (exclusive) and the installed version, newest
// first. A build with no release version ("dev") gets the newest notes.
func WhatsNew(installed, since string) []Page {
	notes := ReleaseNotes()
	var out []Page
	for _, p := range notes {
		isInstalled := p.Since == installed
		newerThanSince := since == "" || Newer(p.Since, since)
		notNewerThanInstalled := installed == "" || !versionPatternOK(installed) || !Newer(p.Since, installed)
		if isInstalled || (newerThanSince && notNewerThanInstalled) {
			out = append(out, p)
		}
	}
	if len(out) == 0 && len(notes) > 0 {
		out = notes[:1]
	}
	if !versionPatternOK(installed) && len(out) > 3 {
		out = out[:3]
	}
	return out
}

func versionPatternOK(v string) bool { return versionPattern.MatchString(v) }
