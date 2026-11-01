package mcprunner

import (
	"context"
	"encoding/json"
	"errors"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The tools below are TOFI's own additions to the gog MCP child, which has no calendar write. They run gog's
// `calendar create` command. They are listed beside the child's tools, annotated as writes, so the connection's tool
// permission and the approval review treat them as any other write.
const gogCalendarCreateTool = "calendar_create"

const (
	maxCalendarAttendees = 50
	calendarFieldLimit   = 8 << 10
)

// calendarWriteGranted reports whether the connected account holds Calendar with write access. gog requests the
// read-only Calendar scope whenever the account was authorised in read-only mode (`--readonly`), and the full scope
// only together with Gmail "read and send" (gogAuthAddFlags), so the recorded Gmail scope tells which one was granted.
func calendarWriteGranted(account gogAccount) bool {
	if account.Scope != "read-send" {
		return false
	}
	for _, service := range authorisedGogServices(account.Services) {
		if service == "calendar" {
			return true
		}
	}
	return false
}

const calendarWriteMissing = "Creating a calendar event needs full Calendar access, and the connected Google account holds Calendar read-only. Tell the person to open Settings > Connections (/settings/connections/local_gog), choose the Gmail access level Read and send, and approve the Google prompt: Google keeps what is already granted and adds full Calendar access."

func calendarCreateTool() mcp.Tool {
	no := false
	yes := true
	str := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	return mcp.Tool{
		Name:        gogCalendarCreateTool,
		Title:       "Create a Google Calendar event",
		Description: "Create one event on the connected Google Calendar. summary, from and to are required. from and to are RFC 3339 timestamps with an offset, or dates when all_day is true (to is the last day, inclusive). attendees receive an invitation only when send_updates is externalOnly or all; with the default none the guests are added silently. Returns the created event with its id and link. This changes the person's calendar, so do it only when the person asked for it. Needs full Calendar access on the connected Google account; a refusal names what to ask the person to do in Settings.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &no, IdempotentHint: false, OpenWorldHint: &yes},
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"summary", "from", "to"},
			"properties": map[string]any{
				"summary":      str("Event title"),
				"from":         str("Start: RFC 3339 timestamp, or a date when all_day is true"),
				"to":           str("End: RFC 3339 timestamp, or the last date when all_day is true"),
				"all_day":      map[string]any{"type": "boolean", "description": "True for an all-day event"},
				"timezone":     str("IANA time zone name applied to from and to"),
				"description":  str("Event description"),
				"location":     str("Event location"),
				"attendees":    map[string]any{"type": "array", "maxItems": maxCalendarAttendees, "items": map[string]any{"type": "string"}, "description": "Guest email addresses"},
				"with_meet":    map[string]any{"type": "boolean", "description": "True to attach a Google Meet link"},
				"send_updates": map[string]any{"type": "string", "enum": []string{"none", "externalOnly", "all"}, "description": "Who Google notifies; defaults to none"},
				"calendar_id":  str("Calendar id; the primary calendar when empty"),
			},
		},
	}
}

// gogExtraTools are the tools TOFI adds for the services the account has authorised.
func gogExtraTools(account gogAccount) []mcp.Tool {
	var out []mcp.Tool
	for _, service := range authorisedGogServices(account.Services) {
		if service == "calendar" {
			out = append(out, calendarCreateTool())
		}
	}
	return out
}

func isGogExtraTool(name string) bool { return name == gogCalendarCreateTool }

