package app

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/JackZhao98/tofibot/internal/manual"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// The `manual` tool: the product manual on demand. The prompt carries one line that points here; a page loads only on a call (the three
// levels of progressive disclosure: that line, index, read). Pages are English; the screen's words in them are put in the person's
// language when a page is read, so the Bot says the names the person sees. The reference pages come from the settings registry, so the
// manual and the tool cannot disagree about a key.

const (
	maxManualPageBytes  = 12 << 10
	maxManualNotesBytes = 6 << 10
	manualSearchLimit   = 8
)

type manualToolInput struct {
	Action string `json:"action"`
	Page   string `json:"page"`
	Query  string `json:"query"`
	Code   string `json:"code"`
	Since  string `json:"since"`
}

func manualToolDescription() string {
	return "Read the product manual when the person asks how TOFI works, where a setting is, why something failed, what changed in this version, or how to set up. " +
		"Call index first, then read one page by its id. Call search with a query to find a page by words. Call explain with a code to read the meaning and the steps for a failure code. Call whats_new for the release notes of this version. " +
		"Write each link in the result as a Markdown link, with link_label as the text and the path unchanged. Answer from the page. When the page does not cover the question, say so."
}

func (s *Server) manualTool(c Conversation, r Run) Tool {
	return Tool{Name: "manual", Description: manualToolDescription(), Parameters: objectSchema(map[string]any{
		"action": map[string]any{"type": "string", "enum": []string{"index", "read", "search", "whats_new", "explain"}},
		"page":   map[string]any{"type": "string", "description": "read: the page id from index."},
		"query":  map[string]any{"type": "string", "description": "search: words to look for."},
		"code":   map[string]any{"type": "string", "description": "explain: a failure code from an error or an outcome."},
		"since":  map[string]any{"type": "string", "description": "whats_new: the version after which changes are wanted; the previous release when omitted."},
	}, []string{"action"}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var in manualToolInput
		if err := decodeWorkspaceTool(raw, &in); err != nil {
			return "", tooloutcome.New(tooloutcome.NeedInformation, "manual_invalid_request", "not_executed", "The manual request is not valid: use only the documented fields.", "repair_arguments").Err()
		}
		mr := manualRun{s: s, c: c, r: r, lang: s.settingsLanguage()}
		switch in.Action {
		case "index":
			return mr.index()
		case "read":
			return mr.read(ctx, in.Page)
		case "search":
			return mr.search(in.Query)
		case "whats_new":
			return mr.whatsNew(in.Since)
		case "explain":
			return mr.explain(in.Code)
		}
		return "", tooloutcome.New(tooloutcome.NeedInformation, "manual_invalid_request", "not_executed", "action must be index, read, search, whats_new or explain.", "repair_arguments").Err()
	}}
}

type manualRun struct {
	s    *Server
	c    Conversation
	r    Run
	lang string
}

var groupLinkPattern = regexp.MustCompile(`\[([^\]]*)\]\(/g/\{group\}[^)]*\)`)

// fill puts the Bot and the group of this run into the links of a text, and the person's words into the placeholders. A text that names
// a group page when no group exists loses the link and keeps its words.
func (m manualRun) fill(text string) string {
	text = manual.Localize(m.lang, text)
	text = strings.ReplaceAll(text, "{bot}", m.r.BotID)
	if group := m.groupID(); group != "" {
		return strings.ReplaceAll(text, "{group}", group)
	}
	return strings.ReplaceAll(groupLinkPattern.ReplaceAllString(text, "$1"), "{group}", "<group id>")
}

func (m manualRun) groupID() string {
	if m.c.Kind == "group" && m.c.UserVisible {
		return m.c.ID
	}
	if convs, err := m.s.store.ListConversations(); err == nil {
		for _, conv := range convs {
			if conv.Kind == "group" && conv.UserVisible && !conv.Archived {
				return conv.ID
			}
		}
	}
	return ""
}

func (m manualRun) link(p manual.Page) string {
	if p.Link == "" || (strings.Contains(p.Link, "{group}") && m.groupID() == "") {
		return ""
	}
	return strings.ReplaceAll(strings.ReplaceAll(p.Link, "{bot}", m.r.BotID), "{group}", m.groupID())
}

func (m manualRun) index() (string, error) {
	type row struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Summary   string `json:"summary"`
		Link      string `json:"link,omitempty"`
		LinkLabel string `json:"link_label,omitempty"`
	}
	var rows []row
	for _, p := range manual.Index() {
		title := manual.Localize(m.lang, p.Title)
		item := row{ID: p.ID, Title: title, Summary: manual.Localize(m.lang, p.Summary)}
		if link := m.link(p); link != "" {
			item.Link, item.LinkLabel = link, title
		}
		rows = append(rows, item)
	}
	return settingsOutput(map[string]any{"pages": rows, "failure_codes": "Call explain with a failure code to read its page.", "release_notes": "Call whats_new for the release notes."})
}

