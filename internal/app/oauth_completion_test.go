package app

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func completionFixture(state string) oauthCompletionPage {
	return oauthCompletionPage{
		State: state, ServiceName: "Google Gmail", LogoURL: "/oauth/provider-google.svg",
		APIURL:  "https://console.cloud.google.com/apis/enableflow;apiid=gmail.googleapis.com",
		MCPURL:  "https://console.cloud.google.com/apis/enableflow;apiid=gmailmcp.googleapis.com",
		DocsURL: "https://developers.google.com/workspace/gmail/api/guides/configure-mcp-server",
	}
}

func TestOAuthCompletionStatesAndHeaders(t *testing.T) {
	for _, tc := range []struct {
		state, title, heading string
		status                int
	}{
		{"pending", "Connecting", "正在连接", http.StatusOK},
		{"success", "Connection complete", "授权已完成", http.StatusOK},
		{"failed", "Connection failed", "授权未完成", http.StatusBadRequest},
		{"denied", "Connection failed", "授权被拒绝", http.StatusForbidden},
		{"unknown", "Connection failed", "授权未完成", http.StatusBadRequest},
	} {
		t.Run(tc.state, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeOAuthCompletion(w, tc.status, completionFixture(tc.state))
			if w.Code != tc.status {
				t.Fatalf("status = %d", w.Code)
			}
			body := w.Body.String()
			for _, want := range []string{"<title>" + tc.title + "</title>", tc.heading, `href="/"`, `src="/oauth/connection.js"`, `href="/oauth/connection.css"`, "与 OAuth Client 相同的项目", `href="https://console.cloud.google.com/auth/audience"`} {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			if strings.Contains(body, "工具已就绪") {
				t.Error("authorization must not claim tools are ready")
			}
			for key, want := range map[string]string{
				"Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store",
				"Referrer-Policy": "no-referrer", "X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY",
			} {
				if got := w.Header().Get(key); got != want {
					t.Errorf("%s = %q", key, got)
				}
			}
			csp := w.Header().Get("Content-Security-Policy")
			for _, directive := range []string{"default-src 'none'", "script-src 'self'", "style-src 'self'", "img-src 'self'", "font-src 'self'", "connect-src 'none'", "base-uri 'none'", "form-action 'none'", "frame-ancestors 'none'"} {
				if !strings.Contains(csp, directive) {
					t.Errorf("missing CSP %s", directive)
				}
			}
			if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "https:") {
				t.Fatal("CSP permits nonlocal resources")
			}
		})
	}
}

