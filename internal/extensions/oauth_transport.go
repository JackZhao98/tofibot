package extensions

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

var ErrNoToken = errors.New("OAuth token is unavailable")
var ErrOAuthAuthorizationRequired = errors.New("OAuth authorization is required")
var ErrAuthorizationRequired = ErrOAuthAuthorizationRequired

// Token preserves the existing private token-file format across SDK upgrades.
type Token struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresIn    int64     `json:"expires_in,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitzero"`
}

func (t *Token) IsExpired() bool { return !t.ExpiresAt.IsZero() && time.Now().After(t.ExpiresAt) }

type TokenStore interface {
	GetToken(context.Context) (*Token, error)
	SaveToken(context.Context, *Token) error
}
type memoryTokenStore struct {
	mu    sync.Mutex
	token *Token
}

func NewMemoryTokenStore() TokenStore { return &memoryTokenStore{} }
func (s *memoryTokenStore) GetToken(ctx context.Context) (*Token, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token == nil {
		return nil, ErrNoToken
	}
	copy := *s.token
	return &copy, nil
}
func (s *memoryTokenStore) SaveToken(ctx context.Context, token *Token) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := *token
	s.token = &copy
	return nil
}

type OAuthFlowConfig struct {
	ClientID, ClientSecret, RedirectURI, AuthServerMetadataURL string
	Scopes                                                     []string
	TokenStore                                                 TokenStore
	PKCEEnabled                                                bool
}
type AuthServerMetadata = oauthex.AuthServerMeta

// OAuthHandler delegates OAuth URL/code/refresh mechanics to oauth2 and dynamic
// client registration to the official SDK. Manager owns state and persistence.
type OAuthHandler struct {
	cfg                    OAuthFlowConfig
	baseURL, expectedState string
	metadata               *AuthServerMetadata
}

