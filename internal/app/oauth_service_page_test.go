package app

import (
	"net/http/httptest"
	"testing"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

func TestOAuthServicePageUsesKnownEndpointNotUntrustedLabel(t *testing.T) {
	for _, tc := range []struct{ endpoint, service, api, logo string }{
		{"https://gmailmcp.googleapis.com/mcp/v1", "Google Gmail", "gmail.googleapis.com", "/brand/services/google-gmail.png"},
		{"https://drivemcp.googleapis.com/mcp/v1", "Google Drive", "drive.googleapis.com", "/brand/services/google-drive.png"},
		{"https://docsmcp.googleapis.com/mcp/v1", "Google Docs", "docs.googleapis.com", "/brand/services/google-docs.png"},
		{"https://sheetsmcp.googleapis.com/mcp/v1", "Google Sheets", "sheets.googleapis.com", "/brand/services/google-sheets.png"},
		{"https://slidesmcp.googleapis.com/mcp/v1", "Google Slides", "slides.googleapis.com", "/brand/services/google-slides.png"},
		{"https://calendarmcp.googleapis.com/mcp/v1", "Google Calendar", "calendar-json.googleapis.com", "/brand/services/google-calendar.png"},
	} {
		page := oauthServicePage(extensions.OAuthServiceIdentity{Name: "custom-alias", URL: tc.endpoint})
		if page.ServiceName != tc.service || page.LogoURL != tc.logo || page.APIURL != "https://console.cloud.google.com/apis/enableflow;apiid="+tc.api || page.MCPURL == "" {
			t.Fatalf("page=%+v", page)
		}
	}
	for _, endpoint := range []string{"http://gmailmcp.googleapis.com/mcp/v1", "https://gmailmcp.googleapis.com.evil.invalid/mcp/v1", "https://gmailmcp.googleapis.com:443/mcp/v1", "https://gmailmcp.googleapis.com/mcp/v1?token=private", "https://example.invalid/mcp", "https://mcp.notion.com.evil.invalid/mcp"} {
		page := oauthServicePage(extensions.OAuthServiceIdentity{Name: "notion", URL: endpoint})
		if page.LogoURL != oauthLogoGeneric || page.APIURL != "" || page.DocsURL != "" {
			t.Fatalf("untrusted branding=%+v", page)
		}
	}
	page := oauthServicePage(extensions.OAuthServiceIdentity{Name: "custom-name", URL: "https://mcp.notion.com/mcp"})
	if page.ServiceName != "Notion" || page.LogoURL != "/brand/services/notion.svg" {
		t.Fatalf("notion=%+v", page)
	}
	// The local gog flow has no endpoint and authorizes a Google account: the Google mark, nothing else.
	page = oauthServicePage(extensions.OAuthServiceIdentity{Name: "Google (gogcli)"})
	if page.LogoURL != "/brand/services/google-g.png" {
		t.Fatalf("gog=%+v", page)
	}
	page = oauthServicePage(extensions.OAuthServiceIdentity{Name: "Google (gogcli)", URL: "https://evil.invalid/mcp/v1"})
	if page.LogoURL != oauthLogoGeneric {
		t.Fatalf("gog name with a foreign endpoint=%+v", page)
	}
	page = oauthServicePage(extensions.OAuthServiceIdentity{})
	if page.ServiceName != "外部服务" || page.APIURL != "" {
		t.Fatal("invalid session used provider identity")
	}
}

func TestOAuthPendingPageRequiresOwner(t *testing.T) {
	if ownerIndependentRoute(httptest.NewRequest("GET", "/api/extensions/mcp/google-gmail/oauth/pending", nil)) {
		t.Fatal("pending page must not reveal configured services without owner authentication")
	}
}
