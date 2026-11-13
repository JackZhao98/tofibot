package app

// The personal browser wire contract is deliberately independent of desktop input.
import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/jpeg"
	"io"
	"math"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/google/uuid"
)

var browserActions = map[string]bool{
	"browser.open": true, "browser.navigate": true, "browser.read": true, "browser.snapshot": true, "browser.click": true,
	"browser.type": true, "browser.key": true, "browser.scroll": true, "browser.back": true, "browser.close": true,
}
var browserFailureCodes = map[string]bool{
	"browser_not_enabled": true, "app_not_allowed": true, "bot_not_allowed": true, "browser_target_gone": true,
	"browser_session_changed": true, "browser_viewport_changed": true, "browser_control_lost": true, "browser_login_required": true,
	"browser_action_unsupported": true, "user_takeover": true, "user_stop": true, "browser_server_unsupported": true,
	"browser_client_unsupported": true, "browser_transport_unavailable": true, "computer_offline": true, "computer_lost": true, "device_revoked": true,
}

func browserError(code, certainty string) error {
	messages := map[string]string{
		"browser_not_enabled":           "Ask the person to enable the dedicated browser in the Mac app advanced settings.",
		"app_not_allowed":               "This step has no permitted Bot identity. Ask the person to check the Mac app permissions.",
		"bot_not_allowed":               "The person denied this Bot on their Mac. Ask them to check Bots denied on this Mac in Settings > Environments. Do not retry.",
		"browser_target_gone":           "The page no longer exists. Ask the person whether to open a new one; do not recreate it automatically.",
		"browser_session_changed":       "The browser session changed. Read its new identity and discard the old action.",
		"browser_viewport_changed":      "The viewport changed. Take a new snapshot before using coordinates.",
		"browser_control_lost":          "Browser control was lost. Explain the uncertainty and wait for the person.",
		"browser_login_required":        "The site needs a sign-in that this browser does not have. Never type passwords.",
		"browser_action_unsupported":    "This browser cannot do this action.",
		"user_takeover":                 "The person took control of the browser. Wait for explicit local handover; there is no automatic resume.",
		"user_stop":                     "The person stopped browser control. Stop and wait for their instruction.",
		"browser_server_unsupported":    "Ask the person to update the server for browser protocol version 1.",
		"browser_client_unsupported":    "Ask the person to update the Mac app for browser protocol version 1.",
		"browser_transport_unavailable": "The browser connection is unavailable. Reconnection does not authorize replay of an interrupted action.",
		"browser_unavailable":           "No browser is connected in any environment.",
	}
	status := tooloutcome.Denied
	if certainty == "unknown" {
		status = tooloutcome.Uncertain
	}
	return tooloutcome.New(status, code, certainty, "Bot browser: "+code+". "+messages[code]+" Do not automatically retry an action whose effect is unknown.", "explain_blocker").Err()
}
func browserUUID(s string) bool {
	u, e := uuid.Parse(s)
	return e == nil && u.String() == strings.ToLower(s) && len(s) == 36
}
func browserSafe(n int64) bool { return n >= 0 && n <= 9007199254740991 }
func browserURL(s string) bool {
	if len(s) > 8192 || !utf8.ValidString(s) {
		return false
	}
	if s == "about:blank" {
		return true
	}
	u, e := url.Parse(s)
	return e == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}
