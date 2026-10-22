package app

import (
	"net/url"
	"strings"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/googleapi"
)

// Provider artwork and configuration links come from known endpoints, never
// from callback parameters or provider-supplied error URLs.
func oauthServicePage(identity extensions.OAuthServiceIdentity) oauthCompletionPage {
	page := oauthCompletionPage{LogoURL: oauthLogoGeneric}
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
			page.MessageKey = "msg_connected_none"
		} else {
			page.MessageKey, page.MessageCount = "msg_connected_n", inspection.ToolCount
		}
		return
	}
	diagnostic := inspection.Diagnostics[0]
	page.DetailCode, page.MessageDetail = diagnostic.Code, diagnostic.Message
	switch {
	case inspection.AuthRequired:
		page.MessageKey, page.DetailCode, page.MessageDetail = "msg_auth_rejected", "", ""
	case diagnostic.Code == "api_disabled" || diagnostic.Code == "permission_denied":
		page.Setup = "enable"
		if diagnostic.Code == "api_disabled" && diagnostic.EnableURL != "" {
			// The exact API Google named, in the project it named: one button.
			page.EnableURL = diagnostic.EnableURL
			page.EnableLabel, page.EnableKind = googleAPILabel(diagnostic.API)
		}
		page.MessageKey = "msg_saved_unusable"
	case diagnostic.Code == "access_denied":
		page.Setup = "audience"
		page.MessageKey = "msg_saved_unusable"
	default:
		page.MessageKey = "msg_saved_unreadable"
	}
}

// googleAPILabel names an API for the "Enable ..." button: the service, and which of its two switches (api or mcp).
func googleAPILabel(api string) (name, kind string) {
	for service, name := range map[string]string{googleapi.Gmail: "Gmail", googleapi.Calendar: "Google Calendar", googleapi.Drive: "Google Drive", googleapi.Docs: "Google Docs", googleapi.Sheets: "Google Sheets", googleapi.Slides: "Google Slides"} {
		if googleapi.ServiceAPI[service] == api {
			return name, "api"
		}
		if googleapi.ServiceMCPAPI[service] == api {
			return name, "mcp"
		}
	}
	return "", "api"
}
