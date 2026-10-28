package app

import (
	"github.com/JackZhao98/tofibot/internal/googleapi"
	"strconv"

	"bytes"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// All fields come from validated session metadata, never callback query values.
// The caller supplies a sanitized, user-facing Message.
type oauthCompletionPage struct {
	State       string
	ServiceName string
	LogoURL     string
	Message     string
	// Lang is one of the seven UI languages; anything else renders English.
	Lang string
	// MessageKey names a catalog sentence and takes the place of Message; MessageCount fills its {n} and
	// MessageDetail follows it (a provider diagnostic, shown in the page language when its code is known).
	MessageKey    string
	MessageCount  int
	DetailCode    string
	MessageDetail string
	APIURL        string
	MCPURL        string
	DocsURL       string
	// Setup says what is actually missing, so the page shows setup help only then:
	// "" (nothing: a connection that works shows the result and the way back),
	// "enable" (the product API or its MCP service is not enabled) or
	// "audience" (the account is not an allowed test user).
	Setup string
	// For Setup "enable": the one console page that enables the API Google named, and that API's name.
	EnableURL   string
	EnableLabel string
	// EnableKind is "api" or "mcp": which of the service's two switches the Enable button turns on.
	EnableKind string
}

// Provider artwork is served from this build (the page's CSP is img-src 'self'). The marks are the vendors' own,
// vendored under ui/public/brand/services with provenance in assets/brand-icons/manifest.json. The page only
// ever renders a logo from this list; anything else becomes the neutral generic pictogram.
const (
	oauthLogoGeneric  = "/oauth/provider-generic.svg"
	oauthLogoGoogle   = "/brand/services/google-g.png"
	oauthLogoGmail    = "/brand/services/google-gmail.png"
	oauthLogoCalendar = "/brand/services/google-calendar.png"
	oauthLogoDrive    = "/brand/services/google-drive.png"
	oauthLogoDocs     = "/brand/services/google-docs.png"
	oauthLogoSheets   = "/brand/services/google-sheets.png"
	oauthLogoSlides   = "/brand/services/google-slides.png"
	oauthLogoNotion   = "/brand/services/notion.svg"
)

var oauthLogoAllowed = map[string]bool{
	oauthLogoGeneric: true, oauthLogoGoogle: true, oauthLogoGmail: true, oauthLogoCalendar: true,
	oauthLogoDrive: true, oauthLogoDocs: true, oauthLogoSheets: true, oauthLogoSlides: true, oauthLogoNotion: true,
}

var oauthAPIPath = regexp.MustCompile(`^/apis/enableflow;apiid=[a-z][a-z0-9-]*\.googleapis\.com$`)
var oauthLibraryPath = regexp.MustCompile(`^/apis/library/([a-z][a-z0-9-]*\.googleapis\.com)$`)

// Setup destinations are public documentation, not redirect targets. Keep this
// independent of callback parameters and disallow query strings and fragments.
func oauthCompletionLink(raw string, docs bool) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return ""
	}
	// The one query TOFI builds itself: a known API's library page for a validated project.
	if !docs && u.Host == "console.cloud.google.com" && u.RawQuery != "" {
		if m := oauthLibraryPath.FindStringSubmatch(u.Path); m != nil && googleapi.KnownAPI(m[1]) {
			if q := u.Query(); len(q) == 1 && googleapi.ValidProject(q.Get("project")) && u.RawQuery == "project="+url.QueryEscape(q.Get("project")) {
				return u.String()
			}
		}
		return ""
	}
	if u.RawQuery != "" {
		return ""
	}
	if !docs && u.Host == "console.cloud.google.com" && oauthAPIPath.MatchString(u.Path) {
		return u.String()
	}
	if docs && ((u.Host == "developers.google.com" && strings.HasPrefix(u.Path, "/workspace/")) ||
		(u.Host == "www.notion.com" && u.Path == "/help/notion-mcp")) {
		return u.String()
	}
	return ""
}

