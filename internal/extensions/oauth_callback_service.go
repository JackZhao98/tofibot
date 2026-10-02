package extensions

import (
	"context"
	"crypto/subtle"
	"errors"
	"time"
)

var ErrOAuthDenied = errors.New("OAuth authorization was not granted")

// OAuthServiceIdentity is only returned after matching the live state, service
// and callback channel. It contains no credentials or authorization parameters.
type OAuthServiceIdentity struct {
	Name string
	URL  string
}

func (m *Manager) OAuthWebCallback(ctx context.Context, server, sid, code, state, redirect string, denied bool) (OAuthServiceIdentity, error) {
	m.mu.Lock()
	if sid == "" {
		sid = m.oauthState[state]
	}
	s, ok := m.oauth[sid]
	if !ok || s.Flow == nil || state == "" || redirect == "" || time.Now().After(s.Expires) || s.Server != server || s.RedirectURI != redirect || subtle.ConstantTimeCompare([]byte(state), []byte(s.Flow.State)) != 1 {
		m.mu.Unlock()
		return OAuthServiceIdentity{}, errors.New("OAuth response could not be verified")
	}
	current, err := loadServers(m.cfg.MCPConfigPath)
	if err != nil || !s.Store.active.Load() || m.tokenStores[s.Server] != s.Store || s.SnapshotTarget != credentialTargetKey(current[s.Server]) {
		m.mu.Unlock()
		return OAuthServiceIdentity{}, errCredentialInvalidated
	}
	identity := OAuthServiceIdentity{Name: s.Server, URL: current[s.Server].URL}
	if denied {
		delete(m.oauth, sid)
		delete(m.oauthState, s.Flow.State)
		m.mu.Unlock()
		return identity, ErrOAuthDenied
	}
	m.mu.Unlock()
	if code == "" {
		return identity, errors.New("OAuth authorization code is missing")
	}
	// The existing exchange rechecks expiry, state, redirect and credential
	// liveness, then consumes the flow once and verifies PKCE at the provider.
	err = m.OAuthCallbackForRedirect(ctx, sid, code, state, redirect)
	return identity, err
}