func TestOAuthCompletionEscapesAndRejectsDestinations(t *testing.T) {
	w := httptest.NewRecorder()
	writeOAuthCompletion(w, http.StatusBadRequest, oauthCompletionPage{
		State: `failed" onload="alert(1)`, ServiceName: `<script>alert("service")</script>`,
		Message: `<img src=x onerror="alert('message')"> & retry`,
		LogoURL: "https://tracker.example/logo.svg?code=secret",
		APIURL:  "javascript:alert(1)", MCPURL: "//tracker.example/enable",
		DocsURL: "https://developers.google.com.evil.example/workspace/",
	})
	body := w.Body.String()
	for _, forbidden := range []string{"<script>alert", "<img src=x", "tracker.example", "javascript:", "evil.example", "code=secret", "onload=", "Google 连接设置", "ZgotmplZ"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("reflected unsafe content: %q", forbidden)
		}
	}
	for _, want := range []string{"&lt;script&gt;", "&lt;img", "&amp; retry", `data-state="failed"`, `src="/oauth/provider-generic.svg"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing escaped content: %q", want)
		}
	}
}

func TestOAuthCompletionAllowedLinksAndLocalMarks(t *testing.T) {
	for _, logo := range []string{"/oauth/provider-google.svg", "/oauth/provider-notion.svg", "/oauth/provider-generic.svg"} {
		page := completionFixture("success")
		page.LogoURL = logo
		w := httptest.NewRecorder()
		writeOAuthCompletion(w, http.StatusOK, page)
		body := w.Body.String()
		for _, link := range []string{page.APIURL, page.MCPURL, page.DocsURL} {
			if !strings.Contains(body, `href="`+link+`" target="_blank" rel="noopener noreferrer"`) {
				t.Errorf("unsafe or missing official link: %s", link)
			}
		}
		if !strings.Contains(body, `src="`+logo+`"`) {
			t.Errorf("missing local mark: %s", logo)
		}
	}
	for _, raw := range []string{
		"https://console.cloud.google.com/apis/enableflow;apiid=gmail.googleapis.com?code=secret",
		"https://console.cloud.google.com/apis/enableflow;apiid=gmail.googleapis.com#state",
		"https://console.cloud.google.com/apis/enableflow;apiid=gmail.googleapis.com?",
		"https://user@console.cloud.google.com/apis/enableflow;apiid=gmail.googleapis.com",
		"https://console.cloud.google.com:443/apis/enableflow;apiid=gmail.googleapis.com",
		"https://console.cloud.google.com/redirect",
		"https://console.cloud.google.com/apis/enableflow;apiid=gmail.googleapis.com.evil",
		"http://console.cloud.google.com/apis/enableflow;apiid=gmail.googleapis.com",
	} {
		if got := oauthCompletionLink(raw, false); got != "" {
			t.Errorf("allowed unsafe URL: %s", got)
		}
	}
	if oauthCompletionLink("https://www.notion.com/help/notion-mcp", true) == "" {
		t.Error("Notion docs missing")
	}
	w := httptest.NewRecorder()
	writeOAuthCompletion(w, http.StatusBadRequest, oauthCompletionPage{State: "failed"})
	if !strings.Contains(w.Body.String(), "外部服务") {
		t.Error("missing generic identity")
	}
}

func TestOAuthCompletionMessageReplacesDefaultCopy(t *testing.T) {
	w := httptest.NewRecorder()
	page := completionFixture("success")
	page.Message = "授权已保存。授权成功不代表服务 API 已启用。"
	writeOAuthCompletion(w, http.StatusOK, page)
	if strings.Count(w.Body.String(), "服务 API 已启用") != 1 {
		t.Fatal("authorization caveat repeated")
	}
}

// Opt-in, synthetic browser fixture serving the actual renderer and built UI.
// No accounts, callbacks, credentials or production services are contacted.
// TOFI_OAUTH_AUDIT_ADDR=127.0.0.1:5179 go test ./internal/app -run '^TestOAuthCompletionAuditServer$' -timeout 24h -v
func TestOAuthCompletionAuditServer(t *testing.T) {
	addr := os.Getenv("TOFI_OAUTH_AUDIT_ADDR")
	if addr == "" {
		t.Skip("set TOFI_OAUTH_AUDIT_ADDR for isolated browser QA")
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host != "127.0.0.1" {
		t.Fatal("fixture requires explicit IPv4 loopback address")
	}
	ui, err := filepath.Abs("../../ui/dist")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ui, "oauth/connection.css")); err != nil {
		t.Fatal("build UI before running fixture:", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/audit", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html lang="en"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Synthetic OAuth QA</title><body><h1>Synthetic OAuth QA</h1><p>Local fixture only. Choose saved Tofi appearance for callback testing.</p><button data-theme="light">Light</button> <button data-theme="dark">Dark</button> <button data-theme="system">System</button><p id="appearance"></p><nav><a href="/success">Google success</a> · <a href="/pending">Pending</a> · <a href="/failed">Failure</a> · <a href="/denied">Denied</a> · <a href="/notion">Notion</a> · <a href="/long">Long generic name</a> · <a href="/no-script">Without JavaScript</a></nav><script src="/audit-theme.js"></script></body></html>`))
	})
	mux.HandleFunc("/audit-theme.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		_, _ = w.Write([]byte(`document.querySelectorAll("button[data-theme]").forEach(button => button.addEventListener("click", () => { localStorage.setItem("tofi:appearance", button.dataset.theme); document.getElementById("appearance").textContent = "Saved: " + button.dataset.theme; }));`))
	})
	mux.HandleFunc("/no-script", func(w http.ResponseWriter, r *http.Request) {
		rendered := httptest.NewRecorder()
		writeOAuthCompletion(rendered, http.StatusOK, completionFixture("success"))
		for key, values := range rendered.Header() {
			w.Header()[key] = values
		}
		w.Header().Set("Content-Security-Policy", strings.ReplaceAll(w.Header().Get("Content-Security-Policy"), "script-src 'self'", "script-src 'none'"))
		_, _ = w.Write(rendered.Body.Bytes())
	})
	mux.HandleFunc("/api/extensions/mcp/fixture/oauth/callback", func(w http.ResponseWriter, r *http.Request) {
		page := completionFixture("success")
		if r.URL.Query().Get("fixture") == "failed" {
			page.State = "failed"
			page.Message = "无法读取工具。请检查连接配置后重试。"
		}
		writeOAuthCompletion(w, http.StatusOK, page)
	})
	for _, state := range []string{"pending", "success", "failed", "denied", "notion", "long"} {
		mux.HandleFunc("/"+state, func(w http.ResponseWriter, r *http.Request) {
			page := completionFixture(state)
			switch state {
			case "notion":
				page = oauthCompletionPage{State: "success", ServiceName: "Notion", LogoURL: "/oauth/provider-notion.svg", DocsURL: "https://www.notion.com/help/notion-mcp"}
			case "long":
				page = oauthCompletionPage{State: "failed", ServiceName: "一个很长的自定义服务名称 LongUnbrokenServiceNameForWrapping", Message: "未能完成授权。请返回 Tofi 重试。"}
			case "failed":
				page.Message = "无法完成授权，请核对服务配置。"
			case "denied":
				page.Message = "此次授权未获批准。"
			}
			writeOAuthCompletion(w, http.StatusOK, page)
		})
	}
	mux.Handle("/", http.FileServer(http.Dir(ui)))
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	t.Logf("Synthetic OAuth renderer fixture: http://%s/success (assets: %s)", addr, ui)
	t.Fatal(server.ListenAndServe())
}