func writeOAuthCompletion(w http.ResponseWriter, status int, page oauthCompletionPage) {
	lang := oauthNormalLanguage(page.Lang)
	if lang == "" {
		lang = "en"
	}
	t := func(key string) string { return oauthText(lang, key) }
	title, heading, next := t("title_failed"), t("heading_failed"), t("next_failed")
	switch page.State {
	case "pending":
		title, heading, next = t("title_pending"), t("heading_pending"), t("next_pending")
	case "success":
		title, heading, next = t("title_success"), t("heading_success"), t("next_success")
	case "denied":
		heading, next = t("heading_denied"), t("next_denied")
	case "failed":
	default:
		page.State = "failed"
	}
	if strings.TrimSpace(page.ServiceName) == "" {
		page.ServiceName = t("service_default")
	}
	if page.MessageKey != "" {
		page.Message = oauthTextWith(lang, page.MessageKey, map[string]string{"n": strconv.Itoa(page.MessageCount)})
		detail := page.MessageDetail
		if localized, ok := oauthPageCatalogs[lang]["diag_"+page.DetailCode]; ok && page.DetailCode != "" {
			detail = localized
		}
		page.Message += detail
	}
	if page.EnableLabel != "" {
		kind := "label_api"
		if page.EnableKind == "mcp" {
			kind = "label_mcp"
		}
		page.EnableLabel += " " + t(kind)
	}
	if !oauthLogoAllowed[page.LogoURL] {
		page.LogoURL = oauthLogoGeneric
	}
	page.EnableURL = oauthCompletionLink(page.EnableURL, false)
	if page.EnableURL == "" {
		page.EnableLabel = ""
	}
	page.APIURL = oauthCompletionLink(page.APIURL, false)
	page.MCPURL = oauthCompletionLink(page.MCPURL, false)
	page.DocsURL = oauthCompletionLink(page.DocsURL, true)
	switch page.Setup {
	case "enable", "audience":
	default:
		page.Setup = ""
	}
	view := struct {
		oauthCompletionPage
		Title, Heading, Next string
		HTMLLang             string
		T                    map[string]string
		EnableButton         string
		HandshakeLabel       string
	}{page, title, heading, next, lang, oauthPageCatalogs[lang], "", oauthTextWith(lang, "handshake", map[string]string{"service": page.ServiceName})}
	if page.EnableLabel != "" {
		view.EnableButton = oauthTextWith(lang, "enable_named", map[string]string{"label": page.EnableLabel})
	}
	var body bytes.Buffer
	if err := oauthCompletionTemplate.Execute(&body, view); err != nil {
		http.Error(w, "Unable to display connection status", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self'; connect-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	w.WriteHeader(status)
	_, _ = w.Write(body.Bytes())
}

var oauthCompletionTemplate = template.Must(template.New("oauth-connection").Parse(`<!doctype html>
<html lang="{{.HTMLLang}}">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="referrer" content="no-referrer">
  <title>{{.Title}}</title>
  <script src="/oauth/connection.js"></script>
  <link rel="stylesheet" href="/oauth/connection.css">
  <link rel="icon" href="/brand/tofi-logo-color.svg" type="image/svg+xml">
</head>
<body class="oauth-connection" data-state="{{.State}}">
  <main class="oauth-completion" aria-labelledby="connection-heading">
    <div class="oauth-handshake" aria-label="{{.HandshakeLabel}}">
      <div class="oauth-identity"><span class="oauth-logo"><img src="/brand/tofi-logo-color.svg" alt="" width="64" height="64"></span><span>Tofi</span></div>
      <div class="oauth-dots" aria-hidden="true"><i></i><i></i><i></i></div>
      <div class="oauth-identity"><span class="oauth-logo oauth-provider"><img src="{{.LogoURL}}" alt="" width="64" height="64"></span><span>{{.ServiceName}}</span></div>
    </div>
    <h1 id="connection-heading">{{.Heading}}</h1>
    {{if .Message}}<p class="oauth-message" role="status">{{.Message}}</p>{{else}}<p class="oauth-next">{{.Next}}</p>{{end}}
    <a class="oauth-return" href="/">{{index .T "return_link"}}</a>
    {{if eq .Setup "enable"}}
    <section class="oauth-setup" aria-labelledby="setup-heading">
      <h2 id="setup-heading">{{index .T "setup_enable_heading"}}</h2>
      <p>{{index .T "setup_enable_body"}}</p>
      <p>{{index .T "setup_preview"}}</p>
      <p>{{index .T "setup_agent_client"}}</p>
      <nav aria-label="{{index .T "nav_label"}}">
        {{if .EnableURL}}<a class="oauth-enable" href="{{.EnableURL}}" target="_blank" rel="noopener noreferrer">{{.EnableButton}}</a>{{else}}
        {{if .APIURL}}<a href="{{.APIURL}}" target="_blank" rel="noopener noreferrer">{{index .T "enable_api"}}</a>{{end}}
        {{if .MCPURL}}<a href="{{.MCPURL}}" target="_blank" rel="noopener noreferrer">{{index .T "enable_mcp"}}</a>{{end}}{{end}}
        {{if .DocsURL}}<a href="{{.DocsURL}}" target="_blank" rel="noopener noreferrer">{{index .T "docs"}}</a>{{end}}
      </nav>
    </section>
    {{else if eq .Setup "audience"}}
    <section class="oauth-setup" aria-labelledby="setup-heading">
      <h2 id="setup-heading">{{index .T "audience_heading"}}</h2>
      <p>{{index .T "audience_body"}}</p>
      <nav aria-label="{{index .T "nav_label"}}">
        <a href="https://console.cloud.google.com/auth/audience" target="_blank" rel="noopener noreferrer">{{index .T "audience_link"}}</a>
        {{if .DocsURL}}<a href="{{.DocsURL}}" target="_blank" rel="noopener noreferrer">{{index .T "docs"}}</a>{{end}}
      </nav>
    </section>
    {{end}}
  </main>
</body>
</html>`))