func browserDecode(raw []byte, out any) error {
	if !utf8.Valid(raw) {
		return errors.New("invalid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func browserObject(raw []byte, required, optional string) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if e := browserDecode(raw, &m); e != nil || m == nil {
		return nil, errors.New("object required")
	}
	allowed := map[string]bool{}
	for _, k := range strings.Fields(required + " " + optional) {
		allowed[k] = true
	}
	for _, k := range strings.Fields(required) {
		if _, ok := m[k]; !ok {
			return nil, errors.New("missing field: " + k)
		}
	}
	for k, v := range m {
		if !allowed[k] || string(v) == "null" {
			return nil, errors.New("invalid field: " + k)
		}
	}
	return m, nil
}
func browserString(m map[string]json.RawMessage, k string, limit int, required bool) bool {
	v, ok := m[k]
	if !ok {
		return !required
	}
	var s string
	return json.Unmarshal(v, &s) == nil && utf8.ValidString(s) && len(s) <= limit && (!required || s != "")
}
func browserNumber(m map[string]json.RawMessage, k string, limit float64) bool {
	var n float64
	v, ok := m[k]
	return ok && json.Unmarshal(v, &n) == nil && !math.IsNaN(n) && !math.IsInf(n, 0) && math.Abs(n) <= limit
}
func browserInteger(m map[string]json.RawMessage, k string, min, max int64) bool {
	var n int64
	return json.Unmarshal(m[k], &n) == nil && n >= min && n <= max
}

// validateBrowserArgs accepts only server-stamped queue arguments.
func validateBrowserArgs(action string, raw []byte) error {
	required, optional := "v connection_epoch", "session_epoch target_id"
	switch action {
	case "browser.open", "browser.navigate":
		required += " url"
	case "browser.read":
		optional += " find max_chars"
	case "browser.snapshot", "browser.back", "browser.close":
	case "browser.click":
		optional += " click x y viewport_epoch"
	case "browser.type":
		required += " text"
	case "browser.key":
		required += " key"
		optional += " modifiers"
	case "browser.scroll":
		required += " delta_x delta_y viewport_epoch"
	default:
		return browserError("browser_action_unsupported", "not_executed")
	}
	m, e := browserObject(raw, required, optional)
	if e != nil {
		return e
	}
	if !browserInteger(m, "v", 1, 1) {
		return errors.New("invalid version")
	}
	var connection, session, target string
	_ = json.Unmarshal(m["connection_epoch"], &connection)
	_ = json.Unmarshal(m["session_epoch"], &session)
	_ = json.Unmarshal(m["target_id"], &target)
	if !browserUUID(connection) || (session != "" && !browserUUID(session)) || (target != "" && !browserUUID(target)) {
		return errors.New("invalid identity")
	}
	if action != "browser.open" && (!browserUUID(session) || !browserUUID(target)) {
		return errors.New("target and session required")
	}
	if action == "browser.open" && target != "" {
		return errors.New("open has no target")
	}
	switch action {
	case "browser.open", "browser.navigate":
		var u string
		_ = json.Unmarshal(m["url"], &u)
		if !browserURL(u) {
			return errors.New("invalid URL")
		}
	case "browser.read":
		if _, ok := m["max_chars"]; ok && !browserInteger(m, "max_chars", 1000, 60000) {
			return errors.New("invalid max_chars")
		}
		if !browserString(m, "find", 1024, false) {
			return errors.New("invalid find")
		}
	case "browser.click":
		_, text := m["click"]
		_, x := m["x"]
		_, y := m["y"]
		_, viewport := m["viewport_epoch"]
		if text {
			if x || y || viewport || !browserString(m, "click", 1024, true) {
				return errors.New("invalid click")
			}
		} else if !x || !y || !viewport || !browserNumber(m, "x", 1e7) || !browserNumber(m, "y", 1e7) || !browserInteger(m, "viewport_epoch", 1, 9007199254740991) {
			return errors.New("invalid coordinates")
		}
	case "browser.type":
		if !browserString(m, "text", 16384, false) {
			return errors.New("invalid text")
		}
	case "browser.key":
		var key string
		_ = json.Unmarshal(m["key"], &key)
		if !strings.Contains("|Enter|Tab|Escape|Backspace|Delete|ArrowUp|ArrowDown|ArrowLeft|ArrowRight|Home|End|PageUp|PageDown|", "|"+key+"|") {
			return errors.New("invalid key")
		}
		if v, ok := m["modifiers"]; ok {
			var mods []string
			if json.Unmarshal(v, &mods) != nil || len(mods) > 1 || (len(mods) == 1 && mods[0] != "shift") {
				return browserError("browser_action_unsupported", "not_executed")
			}
		}
	case "browser.scroll":
		if !browserNumber(m, "delta_x", 2000) || !browserNumber(m, "delta_y", 2000) || !browserInteger(m, "viewport_epoch", 1, 9007199254740991) {
			return errors.New("invalid scroll")
		}
	}
	return nil
}

type browserResult struct {
	V          int             `json:"v"`
	Connection string          `json:"connection_epoch"`
	Session    *string         `json:"session_epoch"`
	Target     *string         `json:"target_id"`
	Viewport   int64           `json:"viewport_epoch,omitempty"`
	Data       json.RawMessage `json:"data,omitempty"`
	Failure    *struct {
		Code      string `json:"code"`
		Certainty string `json:"certainty"`
	} `json:"failure,omitempty"`
}

func browserFailure(raw []byte, code string) error {
	var v browserResult
	if browserDecode(raw, &v) != nil || v.Failure == nil || v.Failure.Code != code || !browserFailureCodes[code] || (v.Failure.Certainty != "not_executed" && v.Failure.Certainty != "unknown") {
		return browserError("browser_control_lost", "unknown")
	}
	certainty := v.Failure.Certainty
	switch code {
	case "browser_not_enabled", "app_not_allowed", "bot_not_allowed", "browser_target_gone", "browser_session_changed", "browser_viewport_changed", "browser_login_required", "browser_server_unsupported", "browser_client_unsupported":
		if certainty != "not_executed" {
			return browserError("browser_control_lost", "unknown")
		}
	case "computer_lost":
		certainty = "unknown"
	}
	return browserError(code, certainty)
}
func validateBrowserResult(action string, args, raw []byte, resultErr string) error {
	var a struct {
		Connection string `json:"connection_epoch"`
		Session    string `json:"session_epoch"`
		Target     string `json:"target_id"`
		MaxChars   int    `json:"max_chars"`
	}
	_ = json.Unmarshal(args, &a)
	var v browserResult
	if len(raw) > maxComputerResult || browserDecode(raw, &v) != nil || v.V != 1 || v.Connection != a.Connection {
		return errors.New("invalid browser result")
	}
	if a.Session != "" && (v.Session == nil || *v.Session != a.Session) {
		return errors.New("browser session mismatch")
	}
	if a.Target != "" && (v.Target == nil || *v.Target != a.Target) {
		return errors.New("browser target mismatch")
	}
	if resultErr != "" {
		if v.Data != nil || v.Failure == nil || v.Failure.Code != resultErr || !browserFailureCodes[resultErr] || (v.Failure.Certainty != "unknown" && v.Failure.Certainty != "not_executed") {
			return errors.New("invalid browser failure")
		}
		return nil
	}
	if v.Failure != nil || v.Session == nil || v.Target == nil || !browserUUID(*v.Session) || !browserUUID(*v.Target) || !browserSafe(v.Viewport) || v.Viewport < 1 {
		return errors.New("invalid browser identity")
	}
	required := ""
	optional := ""
	switch action {
	case "browser.open":
		required = "url title targets"
	case "browser.navigate", "browser.click", "browser.back":
		required = "url title"
	case "browser.read":
		required = "url title text links targets truncated untrusted"
	case "browser.snapshot":
		required = "image_url width height css_width css_height shared_with_person"
	case "browser.type":
		required = "inserted"
	case "browser.key":
		required = "dispatched"
	case "browser.scroll":
		required = "scrolled"
	case "browser.close":
		required = "closed"
	default:
		return errors.New("unsupported browser result")
	}
	m, e := browserObject(v.Data, required, optional)
	if e != nil {
		return e
	}
	for _, k := range []string{"title", "url", "text"} {
		if _, ok := m[k]; ok {
			limit := 512
			if k == "url" {
				limit = 8192
			}
			if k == "text" {
				limit = maxComputerResult
			}
			if k == "url" {
				var page string
				_ = json.Unmarshal(m[k], &page)
				if !browserURL(page) {
					return errors.New("invalid page URL")
				}
			}
			if !browserString(m, k, limit, false) {
				return errors.New("invalid browser string")
			}
		}
	}
	if t, ok := m["targets"]; ok {
		var targets []struct {
			Target string `json:"target_id"`
			URL    string `json:"url"`
			Title  string `json:"title"`
		}
		if browserDecode(t, &targets) != nil || len(targets) > 8 || !browserObjectsHaveFields(t, "target_id url title") {
			return errors.New("invalid targets")
		}
		seen := map[string]bool{}
		for _, t := range targets {
			if !browserUUID(t.Target) || seen[t.Target] || !browserURL(t.URL) || len(t.Title) > 512 {
				return errors.New("invalid target")
			}
			seen[t.Target] = true
		}
		if !seen[*v.Target] {
			return errors.New("result target is missing from targets")
		}
	}
	if action == "browser.read" {
		var text string
		_ = json.Unmarshal(m["text"], &text)
		limit := a.MaxChars
		if limit == 0 {
			limit = 20000
		}
		if utf8.RuneCountInString(text) > limit || string(m["untrusted"]) != "true" || (string(m["truncated"]) != "true" && string(m["truncated"]) != "false") {
			return errors.New("invalid read")
		}
		var links []struct {
			Text string `json:"text"`
			URL  string `json:"url"`
		}
		if browserDecode(m["links"], &links) != nil || len(links) > 200 || !browserObjectsHaveFields(m["links"], "text url") {
			return errors.New("invalid links")
		}
		for _, l := range links {
			if len(l.Text) > 1024 || len(l.URL) > 8192 {
				return errors.New("invalid link")
			}
		}
	}
	for _, k := range []string{"inserted", "dispatched", "scrolled", "closed"} {
		if b, ok := m[k]; ok && string(b) != "true" {
			return errors.New("invalid success")
		}
	}
	if action == "browser.snapshot" {
		var u string
		_ = json.Unmarshal(m["image_url"], &u)
		const prefix = "data:image/jpeg;base64,"
		if !strings.HasPrefix(u, prefix) || string(m["shared_with_person"]) != "false" {
			return errors.New("invalid snapshot")
		}
		b, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(u, prefix))
		if e != nil || len(b) > 768<<10 {
			return errors.New("oversized snapshot")
		}
		config, e := jpeg.DecodeConfig(bytes.NewReader(b))
		if e != nil || config.Width < 1 || config.Height < 1 || config.Width > 1280 || config.Height > 800 || !browserInteger(m, "width", int64(config.Width), int64(config.Width)) || !browserInteger(m, "height", int64(config.Height), int64(config.Height)) || !browserNumber(m, "css_width", 1e7) || !browserNumber(m, "css_height", 1e7) {
			return errors.New("invalid snapshot dimensions")
		}
	}
	return nil
}

func browserObjectsHaveFields(raw []byte, fields string) bool {
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil {
		return false
	}
	for _, entry := range entries {
		if _, e := browserObject(entry, fields, ""); e != nil {
			return false
		}
	}
	return true
}
