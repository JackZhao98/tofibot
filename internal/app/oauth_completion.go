package app

import (
	"github.com/JackZhao98/tofibot/internal/googleapi"

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
	APIURL      string
	MCPURL      string
	DocsURL     string
	// Setup says what is actually missing, so the page shows setup help only then:
	// "" (nothing: a connection that works shows the result and the way back),
	// "enable" (the product API or its MCP service is not enabled) or
	// "audience" (the account is not an allowed test user).
	Setup string
	// For Setup "enable": the one console page that enables the API Google named, and that API's name.
	EnableURL   string
	EnableLabel string
}

// Provider artwork is served from this build (the page's CSP is img-src 'self'). The marks are the vendors' own,
// vendored under ui/public/brand/services with provenance in docs/integrations/icons/manifest.json. The page only
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
	title, heading, next := "Connection failed", "授权未完成", "返回 Tofi 检查连接配置，然后重试授权。"
	switch page.State {
	case "pending":
		title, heading, next = "Connecting", "正在连接", "正在打开服务的授权页面，请稍候。"
	case "success":
		title, heading, next = "Connection complete", "授权已完成", "返回 Tofi 即可使用。"
	case "denied":
		heading, next = "授权被拒绝", "如需继续连接，请返回 Tofi 检查账号权限并重试。"
	case "failed":
	default:
		page.State = "failed"
	}
	if strings.TrimSpace(page.ServiceName) == "" {
		page.ServiceName = "外部服务"
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
	}{page, title, heading, next}
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
<html lang="zh-CN">
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
    <div class="oauth-handshake" aria-label="Tofi 连接 {{.ServiceName}}">
      <div class="oauth-identity"><span class="oauth-logo"><img src="/brand/tofi-logo-color.svg" alt="" width="64" height="64"></span><span>Tofi</span></div>
      <div class="oauth-dots" aria-hidden="true"><i></i><i></i><i></i></div>
      <div class="oauth-identity"><span class="oauth-logo oauth-provider"><img src="{{.LogoURL}}" alt="" width="64" height="64"></span><span>{{.ServiceName}}</span></div>
    </div>
    <h1 id="connection-heading">{{.Heading}}</h1>
    {{if .Message}}<p class="oauth-message" role="status">{{.Message}}</p>{{else}}<p class="oauth-next">{{.Next}}</p>{{end}}
    <a class="oauth-return" href="/">返回 Tofi</a>
    {{if eq .Setup "enable"}}
    <section class="oauth-setup" aria-labelledby="setup-heading">
      <h2 id="setup-heading">启用服务</h2>
      <p>在 OAuth Client 所属的同一个 Google Cloud 项目中启用下面的服务，然后回到 Tofi 刷新工具。</p>
      <nav aria-label="Google 配置">
        {{if .EnableURL}}<a class="oauth-enable" href="{{.EnableURL}}" target="_blank" rel="noopener noreferrer">启用 {{.EnableLabel}}</a>{{else}}
        {{if .APIURL}}<a href="{{.APIURL}}" target="_blank" rel="noopener noreferrer">启用产品 API</a>{{end}}
        {{if .MCPURL}}<a href="{{.MCPURL}}" target="_blank" rel="noopener noreferrer">启用 MCP 服务</a>{{end}}{{end}}
        {{if .DocsURL}}<a href="{{.DocsURL}}" target="_blank" rel="noopener noreferrer">官方接入文档</a>{{end}}
      </nav>
    </section>
    {{else if eq .Setup "audience"}}
    <section class="oauth-setup" aria-labelledby="setup-heading">
      <h2 id="setup-heading">添加测试用户</h2>
      <p>应用处于测试模式时，只有测试用户能授权。请在 Audience 中添加当前账号，然后重试。</p>
      <nav aria-label="Google 配置">
        <a href="https://console.cloud.google.com/auth/audience" target="_blank" rel="noopener noreferrer">配置测试用户</a>
        {{if .DocsURL}}<a href="{{.DocsURL}}" target="_blank" rel="noopener noreferrer">官方接入文档</a>{{end}}
      </nav>
    </section>
    {{end}}
  </main>
</body>
</html>`))
