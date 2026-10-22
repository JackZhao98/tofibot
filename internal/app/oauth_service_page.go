package app

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/googleapi"
)

// Provider artwork and configuration links come from known endpoints, never
// from callback parameters or provider-supplied error URLs.
func oauthServicePage(identity extensions.OAuthServiceIdentity) oauthCompletionPage {
	page := oauthCompletionPage{ServiceName: "外部服务", LogoURL: oauthLogoGeneric}
	if identity.Name == "" {
		return page
	}
	page.ServiceName = identity.Name
	if identity.Name == "Google (gogcli)" && identity.URL == "" {
		// The local gog flow has no MCP endpoint; it authorizes a Google account, so it wears the Google mark.
		page.LogoURL = oauthLogoGoogle
		return page
	}
	u, err := url.Parse(identity.URL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" {
		return page
	}
	if u.Hostname() == "mcp.notion.com" && strings.TrimSuffix(u.Path, "/") == "/mcp" {
		page.ServiceName, page.LogoURL, page.DocsURL = "Notion", oauthLogoNotion, "https://www.notion.com/help/notion-mcp"
		return page
	}
	if strings.TrimSuffix(u.Path, "/") != "/mcp/v1" {
		return page
	}
	products := map[string]struct{ name, api, product, logo string }{
		"gmailmcp.googleapis.com":    {"Google Gmail", "gmail.googleapis.com", "gmail/api", oauthLogoGmail},
		"drivemcp.googleapis.com":    {"Google Drive", "drive.googleapis.com", "drive/api", oauthLogoDrive},
		"docsmcp.googleapis.com":     {"Google Docs", "docs.googleapis.com", "docs/api", oauthLogoDocs},
		"sheetsmcp.googleapis.com":   {"Google Sheets", "sheets.googleapis.com", "sheets/api", oauthLogoSheets},
		"slidesmcp.googleapis.com":   {"Google Slides", "slides.googleapis.com", "slides/api", oauthLogoSlides},
		"calendarmcp.googleapis.com": {"Google Calendar", "calendar-json.googleapis.com", "calendar/api", oauthLogoCalendar},
	}
	if product, ok := products[u.Hostname()]; ok {
		page.ServiceName, page.LogoURL = product.name, product.logo
		page.APIURL = "https://console.cloud.google.com/apis/enableflow;apiid=" + product.api
		page.MCPURL = "https://console.cloud.google.com/apis/enableflow;apiid=" + u.Hostname()
		page.DocsURL = "https://developers.google.com/workspace/" + product.product + "/guides/configure-mcp-server"
	}
	return page
}

// oauthVerifiedPage turns the tool listing taken right after a successful authorization into what the page says.
// Every sentence comes from fixed server text (inspection diagnostics carry no provider text), never from the
// callback. A working connection gets its result and nothing else; setup help appears only when the diagnostic names
// what is missing.
func oauthVerifiedPage(page *oauthCompletionPage, inspection extensions.MCPInspection) {
	page.State = "success"
	if len(inspection.Diagnostics) == 0 {
		if inspection.ToolCount == 0 {
			page.Message = "已连接，但这个服务暂时没有列出工具。"
		} else {
			page.Message = fmt.Sprintf("已连接，读取到 %d 个工具，可以使用了。", inspection.ToolCount)
		}
		return
	}
	diagnostic := inspection.Diagnostics[0]
	switch {
	case inspection.AuthRequired:
		page.Message = "授权已保存，但服务还不接受这次登录。请回到 Tofi 重新授权。"
	case diagnostic.Code == "api_disabled" || diagnostic.Code == "permission_denied":
		page.Setup = "enable"
		if diagnostic.Code == "api_disabled" && diagnostic.EnableURL != "" {
			// The exact API Google named, in the project it named: one button.
			page.EnableURL, page.EnableLabel = diagnostic.EnableURL, googleAPILabel(diagnostic.API)
		}
		page.Message = "授权已保存，但工具还不能用。" + diagnostic.Message
	case diagnostic.Code == "access_denied":
		page.Setup = "audience"
		page.Message = "授权已保存，但工具还不能用。" + diagnostic.Message
	default:
		page.Message = "授权已保存，但暂时读取不到工具。" + diagnostic.Message
	}
}

// googleAPILabel names an API for the "Enable ..." button.
func googleAPILabel(api string) string {
	for service, name := range map[string]string{googleapi.Gmail: "Gmail", googleapi.Calendar: "Google Calendar", googleapi.Drive: "Google Drive", googleapi.Docs: "Google Docs", googleapi.Sheets: "Google Sheets", googleapi.Slides: "Google Slides"} {
		if googleapi.ServiceAPI[service] == api {
			return name + " API"
		}
		if googleapi.ServiceMCPAPI[service] == api {
			return name + " MCP 服务"
		}
	}
	return "API"
}