func (m manualRun) read(ctx context.Context, id string) (string, error) {
	id = strings.TrimSpace(id)
	p, ok := manual.Get(id)
	if !ok {
		return "", tooloutcome.New(tooloutcome.NeedInformation, "manual_page_not_found", "not_executed", "No manual page has this id. Call index to see the ids, or search to find a page by words.", "repair_arguments").Err()
	}
	body := m.fill(p.Body)
	if len(body) > maxManualPageBytes {
		body = body[:maxManualPageBytes]
	}
	title := manual.Localize(m.lang, p.Title)
	out := map[string]any{"id": p.ID, "title": title, "link": m.link(p), "link_label": title, "body": body}
	if see := m.seeAlso(p); len(see) > 0 {
		out["see_also"] = see
	}
	if p.ID == "setup" {
		out["state"] = m.setupState(ctx)
	}
	return settingsOutput(out)
}

func (m manualRun) seeAlso(p manual.Page) []map[string]string {
	var out []map[string]string
	for _, id := range p.SeeAlso {
		if other, ok := manual.Get(id); ok {
			out = append(out, map[string]string{"id": id, "title": manual.Localize(m.lang, other.Title)})
		}
	}
	return out
}

// setupState is the header of the setup page: where this workspace is, so the Bot starts at the right step. It never claims a computer
// that does not exist.
func (m manualRun) setupState(ctx context.Context) map[string]any {
	state := map[string]any{
		"model_connected": m.s.anyProviderConfigured(),
		"computer":        m.s.computerCapabilities().Backend,
	}
	if st, err := m.s.store.onboardingState(); err == nil {
		state["onboarding_step"], state["onboarding_completed"], state["onboarding_skipped"] = st.Step, st.Completed, st.Skipped
	}
	next := "none: setup is done"
	switch {
	case !m.s.anyProviderConfigured():
		next = "1: connect a model"
	case m.s.extensions != nil:
		if servers, err := m.s.extensions.ListMCP(); err == nil && len(servers) == 0 {
			next = "2: connect a service (optional)"
		}
	}
	state["next_step"] = next
	return state
}

func (m manualRun) search(query string) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "", tooloutcome.New(tooloutcome.NeedInformation, "manual_invalid_request", "not_executed", "query is required for search.", "repair_arguments").Err()
	}
	type hit struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Snippet string `json:"snippet"`
	}
	var hits []hit
	for _, h := range manual.Search(m.lang, query, manualSearchLimit) {
		hits = append(hits, hit{ID: h.ID, Title: h.Title, Snippet: m.fill(h.Snippet)})
	}
	return settingsOutput(map[string]any{"hits": hits})
}

func (m manualRun) whatsNew(since string) (string, error) {
	pages := manual.WhatsNew(Version, strings.TrimSpace(since))
	type note struct {
		ID      string `json:"id"`
		Version string `json:"version"`
		Title   string `json:"title"`
		Body    string `json:"body"`
	}
	var notes []note
	for _, p := range pages {
		body := m.fill(p.Body)
		if len(body) > maxManualNotesBytes {
			body = body[:maxManualNotesBytes]
		}
		notes = append(notes, note{ID: p.ID, Version: p.Since, Title: manual.Localize(m.lang, p.Title), Body: body})
	}
	out := map[string]any{"installed": Version, "notes": notes, "link": "/settings/advanced#version", "link_label": manual.Term(m.lang, "settings:shell.title") + " › " + manual.Term(m.lang, "settings:shell.page.advanced.name")}
	if len(notes) == 0 {
		out["message"] = "This build has no release notes."
	}
	return settingsOutput(out)
}

func (m manualRun) explain(code string) (string, error) {
	code = strings.TrimSpace(code)
	p, ok := manual.Explain(code)
	if !ok {
		out := tooloutcome.New(tooloutcome.NeedInformation, "manual_page_not_found", "not_executed", "No failure page has this code. Search the manual by words, or read the nearest pages: "+strings.Join(manual.NearestFailure(code), ", ")+".", "repair_arguments")
		return "", out.Err()
	}
	title := manual.Localize(m.lang, p.Title)
	out := map[string]any{"code": code, "id": p.ID, "title": title, "link": m.link(p), "link_label": title, "body": m.fill(p.Body)}
	return settingsOutput(out)
}