func NewOAuthHandler(cfg OAuthFlowConfig) *OAuthHandler { return &OAuthHandler{cfg: cfg} }
func (h *OAuthHandler) SetBaseURL(u string)             { h.baseURL = u }
func (h *OAuthHandler) SetExpectedState(s string)       { h.expectedState = s }
func (h *OAuthHandler) GetClientID() string             { return h.cfg.ClientID }
func (h *OAuthHandler) GetClientSecret() string         { return h.cfg.ClientSecret }
func (h *OAuthHandler) GetServerMetadata(ctx context.Context) (*AuthServerMetadata, error) {
	if h.metadata != nil {
		return h.metadata, nil
	}
	metadataURL := h.cfg.AuthServerMetadataURL
	if metadataURL == "" {
		_, resolved, err := discoverOAuthMetadata(ctx, h.baseURL)
		if err != nil {
			return nil, err
		}
		metadataURL = resolved
	}
	var md AuthServerMetadata
	status, err := fetchOAuthJSON(ctx, metadataURL, &md)
	if err != nil {
		return nil, err
	}
	if status/100 != 2 {
		return nil, fmt.Errorf("OAuth metadata: HTTP %d", status)
	}
	if md.AuthorizationEndpoint == "" || md.TokenEndpoint == "" {
		return nil, errors.New("OAuth metadata missing endpoints")
	}
	for _, endpoint := range []string{metadataURL, md.AuthorizationEndpoint, md.TokenEndpoint, md.RegistrationEndpoint, md.RevocationEndpoint} {
		if endpoint != "" {
			if err := ValidateRedirectURI(endpoint); err != nil {
				return nil, fmt.Errorf("OAuth metadata endpoint: %w", err)
			}
		}
	}
	if len(md.CodeChallengeMethodsSupported) > 0 && !contains(md.CodeChallengeMethodsSupported, "S256") {
		return nil, errors.New("OAuth metadata does not support S256 PKCE")
	}
	h.metadata = &md
	return &md, nil
}
func (h *OAuthHandler) RegisterClient(ctx context.Context, name string) error {
	md, err := h.GetServerMetadata(ctx)
	if err != nil {
		return err
	}
	registration, err := oauthex.RegisterClient(ctx, md.RegistrationEndpoint, &oauthex.ClientRegistrationMetadata{
		RedirectURIs: []string{h.cfg.RedirectURI}, TokenEndpointAuthMethod: "none", GrantTypes: []string{"authorization_code", "refresh_token"}, ResponseTypes: []string{"code"}, ClientName: name, Scope: strings.Join(h.cfg.Scopes, " "),
	}, http.DefaultClient)
	if err != nil {
		return err
	}
	h.cfg.ClientID = registration.ClientID
	h.cfg.ClientSecret = registration.ClientSecret
	return nil
}
func (h *OAuthHandler) config(ctx context.Context) (*oauth2.Config, error) {
	md, err := h.GetServerMetadata(ctx)
	if err != nil {
		return nil, err
	}
	style := oauth2.AuthStyleInParams
	if contains(md.TokenEndpointAuthMethodsSupported, "client_secret_basic") && !contains(md.TokenEndpointAuthMethodsSupported, "client_secret_post") {
		style = oauth2.AuthStyleInHeader
	}
	return &oauth2.Config{ClientID: h.cfg.ClientID, ClientSecret: h.cfg.ClientSecret, RedirectURL: h.cfg.RedirectURI, Scopes: h.cfg.Scopes, Endpoint: oauth2.Endpoint{AuthURL: md.AuthorizationEndpoint, TokenURL: md.TokenEndpoint, AuthStyle: style}}, nil
}
func (h *OAuthHandler) GetAuthorizationURL(ctx context.Context, state, challenge string) (string, error) {
	cfg, err := h.config(ctx)
	if err != nil {
		return "", err
	}
	return cfg.AuthCodeURL(state, oauth2.SetAuthURLParam("code_challenge", challenge), oauth2.SetAuthURLParam("code_challenge_method", "S256"), oauth2.SetAuthURLParam("resource", h.baseURL)), nil
}
func (h *OAuthHandler) ProcessAuthorizationResponse(ctx context.Context, code, state, verifier string) error {
	if subtle.ConstantTimeCompare([]byte(state), []byte(h.expectedState)) != 1 {
		return errors.New("OAuth state mismatch")
	}
	cfg, err := h.config(ctx)
	if err != nil {
		return err
	}
	token, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier), oauth2.SetAuthURLParam("resource", h.baseURL))
	if err != nil {
		return err
	}
	return h.save(ctx, token, "")
}
func (h *OAuthHandler) save(ctx context.Context, token *oauth2.Token, oldRefresh string) error {
	refresh := token.RefreshToken
	if refresh == "" {
		refresh = oldRefresh
	}
	t := &Token{AccessToken: token.AccessToken, TokenType: token.TokenType, RefreshToken: refresh, ExpiresAt: token.Expiry, ExpiresIn: token.ExpiresIn}
	if scope, ok := token.Extra("scope").(string); ok {
		t.Scope = scope
	}
	return h.cfg.TokenStore.SaveToken(ctx, t)
}
func (h *OAuthHandler) RefreshToken(ctx context.Context, refresh string) (*Token, error) {
	cfg, err := h.config(ctx)
	if err != nil {
		return nil, err
	}
	// Preserve RFC 8707 resource binding during the RFC 6749 refresh grant.
	token, err := cfg.Exchange(ctx, "", oauth2.SetAuthURLParam("grant_type", "refresh_token"), oauth2.SetAuthURLParam("refresh_token", refresh), oauth2.SetAuthURLParam("resource", h.baseURL))
	if err != nil {
		return nil, err
	}
	if err := h.save(ctx, token, refresh); err != nil {
		return nil, err
	}
	if token.RefreshToken == "" {
		token.RefreshToken = refresh
	}
	return &Token{AccessToken: token.AccessToken, TokenType: token.TokenType, RefreshToken: token.RefreshToken, ExpiresAt: token.Expiry, ExpiresIn: token.ExpiresIn}, nil
}
func ValidateRedirectURI(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return errors.New("invalid OAuth redirect URI")
	}
	if u.Scheme == "https" {
		return nil
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme == "http" && (strings.EqualFold(u.Hostname(), "localhost") || ip != nil && ip.IsLoopback()) {
		return nil
	}
	return errors.New("OAuth redirect URI must use HTTPS or HTTP localhost")
}
func generateOAuthState() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

type storedMCPOAuth struct{ store TokenStore }

var _ auth.OAuthHandler = storedMCPOAuth{}

func (h storedMCPOAuth) TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	t, err := h.store.GetToken(ctx)
	if errors.Is(err, ErrNoToken) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if t.IsExpired() || t.AccessToken == "" {
		return nil, ErrOAuthAuthorizationRequired
	}
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: t.AccessToken, TokenType: t.TokenType, RefreshToken: t.RefreshToken, Expiry: t.ExpiresAt}), nil
}
func (storedMCPOAuth) Authorize(_ context.Context, _ *http.Request, res *http.Response) error {
	if res.Body != nil {
		_ = res.Body.Close()
	}
	return ErrOAuthAuthorizationRequired
}

func (h *OAuthHandler) GetAuthorizationHeader(ctx context.Context) (string, error) {
	token, err := h.cfg.TokenStore.GetToken(ctx)
	if err != nil {
		return "", err
	}
	if token.IsExpired() {
		return "", ErrOAuthAuthorizationRequired
	}
	kind := token.TokenType
	if kind == "" {
		kind = "Bearer"
	}
	return kind + " " + token.AccessToken, nil
}
