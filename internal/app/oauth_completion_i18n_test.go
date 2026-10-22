package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

// One row per UI language: the <html lang>, the success heading and the connected-with-N-tools sentence.
var oauthLocaleRows = []struct{ lang, heading, connected string }{
	{"en", "Authorization complete", "Connected. Tools available: 7."},
	{"zh-CN", "授权已完成", "已连接，读取到 7 个工具，可以使用了。"},
	{"zh-TW", "授權已完成", "已連線，讀取到 7 個工具，可以使用了。"},
	{"ja", "認可が完了しました", "接続しました。利用可能なツール: 7 件。"},
	{"ko", "인증 완료", "연결되었습니다. 사용 가능한 도구: 7개."},
	{"de", "Autorisierung abgeschlossen", "Verbunden. Verfügbare Tools: 7."},
	{"fr", "Autorisation terminée", "Connecté. Outils disponibles : 7."},
}

func TestOAuthCompletionSevenLocales(t *testing.T) {
	if len(oauthLocaleRows) != len(uiLanguages) {
		t.Fatalf("rows=%d languages=%d", len(oauthLocaleRows), len(uiLanguages))
	}
	for _, row := range oauthLocaleRows {
		t.Run(row.lang, func(t *testing.T) {
			page := oauthCompletionPage{Lang: row.lang, State: "success", LogoURL: oauthLogoGmail}
			oauthVerifiedPage(&page, extensions.MCPInspection{ToolCount: 7})
			w := httptest.NewRecorder()
			writeOAuthCompletion(w, http.StatusOK, page)
			body := w.Body.String()
			for _, want := range []string{`<html lang="` + row.lang + `">`, row.heading, row.connected, oauthText(row.lang, "service_default"), oauthText(row.lang, "return_link")} {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			if row.lang != "zh-CN" && strings.Contains(body, `lang="zh-CN"`) {
				t.Error("language is still hard-coded")
			}
			// Every catalog covers every key, so nothing falls through to another language or a bare key.
			for key := range oauthPageCatalogs["en"] {
				if oauthPageCatalogs[row.lang][key] == "" {
					t.Errorf("catalog %s lacks %q", row.lang, key)
				}
			}
			// The setup section is in the page language too.
			page = oauthCompletionPage{Lang: row.lang, State: "success", ServiceName: "Google Gmail", Setup: "audience"}
			w = httptest.NewRecorder()
			writeOAuthCompletion(w, http.StatusOK, page)
			if !strings.Contains(w.Body.String(), oauthText(row.lang, "audience_heading")) {
				t.Error("setup section not localized")
			}
		})
	}
	// An unknown language renders English, never Chinese.
	w := httptest.NewRecorder()
	writeOAuthCompletion(w, http.StatusOK, oauthCompletionPage{Lang: "xx", State: "success"})
	if !strings.Contains(w.Body.String(), `<html lang="en">`) || !strings.Contains(w.Body.String(), "External service") {
		t.Errorf("fallback not English: %s", w.Body.String())
	}
}

func TestOAuthAcceptLanguage(t *testing.T) {
	for header, want := range map[string]string{
		"":                           "",
		"fr-CH, fr;q=0.9, en;q=0.8":  "fr",
		"en;q=0.5, ja;q=0.9":         "ja",
		"zh-Hant-TW,zh;q=0.8":        "zh-TW",
		"zh-HK":                      "zh-TW",
		"zh-Hans-CN":                 "zh-CN",
		"zh":                         "zh-CN",
		"pt-BR, de;q=0.4":            "de",
		"ko;q=0, en;q=0.1":           "en",
		"es, it":                     "",
		"de_DE":                      "de",
		"*":                          "",
		"en-US,en;q=0.9,zh-CN;q=0.8": "en",
	} {
		if got := oauthAcceptLanguage(header); got != want {
			t.Errorf("%q -> %q, want %q", header, got, want)
		}
	}
}

// The route picks the language: a saved account language wins, else Accept-Language, else English.
func TestOAuthPageLanguageFromRequest(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	call := func(accept string) string {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/extensions/local-mcp/gog/oauth/callback?code=c&state=s", nil)
		if accept != "" {
			r.Header.Set("Accept-Language", accept)
		}
		s.routeLocalMCP(w, r, "extensions/local-mcp/gog/oauth/callback")
		return w.Body.String()
	}
	if body := call("de-DE,de;q=0.9"); !strings.Contains(body, `<html lang="de">`) || !strings.Contains(body, oauthText("de", "msg_gog_failed")) {
		t.Errorf("Accept-Language ignored: %s", body)
	}
	if body := call(""); !strings.Contains(body, `<html lang="en">`) {
		t.Errorf("no preference must be English: %s", body)
	}
	if err := s.store.putUserLanguage("ja"); err != nil {
		t.Fatal(err)
	}
	if body := call("de-DE"); !strings.Contains(body, `<html lang="ja">`) {
		t.Errorf("account language must win: %s", body)
	}
}