func gogToolError(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

type calendarCreateInput struct {
	Summary     string   `json:"summary"`
	From        string   `json:"from"`
	To          string   `json:"to"`
	AllDay      bool     `json:"all_day"`
	Timezone    string   `json:"timezone"`
	Description string   `json:"description"`
	Location    string   `json:"location"`
	Attendees   []string `json:"attendees"`
	WithMeet    bool     `json:"with_meet"`
	SendUpdates string   `json:"send_updates"`
	CalendarID  string   `json:"calendar_id"`
}

// calendarArgs validates the input and builds gog's argument list. Every value is passed as --flag=value so that a
// value can never be read as an option.
func calendarArgs(account gogAccount, in calendarCreateInput) ([]string, error) {
	in.Summary = strings.TrimSpace(in.Summary)
	for _, text := range []string{in.Summary, in.Description, in.Location} {
		if len(text) > calendarFieldLimit || !utf8.ValidString(text) {
			return nil, errors.New("summary, description and location must be valid text of at most 8 KB")
		}
	}
	if in.Summary == "" || strings.ContainsAny(in.Summary, "\r\n") || strings.ContainsAny(in.Location, "\r\n") {
		return nil, errors.New("summary is required and summary and location are single lines")
	}
	layout := time.RFC3339
	if in.AllDay {
		layout = "2006-01-02"
	}
	from, err := time.Parse(layout, strings.TrimSpace(in.From))
	if err != nil {
		return nil, errors.New("from must be an RFC 3339 timestamp with an offset, or a YYYY-MM-DD date when all_day is true")
	}
	to, err := time.Parse(layout, strings.TrimSpace(in.To))
	if err != nil {
		return nil, errors.New("to must be an RFC 3339 timestamp with an offset, or a YYYY-MM-DD date when all_day is true")
	}
	if in.AllDay && to.Before(from) || !in.AllDay && !to.After(from) {
		return nil, errors.New("to must come after from")
	}
	args := []string{"--json", "--no-input", "--account", account.Email, "calendar", "create", "--summary=" + in.Summary, "--from=" + strings.TrimSpace(in.From), "--to=" + strings.TrimSpace(in.To)}
	if in.AllDay {
		args = append(args, "--all-day")
	}
	if tz := strings.TrimSpace(in.Timezone); tz != "" {
		if _, err := time.LoadLocation(tz); err != nil || strings.ContainsAny(tz, " \t\r\n") {
			return nil, errors.New("timezone must be an IANA time zone name")
		}
		args = append(args, "--timezone="+tz)
	}
	if in.Description != "" {
		args = append(args, "--description="+in.Description)
	}
	if in.Location != "" {
		args = append(args, "--location="+in.Location)
	}
	if len(in.Attendees) > maxCalendarAttendees {
		return nil, errors.New("too many attendees")
	}
	if len(in.Attendees) > 0 {
		addresses := make([]string, 0, len(in.Attendees))
		for _, raw := range in.Attendees {
			raw = strings.TrimSpace(raw)
			parsed, err := mail.ParseAddress(raw)
			if err != nil || parsed.Address != raw || strings.ContainsAny(raw, ",;\r\n") {
				return nil, errors.New("attendees are literal email addresses")
			}
			addresses = append(addresses, raw)
		}
		args = append(args, "--attendees="+strings.Join(addresses, ","))
	}
	if in.WithMeet {
		args = append(args, "--with-meet")
	}
	switch in.SendUpdates {
	case "":
		in.SendUpdates = "none"
	case "none", "externalOnly", "all":
	default:
		return nil, errors.New("send_updates is none, externalOnly or all")
	}
	args = append(args, "--send-updates="+in.SendUpdates)
	calendarID := strings.TrimSpace(in.CalendarID)
	if calendarID == "" {
		calendarID = "primary"
	}
	if len(calendarID) > 254 || strings.ContainsAny(calendarID, " \t\r\n") {
		return nil, errors.New("calendar_id is a calendar id or empty")
	}
	return append(args, "--", calendarID), nil
}

// callGogExtra runs one of TOFI's own gog tools.
func (r *Runner) callGogExtra(ctx context.Context, p *plugin, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	account, err := r.GogStatus(p.spec.ID)
	if err != nil || account.Email == "" {
		return gogToolError("No Google account is connected. Tell the person to connect Google in Settings > Connections (/settings/connections/local_gog)."), nil
	}
	if r.GogNeedsReauth(p.spec.ID) {
		return gogToolError(GogReauthMessage), nil
	}
	hasCalendar := false
	for _, service := range authorisedGogServices(account.Services) {
		hasCalendar = hasCalendar || service == "calendar"
	}
	if !hasCalendar {
		return gogToolError("Calendar is not authorised on the connected Google account. Tell the person to add Calendar in Settings > Connections (/settings/connections/local_gog); Google keeps what is already granted."), nil
	}
	if !calendarWriteGranted(account) {
		return gogToolError(calendarWriteMissing), nil
	}
	raw, _ := json.Marshal(params.Arguments)
	var in calendarCreateInput
	if json.Unmarshal(raw, &in) != nil {
		return gogToolError("Invalid arguments."), nil
	}
	args, err := calendarArgs(account, in)
	if err != nil {
		return gogToolError("Invalid arguments: " + err.Error() + ". Nothing was created."), nil
	}
	p.gogMu.Lock()
	defer p.gogMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	output, err := runGog(ctx, p.spec, nil, args...)
	if err != nil {
		var failed *gogCommandError
		if disabled := classifyGogFailure(err, "calendar-json.googleapis.com"); disabled != nil {
			return gogToolError("The Google Calendar API is not enabled for the project. Tell the person to enable it from Settings > Connections (/settings/connections/local_gog)."), nil
		}
		if errors.As(err, &failed) {
			lower := strings.ToLower(failed.output)
			switch {
			case IsGogGrantFailure(failed.output):
				r.GogMarkReauth(p.spec.ID)
				return gogToolError(GogReauthMessage), nil
			case strings.Contains(lower, "insufficient") && (strings.Contains(lower, "scope") || strings.Contains(lower, "permission")):
				return gogToolError(calendarWriteMissing), nil
			case strings.Contains(lower, "timed out") || ctx.Err() != nil:
				return gogToolError("Tool result unknown; the event may have been created. Check the calendar before creating it again."), nil
			}
		}
		if ctx.Err() != nil {
			return gogToolError("Tool result unknown; the event may have been created. Check the calendar before creating it again."), nil
		}
		return gogToolError("Google Calendar reported an error. Nothing was confirmed, so the event may or may not exist; check the calendar before trying again."), nil
	}
	if !json.Valid(output) {
		return gogToolError("Google Calendar returned an unrecognised result; check the calendar before creating the event again."), nil
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(output)}}, StructuredContent: nil}, nil
}
