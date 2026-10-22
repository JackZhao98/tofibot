// Package googleapi holds what TOFI knows about Google Cloud for its Google connections: which API a service needs,
// how to read the project out of an uploaded OAuth client, where to send a person to enable an API, and how to read
// Google's structured "API not enabled" error. Every link is built here from a known API id and a validated
// project; nothing from a provider error body (an activation URL, a message) is ever reflected.
package googleapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// Service keys are the ones TOFI offers: the gogcli Gmail path, and the six Workspace MCP servers.
const (
	Gmail    = "gmail"
	Calendar = "calendar"
	Drive    = "drive"
	Docs     = "docs"
	Sheets   = "sheets"
	Slides   = "slides"
)

// ServiceAPI is the product API a service calls (gogcli talks to it directly; the Workspace MCP servers need it too).
var ServiceAPI = map[string]string{
	Gmail:    "gmail.googleapis.com",
	Calendar: "calendar-json.googleapis.com",
	Drive:    "drive.googleapis.com",
	Docs:     "docs.googleapis.com",
	Sheets:   "sheets.googleapis.com",
	Slides:   "slides.googleapis.com",
}

// ServiceMCPAPI is the Workspace MCP service of the same product (Developer Preview).
var ServiceMCPAPI = map[string]string{
	Gmail:    "gmailmcp.googleapis.com",
	Calendar: "calendarmcp.googleapis.com",
	Drive:    "drivemcp.googleapis.com",
	Docs:     "docsmcp.googleapis.com",
	Sheets:   "sheetsmcp.googleapis.com",
	Slides:   "slidesmcp.googleapis.com",
}

var knownAPI = func() map[string]bool {
	out := map[string]bool{}
	for _, api := range ServiceAPI {
		out[api] = true
	}
	for _, api := range ServiceMCPAPI {
		out[api] = true
	}
	return out
}()

// KnownAPI reports whether api is one TOFI links to.
func KnownAPI(api string) bool { return knownAPI[api] }

var (
	projectID     = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
	projectNumber = regexp.MustCompile(`^[0-9]{6,20}$`)
	clientID      = regexp.MustCompile(`^([0-9]{6,20})-[A-Za-z0-9_-]+\.apps\.googleusercontent\.com$`)
)

// Client is the part of a downloaded "Web application" OAuth client JSON that TOFI uses.
type Client struct {
	ClientID      string
	ClientSecret  string
	ProjectID     string // "project_id" from the JSON, empty when absent or malformed
	ProjectNumber string // the digits before the first dash of the client id
	RedirectURIs  []string
}

// ParseClient reads a Google "Web application" OAuth client JSON. The project number comes from the client id
// (Google ids start with it) and the project id from the JSON's own field; either may be missing but not both.
func ParseClient(raw []byte) (Client, error) {
	if len(raw) == 0 || len(raw) > 32<<10 {
		return Client{}, errors.New("Google Web OAuth client JSON required")
	}
	var file struct {
		Web struct {
			ClientID     string   `json:"client_id"`
			ClientSecret string   `json:"client_secret"`
			ProjectID    string   `json:"project_id"`
			RedirectURIs []string `json:"redirect_uris"`
		} `json:"web"`
	}
	if err := json.Unmarshal(raw, &file); err != nil || file.Web.ClientID == "" || file.Web.ClientSecret == "" {
		return Client{}, errors.New("Google Web OAuth client JSON required")
	}
	c := Client{ClientID: file.Web.ClientID, ClientSecret: file.Web.ClientSecret, RedirectURIs: file.Web.RedirectURIs}
	if m := clientID.FindStringSubmatch(c.ClientID); m != nil {
		c.ProjectNumber = m[1]
	}
	if projectID.MatchString(file.Web.ProjectID) {
		c.ProjectID = file.Web.ProjectID
	}
	return c, nil
}

// Project is what the console accepts in `?project=`: the id when known, else the number, else "".
func (c Client) Project() string {
	if c.ProjectID != "" {
		return c.ProjectID
	}
	return c.ProjectNumber
}

// ValidProject reports whether s is a project id or number the console accepts.
func ValidProject(s string) bool { return projectID.MatchString(s) || projectNumber.MatchString(s) }

// EnableURL is the console page that enables one API in one project. With no usable project it falls back to the
// enable flow, where Google asks which project. An API TOFI does not know yields "" (no link).
func EnableURL(api, project string) string {
	if !KnownAPI(api) {
		return ""
	}
	if ValidProject(project) {
		return "https://console.cloud.google.com/apis/library/" + api + "?project=" + url.QueryEscape(project)
	}
	return "https://console.cloud.google.com/apis/enableflow;apiid=" + api
}

// Failure is a structured Google API error that says an API is not enabled in the caller's project.
type Failure struct {
	Reason  string // always "SERVICE_DISABLED"
	API     string // the service id from the error's ErrorInfo, restricted to the APIs TOFI knows
	Project string // the project number from the error's ErrorInfo, validated
}

// ParseFailure finds Google's JSON error object inside an error's text and reads its structured fields: HTTP-style
// `error.status == PERMISSION_DENIED` with a `google.rpc.ErrorInfo` detail whose `reason` is `SERVICE_DISABLED`
// (`metadata.service` names the API, `metadata.consumer` the project), or the older `error.errors[].reason ==
// accessNotConfigured`. The decision rests on those fields only, never on the message wording.
func ParseFailure(text string) (Failure, bool) {
	for start := 0; start < len(text); {
		i := strings.IndexByte(text[start:], '{')
		if i < 0 {
			return Failure{}, false
		}
		start += i
		var body struct {
			Error struct {
				Status  string `json:"status"`
				Code    int    `json:"code"`
				Details []struct {
					Type     string            `json:"@type"`
					Reason   string            `json:"reason"`
					Metadata map[string]string `json:"metadata"`
				} `json:"details"`
				Errors []struct {
					Reason string `json:"reason"`
				} `json:"errors"`
			} `json:"error"`
		}
		decoder := json.NewDecoder(bytes.NewReader([]byte(text[start:])))
		if err := decoder.Decode(&body); err != nil {
			start++
			continue
		}
		e := body.Error
		for _, detail := range e.Details {
			if strings.HasSuffix(detail.Type, "google.rpc.ErrorInfo") && detail.Reason == "SERVICE_DISABLED" && (e.Status == "PERMISSION_DENIED" || e.Code == 403) {
				f := Failure{Reason: "SERVICE_DISABLED"}
				if api := detail.Metadata["service"]; KnownAPI(api) {
					f.API = api
				}
				consumer := strings.TrimPrefix(detail.Metadata["consumer"], "projects/")
				if projectNumber.MatchString(consumer) {
					f.Project = consumer
				}
				return f, true
			}
		}
		for _, item := range e.Errors {
			if item.Reason == "accessNotConfigured" {
				return Failure{Reason: "SERVICE_DISABLED"}, true
			}
		}
		return Failure{}, false
	}
	return Failure{}, false
}
