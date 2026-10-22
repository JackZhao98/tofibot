package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/googleapi"
)

// One Google OAuth client for the whole workspace. The first Google connection uploads the client JSON; it is kept in
// the sealed secret vault (kind google_client, never listed or exported) so a second Google service, through gogcli or
// a Workspace MCP entry, signs in again with the same client and never asks for the JSON. The browser can read which
// project it belongs to and replace or remove it; it can never read the secret back.
const (
	googleClientKind = "google_client"
	googleClientID   = "workspace:google_client"
)

var errNoGoogleClient = errors.New("no Google OAuth client is saved")

// saveGoogleClient seals the uploaded JSON after checking it is a usable Web client.
func (s *Server) saveGoogleClient(raw []byte) (googleapi.Client, error) {
	client, err := googleapi.ParseClient(raw)
	if err != nil {
		return googleapi.Client{}, err
	}
	v := s.secretVault
	if v == nil {
		return googleapi.Client{}, errors.New("secret storage unavailable")
	}
	record := secretRecord{ID: googleClientID, Name: "Google OAuth client", Kind: googleClientKind, Target: "google", Label: client.Project(), Status: "stored", CreatedAt: now()}
	if record.Ciphertext, err = v.seal(googleClientID, string(raw)); err != nil {
		return googleapi.Client{}, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	previous, existed := v.records[googleClientID]
	v.records[googleClientID] = record
	if err = v.saveLocked(); err != nil {
		if existed {
			v.records[googleClientID] = previous
		} else {
			delete(v.records, googleClientID)
		}
		return googleapi.Client{}, err
	}
	return client, nil
}

// googleClient returns the saved client JSON and its parsed form.
func (s *Server) googleClient() ([]byte, googleapi.Client, error) {
	v := s.secretVault
	if v == nil {
		return nil, googleapi.Client{}, errNoGoogleClient
	}
	v.mu.Lock()
	record, ok := v.records[googleClientID]
	v.mu.Unlock()
	if !ok || record.Kind != googleClientKind {
		return nil, googleapi.Client{}, errNoGoogleClient
	}
	raw, err := v.reveal(record)
	if err != nil {
		return nil, googleapi.Client{}, err
	}
	client, err := googleapi.ParseClient([]byte(raw))
	return []byte(raw), client, err
}

func (s *Server) deleteGoogleClient() error {
	v := s.secretVault
	if v == nil {
		return errors.New("secret storage unavailable")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	previous, existed := v.records[googleClientID]
	if !existed {
		return nil
	}
	delete(v.records, googleClientID)
	if err := v.saveLocked(); err != nil {
		v.records[googleClientID] = previous
		return err
	}
	return nil
}

type googleAPIView struct {
	Service   string `json:"service"`
	API       string `json:"api"`
	MCPAPI    string `json:"mcp_api"`
	EnableURL string `json:"enable_url"`
	MCPURL    string `json:"mcp_enable_url"`
}

// googleOverview is what Settings shows once for the Google account: the saved client's project (never its
// secret), the account signed in through gogcli, and the enable link of every Google API TOFI offers, built for that project.
func (s *Server) googleOverview(r *http.Request) map[string]any {
	out := map[string]any{"client": map[string]any{"configured": false}}
	project := ""
	if _, client, err := s.googleClient(); err == nil {
		project = client.Project()
		out["client"] = map[string]any{"configured": true, "project_id": client.ProjectID, "project_number": client.ProjectNumber}
	}
	services := make([]string, 0, len(googleapi.ServiceAPI))
	for service := range googleapi.ServiceAPI {
		services = append(services, service)
	}
	sort.Strings(services)
	apis := []googleAPIView{}
	for _, service := range services {
		apis = append(apis, googleAPIView{Service: service, API: googleapi.ServiceAPI[service], MCPAPI: googleapi.ServiceMCPAPI[service],
			EnableURL: googleapi.EnableURL(googleapi.ServiceAPI[service], project), MCPURL: googleapi.EnableURL(googleapi.ServiceMCPAPI[service], project)})
	}
	out["apis"] = apis
	return out
}

func (s *Server) routeGoogle(w http.ResponseWriter, r *http.Request, p string) bool {
	switch {
	case p == "extensions/google" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, s.googleOverview(r))
		return true
	case p == "extensions/google/client" && r.Method == http.MethodPut:
		var input struct {
			CredentialsJSON json.RawMessage `json:"credentials_json"`
		}
		if err := decodeExtensionJSON(r, &input); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
			return true
		}
		if _, err := s.saveGoogleClient(input.CredentialsJSON); err != nil {
			writeErr(w, http.StatusBadRequest, "google_client_invalid", err.Error())
			return true
		}
		_, _ = s.store.WorkspaceEvent(workspaceScopeConfig)
		writeJSON(w, http.StatusOK, s.googleOverview(r))
		return true
	case p == "extensions/google/client" && r.Method == http.MethodDelete:
		if err := s.deleteGoogleClient(); err != nil {
			writeErr(w, http.StatusInternalServerError, "google_client", err.Error())
			return true
		}
		_, _ = s.store.WorkspaceEvent(workspaceScopeConfig)
		writeJSON(w, http.StatusOK, s.googleOverview(r))
		return true
	}
	return false
}

// useWorkspaceGoogleClient fills an MCP entry's OAuth client from the saved workspace client. It writes the error
// response itself and returns false when there is none to use.
func (s *Server) useWorkspaceGoogleClient(w http.ResponseWriter, oauth **extensions.OAuthConfig) bool {
	_, client, err := s.googleClient()
	if err != nil {
		writeErr(w, http.StatusConflict, "google_client_missing", "No Google OAuth client is saved yet. Add one first.")
		return false
	}
	if *oauth == nil {
		*oauth = &extensions.OAuthConfig{}
	}
	(*oauth).ClientID, (*oauth).ClientSecret = client.ClientID, client.ClientSecret
	return true
}
