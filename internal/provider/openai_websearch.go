package provider

import (
	"encoding/json"
	"log"
	"net/url"
	"strings"
	"sync"
)

// openaiWebSearchType is OpenAI's hosted search tool in the Responses API.
// The same declaration works on the API-key path and on the ChatGPT (Codex)
// sign-in backend.
const openaiWebSearchType = "web_search"

// openaiWebSearchRefused remembers endpoint+model pairs whose backend rejected
// the hosted search tool, so one refusal is not paid again on every turn.
var openaiWebSearchRefused sync.Map

func openaiWebSearchKey(baseURL, model string) string { return baseURL + "|" + model }

func (o *openaiResponses) webSearchRefused(model string) bool {
	_, refused := openaiWebSearchRefused.Load(openaiWebSearchKey(o.baseURL, model))
	return refused
}

// dropRefusedWebSearch remembers the refusal and returns the request without
// the hosted tool.
func (o *openaiResponses) dropRefusedWebSearch(req *ChatRequest, err error) *ChatRequest {
	openaiWebSearchRefused.Store(openaiWebSearchKey(o.baseURL, req.Model), true)
	log.Printf("[openai] hosted web search refused for %s (%s); continuing without it until restart", req.Model, strings.TrimSpace(err.Error()))
	plain := *req
	plain.WebSearch = nil
	return &plain
}

// isOpenAIWebSearchRejection matches a backend saying the hosted search tool
// is unavailable for this credential or model. The Responses API and the
// Codex backend word it differently ("Unsupported tool type", "Tool
// 'web_search' is not supported", "Invalid value: 'web_search'. Supported
// values are ..."), so beyond the shared refusal phrases a 4xx that names the
// tool in an invalid/unknown/unrecognized sense also counts. Errors about the
// shape of the history are never a refusal.
func isOpenAIWebSearchRejection(err error) bool {
	if apiErr, ok := AsAPIError(err); ok && strings.Contains(strings.ToLower(apiErr.Body), "following item") {
		return false // a replayed item lost its pair: not a refusal of the tool
	}
	if isServerToolRefusal(err, "web_search", "web search") {
		return true
	}
	apiErr, ok := AsAPIError(err)
	if !ok || (apiErr.StatusCode != 400 && apiErr.StatusCode != 403 && apiErr.StatusCode != 404 && apiErr.StatusCode != 422) {
		return false
	}
	body := strings.ToLower(apiErr.Body)
	if !strings.Contains(body, "web_search") || isMalformedRequestBody(body) {
		return false
	}
	for _, phrase := range []string{"invalid value", "unknown tool", "unknown parameter", "unrecognized", "supported values", "invalid tool", "tools["} {
		if strings.Contains(body, phrase) {
			return true
		}
	}
	return false
}

// responsesWebSearches collects the hosted searches and url_citation sources
// of one Responses API response, from either the streamed or the complete form.
type responsesWebSearches struct {
	searches []WebSearch
	seen     map[string]bool // search item ids
	cited    []WebSearchSource
	citedURL map[string]bool
}

// item records a web_search_call output item (added, done, or in a complete
// response). The same item may arrive more than once; the last copy wins.
func (c *responsesWebSearches) item(raw json.RawMessage) {
	var it struct {
		Type   string `json:"type"`
		ID     string `json:"id"`
		Status string `json:"status"`
		Action struct {
			Type    string   `json:"type"`
			Query   string   `json:"query"`
			Queries []string `json:"queries"`
			URL     string   `json:"url"`
			Sources []struct {
				URL   string `json:"url"`
				Title string `json:"title"`
			} `json:"sources"`
		} `json:"action"`
	}
	if json.Unmarshal(raw, &it) != nil || it.Type != "web_search_call" {
		return
	}
	ws := WebSearch{ID: it.ID, Query: strings.TrimSpace(it.Action.Query)}
	if ws.Query == "" && len(it.Action.Queries) > 0 {
		ws.Query = strings.Join(it.Action.Queries, "; ")
	}
	if ws.Query == "" && it.Action.URL != "" {
		ws.Query = it.Action.URL // open_page / find_in_page act on one address
	}
	for _, s := range it.Action.Sources {
		if u := cleanCitationURL(s.URL); u != "" {
			ws.Sources = append(ws.Sources, WebSearchSource{Title: s.Title, URL: u})
		}
	}
	if it.Status == "failed" {
		ws.Error = "failed"
	}
	if c.seen == nil {
		c.seen = map[string]bool{}
	}
	if it.ID != "" && c.seen[it.ID] {
		for i := range c.searches {
			if c.searches[i].ID == it.ID {
				if ws.Query == "" {
					ws.Query = c.searches[i].Query
				}
				c.searches[i] = ws
			}
		}
		return
	}
	if it.ID != "" {
		c.seen[it.ID] = true
	}
	c.searches = append(c.searches, ws)
}

// annotation records one url_citation from an output_text part.
func (c *responsesWebSearches) annotation(raw json.RawMessage) {
	var a struct {
		Type  string `json:"type"`
		URL   string `json:"url"`
		Title string `json:"title"`
	}
	if json.Unmarshal(raw, &a) != nil || a.Type != "url_citation" {
		return
	}
	u := cleanCitationURL(a.URL)
	if u == "" {
		return
	}
	if c.citedURL == nil {
		c.citedURL = map[string]bool{}
	}
	if c.citedURL[u] {
		return
	}
	c.citedURL[u] = true
	c.cited = append(c.cited, WebSearchSource{Title: a.Title, URL: u})
}

// messageItem reads the citations of a completed message output item.
func (c *responsesWebSearches) messageItem(raw json.RawMessage) {
	var m struct {
		Type    string `json:"type"`
		Content []struct {
			Annotations []json.RawMessage `json:"annotations"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &m) != nil || m.Type != "message" {
		return
	}
	for _, part := range m.Content {
		for _, a := range part.Annotations {
			c.annotation(a)
		}
	}
}

// finish writes the searches and their count onto the response. Citations the
// search actions did not already list are attached to the last search, so the
// sources the model actually relied on always appear in the evidence.
func (c *responsesWebSearches) finish(result *ChatResponse) {
	if len(c.searches) == 0 {
		return
	}
	have := map[string]bool{}
	for _, ws := range c.searches {
		for _, s := range ws.Sources {
			have[s.URL] = true
		}
	}
	last := &c.searches[len(c.searches)-1]
	for _, s := range c.cited {
		if !have[s.URL] {
			have[s.URL] = true
			last.Sources = append(last.Sources, s)
		}
	}
	result.WebSearches = c.searches
	result.WebSearchRequests = int64(len(c.searches))
}

// cleanCitationURL drops the utm_source=openai tag OpenAI appends to cited
// addresses, and anything that is not an http(s) address.
func cleanCitationURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	q := u.Query()
	if q.Get("utm_source") == "openai" {
		q.Del("utm_source")
		u.RawQuery = q.Encode()
	}
	return u.String()
}
