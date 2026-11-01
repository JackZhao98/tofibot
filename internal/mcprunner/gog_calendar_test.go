package mcprunner

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func setGogAccount(t *testing.T, r *Runner, scope string, services ...string) {
	t.Helper()
	spec, _ := r.gogSpec("gog")
	data, _ := json.Marshal(gogAccount{Email: "person@gmail.com", Scope: scope, Services: services, ConnectedAt: time.Now()})
	if err := spec.writeInPlugin("gog-account.json", data); err != nil {
		t.Fatal(err)
	}
}

func callCalendarCreate(t *testing.T, r *Runner, args map[string]any) (string, bool) {
	t.Helper()
	p, err := r.get("gog")
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.callPlugin(context.Background(), p, &mcp.CallToolParams{Name: "calendar_create", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	text := ""
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	return text, result.IsError
}

func TestCalendarCreateIsListedForAuthorisedCalendarAsAWrite(t *testing.T) {
	account := gogAccount{Email: "a@b.c", Scope: "read-send", Services: []string{"gmail", "calendar"}}
	tools := gogExtraTools(account)
	if len(tools) != 1 || tools[0].Name != "calendar_create" || tools[0].Annotations == nil || tools[0].Annotations.ReadOnlyHint {
		t.Fatalf("tools=%+v", tools)
	}
	if len(gogExtraTools(gogAccount{Email: "a@b.c", Services: []string{"gmail", "drive"}})) != 0 {
		t.Fatal("calendar_create listed without Calendar")
	}
	if strings.Contains(strings.ToLower(tools[0].Description), "for example") || strings.Contains(tools[0].Description, "e.g.") {
		t.Fatalf("description holds an example: %s", tools[0].Description)
	}
	if !calendarWriteGranted(account) || calendarWriteGranted(gogAccount{Scope: "readonly", Services: []string{"calendar"}}) || calendarWriteGranted(gogAccount{Scope: "read-send", Services: []string{"gmail"}}) {
		t.Fatal("write grant derivation")
	}
}

func TestCalendarCreateNeedsWriteGrantAndSaysHow(t *testing.T) {
	r := newFileFakeGogRunner(t)
	base := map[string]any{"summary": "x", "from": "2026-11-02T10:00:00-08:00", "to": "2026-11-02T11:00:00-08:00"}
	if text, isErr := callCalendarCreate(t, r, base); !isErr || !strings.Contains(text, "No Google account") {
		t.Fatalf("not connected: %q", text)
	}
	setGogAccount(t, r, "readonly", "gmail", "calendar")
	text, isErr := callCalendarCreate(t, r, base)
	if !isErr || !strings.Contains(text, "Read and send") || !strings.Contains(text, "/settings/connections/local_gog") || !strings.Contains(text, "read-only") {
		t.Fatalf("read-only grant: %q", text)
	}
	setGogAccount(t, r, "read-send", "gmail")
	if text, isErr = callCalendarCreate(t, r, base); !isErr || !strings.Contains(text, "Calendar is not authorised") {
		t.Fatalf("no calendar: %q", text)
	}
}

func TestCalendarCreateRunsGogWithBoundedArguments(t *testing.T) {
	r := newFileFakeGogRunner(t)
	setGogAccount(t, r, "read-send", "gmail", "calendar")
	text, isErr := callCalendarCreate(t, r, map[string]any{
		"summary": "--readonly review", "from": "2026-11-02T10:00:00-08:00", "to": "2026-11-02T11:00:00-08:00",
		"attendees": []any{"guest@example.com"}, "with_meet": true, "timezone": "America/Los_Angeles",
	})
	if isErr {
		t.Fatalf("create failed: %q", text)
	}
	var out struct {
		Event struct {
			ID   string   `json:"id"`
			Argv []string `json:"argv"`
		} `json:"event"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil || out.Event.ID != "evt1" {
		t.Fatalf("result=%q err=%v", text, err)
	}
	joined := strings.Join(out.Event.Argv, " ")
	for _, want := range []string{"calendar create", "--summary=--readonly review", "--attendees=guest@example.com", "--with-meet", "--send-updates=none", "--timezone=America/Los_Angeles", "-- primary"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %s", want, joined)
		}
	}
	if strings.Contains(joined, " --readonly ") {
		t.Errorf("a create must not run read-only: %s", joined)
	}
	valid := func(extra map[string]any) map[string]any {
		in := map[string]any{"summary": "x", "from": "2026-11-02T10:00:00-08:00", "to": "2026-11-02T11:00:00-08:00"}
		for k, v := range extra {
			in[k] = v
		}
		return in
	}
	bad := []map[string]any{
		valid(map[string]any{"summary": ""}),
		valid(map[string]any{"from": "tomorrow"}),
		valid(map[string]any{"from": "2026-11-02T11:00:00-08:00", "to": "2026-11-02T10:00:00-08:00"}),
		valid(map[string]any{"attendees": []any{"a@b.c,d@e.f"}}),
		valid(map[string]any{"send_updates": "everyone"}),
		valid(map[string]any{"timezone": "Not/AZone"}),
	}
	for _, args := range bad {
		if text, isErr := callCalendarCreate(t, r, args); !isErr || !strings.Contains(text, "Nothing was created") {
			t.Errorf("%v accepted: %q", args, text)
		}
	}
	allDay, isErr := callCalendarCreate(t, r, map[string]any{"summary": "off", "from": "2026-11-02", "to": "2026-11-02", "all_day": true})
	if isErr || !strings.Contains(allDay, "--all-day") {
		t.Errorf("all day: %q", allDay)
	}
	// Google's own refusal becomes the scope instruction, never its text.
	text, isErr = callCalendarCreate(t, r, valid(map[string]any{"summary": "fail"}))
	if !isErr || !strings.Contains(text, "Read and send") || strings.Contains(text, "googleapi") {
		t.Errorf("scope refusal: %q", text)
	}
}
