package app

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

// Paste-back sign-in (decision 0055) for a provider that accepts only a 127.0.0.1
// redirect, when neither the native app nor the shared computer is available.
// The person signs in in their own browser; the provider redirects to
// desktopOAuthRedirect, which does not load on their device; they paste that full
// address back. The server is the only parser: it takes the code and state from
// that one URL, compares the address with the registered redirect exactly, and hands
// both to the same session check every other route uses (state, expiry, redirect,
// PKCE at the provider). The pasted text is never logged, echoed, stored, or put
// into an event or an error.
const (
	pasteBackMaxURL   = 4096
	pasteBackMaxCode  = 2048
	pasteBackMaxState = 512
	pasteBackMaxError = 128
	pasteBackMaxIss   = 512
)

var errPasteInvalid = errors.New("pasted address is not a valid redirect")

// parsePastedRedirect returns the code, state and issuer (`iss`, RFC 9207; "" when
// absent) of a redirected address that matches redirect in scheme, host, port and
// path exactly. denied is true when the provider reported an error instead of a
// code (state is still required).
func parsePastedRedirect(raw, redirect string) (code, state, iss string, denied bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > pasteBackMaxURL {
		return "", "", "", false, errPasteInvalid
	}
	for _, c := range raw {
		if c < 0x21 || c == 0x7f {
			return "", "", "", false, errPasteInvalid
		}
	}
	want, werr := url.Parse(redirect)
	got, gerr := url.Parse(raw)
	if werr != nil || gerr != nil || got.Scheme != "http" || got.Opaque != "" || got.User != nil || got.Fragment != "" || got.RawFragment != "" ||
		got.Host != want.Host || got.Path != want.Path || got.RawQuery == "" {
		return "", "", "", false, errPasteInvalid
	}
	q, qerr := url.ParseQuery(got.RawQuery)
	if qerr != nil {
		return "", "", "", false, errPasteInvalid
	}
	one := func(key string, max int) (string, bool) {
		v := q[key]
		if len(v) > 1 || (len(v) == 1 && len(v[0]) > max) {
			return "", false
		}
		if len(v) == 0 {
			return "", true
		}
		return v[0], true
	}
	var ok bool
	if state, ok = one("state", pasteBackMaxState); !ok || state == "" {
		return "", "", "", false, errPasteInvalid
	}
	if code, ok = one("code", pasteBackMaxCode); !ok {
		return "", "", "", false, errPasteInvalid
	}
	if iss, ok = one("iss", pasteBackMaxIss); !ok {
		return "", "", "", false, errPasteInvalid
	}
	providerErr, ok := one("error", pasteBackMaxError)
	if !ok {
		return "", "", "", false, errPasteInvalid
	}
	if providerErr != "" {
		return "", state, iss, true, nil
	}
	if code == "" {
		return "", "", "", false, errPasteInvalid
	}
	return code, state, iss, false, nil
}

func (s *Server) routePasteOAuth(w http.ResponseWriter, r *http.Request, p string) bool {
	if r.Method != http.MethodPost || !strings.HasPrefix(p, "extensions/mcp/") {
		return false
	}
	rest := strings.TrimPrefix(p, "extensions/mcp/")
	switch {
	case strings.HasSuffix(rest, "/oauth/paste/start"):
		name := strings.TrimSuffix(rest, "/oauth/paste/start")
		// Only for a connection whose provider accepts loopback and nothing else;
		// any other provider has the https popup or the shared computer.
		if !s.extensions.OAuthLoopbackOnly(name) {
			writeErr(w, 400, "oauth_paste_not_needed", "This connection does not use paste-back sign-in.")
			return true
		}
		sid, authorization, err := s.extensions.OAuthStart(r.Context(), name, desktopOAuthRedirect)
		if err != nil {
			writeErr(w, 400, "oauth", extensions.OAuthPublicError(err))
			return true
		}
		writeJSON(w, 200, map[string]any{"session_id": sid, "authorization_url": authorization, "redirect_uri": desktopOAuthRedirect})
		return true
	case strings.HasSuffix(rest, "/oauth/paste/complete"):
		name := strings.TrimSuffix(rest, "/oauth/paste/complete")
		var in struct {
			SessionID string `json:"session_id"`
			URL       string `json:"url"`
		}
		if err := decodeExtensionJSON(r, &in); err != nil || in.SessionID == "" {
			writeErr(w, 400, "invalid_request", "session_id and url are required")
			return true
		}
		if !s.extensions.OAuthLoopbackOnly(name) {
			writeErr(w, 400, "oauth_paste_not_needed", "This connection does not use paste-back sign-in.")
			return true
		}
		code, state, iss, denied, err := parsePastedRedirect(in.URL, desktopOAuthRedirect)
		if err != nil {
			writeErr(w, 400, "oauth_paste_invalid", "The pasted address is not the redirect this sign-in expects.")
			return true
		}
		if _, err := s.extensions.OAuthPasteCallback(r.Context(), name, in.SessionID, code, state, desktopOAuthRedirect, iss, denied); err != nil {
			if errors.Is(err, extensions.ErrOAuthDenied) {
				writeErr(w, 400, "oauth_denied", "Authorization was not granted.")
			} else if errors.Is(err, extensions.ErrOAuthIssuerMismatch) {
				writeErr(w, 400, "oauth_issuer_mismatch", "The response did not come from the expected authorization server. Please connect again.")
			} else {
				writeErr(w, 400, "oauth", "Authorization could not be verified. Please connect again.")
			}
			return true
		}
		_, _ = s.store.WorkspaceEvent(workspaceScopeConfig)
		writeJSON(w, 200, map[string]any{"ok": true})
		return true
	}
	return false
}
