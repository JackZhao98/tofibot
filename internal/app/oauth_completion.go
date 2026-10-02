package app

import (
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
}

var oauthAPIPath = regexp.MustCompile(`^/apis/enableflow;apiid=[a-z][a-z0-9-]*\.googleapis\.com$`)

// Setup destinations are public documentation, not redirect targets. Keep this
// independent of callback parameters and disallow query strings and fragments.
func oauthCompletionLink(raw string, docs bool) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
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
		title, heading, next = "Connection complete", "授权已完成", "返回 Tofi 读取工具并检查连接状态。授权完成不代表服务 API 已启用。"
	case "denied":
		heading, next = "授权被拒绝", "如需继续连接，请返回 Tofi 检查账号权限并重试。"
	case "failed":
	default:
		page.State = "failed"
	}
	if strings.TrimSpace(page.ServiceName) == "" {
		page.ServiceName = "外部服务"
	}
	switch page.LogoURL {
	case "/oauth/provider-google.svg", "/oauth/provider-notion.svg", "/oauth/provider-generic.svg":
	default:
		page.LogoURL = "/oauth/provider-generic.svg"
	}
	page.APIURL = oauthCompletionLink(page.APIURL, false)
	page.MCPURL = oauthCompletionLink(page.MCPURL, false)
	page.DocsURL = oauthCompletionLink(page.DocsURL, true)
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
    {{if or .APIURL .MCPURL}}
    <section class="oauth-setup" aria-labelledby="setup-heading">
      <h2 id="setup-heading">Google 连接设置</h2>
      <p>打开 Google Cloud 后，选择与 OAuth Client 相同的项目。若应用处于测试模式，请在 Audience 中添加当前账号为测试用户。</p>
      <nav aria-label="Google 配置">
        {{if .APIURL}}<a href="{{.APIURL}}" target="_blank" rel="noopener noreferrer">启用产品 API</a>{{end}}
        {{if .MCPURL}}<a href="{{.MCPURL}}" target="_blank" rel="noopener noreferrer">启用 MCP 服务</a>{{end}}
        <a href="https://console.cloud.google.com/auth/audience" target="_blank" rel="noopener noreferrer">配置测试用户</a>
      </nav>
    </section>
    {{end}}
    {{if .DocsURL}}<a class="oauth-docs" href="{{.DocsURL}}" target="_blank" rel="noopener noreferrer">查看官方接入文档</a>{{end}}
  </main>
</body>
</html>`))
