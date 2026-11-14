package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

type environmentFeature struct {
	Key    string `json:"key"`
	Status string `json:"status"`
	Code   string `json:"code,omitempty"`
}

type environmentRun struct {
	mu     sync.Mutex
	origin string
	route  string
	denied map[string]bool
}

func (s *Store) validMessageOrigin(id string) string {
	if id == "" || utf8.RuneCountInString(id) > 128 {
		return ""
	}
	row, err := s.computerRow(id)
	if err != nil || row.Kind != computer.RowKindPersonal {
		return ""
	}
	return id
}

func (s *Server) environmentRun(r Run) *environmentRun {
	if r.ID != "" {
		if v, ok := s.environmentRuns.Load(r.ID); ok {
			return v.(*environmentRun)
		}
	}
	origin := ""
	if s.store != nil && r.Kind != runKindSchedule && r.scheduleTask == nil {
		trigger, parent := r.TriggerMessageID, r.ParentRunID
		seen := map[string]bool{}
		for parent != "" && !seen[parent] {
			seen[parent] = true
			var kind string
			if s.store.db.QueryRow(`SELECT COALESCE(parent_run_id,''),COALESCE(trigger_message_id,''),kind FROM runs WHERE id=?`, parent).Scan(&parent, &trigger, &kind) != nil {
				trigger = ""
				break
			}
			if kind == runKindSchedule {
				trigger = ""
				break
			}
		}
		_ = s.store.db.QueryRow(`SELECT origin_computer_id FROM messages WHERE id=?`, trigger).Scan(&origin)
		origin = s.store.validMessageOrigin(origin)
	}
	v := &environmentRun{origin: origin, denied: map[string]bool{}}
	if r.ID == "" {
		return v
	}
	actual, _ := s.environmentRuns.LoadOrStore(r.ID, v)
	return actual.(*environmentRun)
}
func (s *Server) runOrigin(r Run) string {
	if r.Kind == runKindSchedule || r.scheduleTask != nil {
		return ""
	}
	return s.environmentRun(r).origin
}

func (s *Server) cachedEnvironmentInfo() (computer.Info, error) {
	info, err, _ := s.cachedMicroVMInfo(context.Background())
	return info, err
}

func environmentFeatures(row ComputerRow, online bool, hub *browserHub) []environmentFeature {
	has := func(cap string) bool {
		for _, c := range row.Capabilities {
			if c == cap {
				return true
			}
		}
		return false
	}
	out := []environmentFeature{}
	for _, key := range []string{"browser", "desktop", "files", "shell"} {
		advertised := row.Kind != computer.RowKindPersonal
		if row.Kind == computer.RowKindPersonal {
			switch key {
			case "browser":
				advertised = has("browser.open")
			case "desktop":
				advertised = has("desktop.click") && (has("screen.capture") || has("desktop.capture"))
			case "files":
				advertised = has("files.read")
			case "shell":
				advertised = has("sandbox.exec")
			}
		}
		f := environmentFeature{Key: key, Status: "off"}
		if advertised {
			f.Status = "connected"
			if !online {
				f.Status = "disconnected"
				f.Code = "computer_offline"
			} else if key == "browser" && row.Kind == computer.RowKindPersonal {
				f.Code = "browser_transport_unavailable"
				if hub != nil {
					hub.mu.Lock()
					if !hub.supported {
						f.Code = "browser_client_unsupported"
					} else if hub.upload != nil && hub.state != nil {
						if !hub.state.Settings.Enabled {
							f.Code = "browser_not_enabled"
						} else {
							f.Code = ""
						}
					}
					hub.mu.Unlock()
				}
				if f.Code != "" {
					f.Status = "disconnected"
				}
			}
		}
		out = append(out, f)
	}
	return out
}
func featureConnected(c computerSummary, key string) bool {
	for _, f := range c.Features {
		if f.Key == key {
			return f.Status == "connected"
		}
	}
	return false
}

func (s *Server) environmentOrder(r Run) []computerSummary {
	var bot *computerSummary
	var macs []computerSummary
	listed := map[string]bool{}
	if s.store != nil {
		rows, _ := s.store.listComputerRows()
		for _, row := range rows {
			if row.Kind != computer.RowKindPersonal {
				continue
			}
			c := computerSummary{ID: row.ID, Name: row.Name, Kind: row.Kind, Online: row.Status == computerRowAttached && computerRecentlySeen(row.LastSeen), Capabilities: row.Capabilities}
			if row.Region != nil {
				c.Region = *row.Region
			}
			if slot := s.slotFor(row.ID); slot != nil {
				c.InUse = slot.holder() != ""
				c.Online = c.Online && slot.currentState() == slotAttached
			}
			c.Features = environmentFeatures(row, c.Online, s.browserHub(row.ID))
			macs = append(macs, c)
			listed[row.ID] = true
		}
	}
	// A personal computer can be attached before its row exists (registration): list it with what the slot knows.
	for _, slot := range s.attachedSlots() {
		if slot.rowKind != computer.RowKindPersonal || listed[slot.id] {
			continue
		}
		c := computerSummary{ID: slot.id, Name: slot.id, Kind: slot.rowKind, Region: slot.region, Online: slot.currentState() == slotAttached, InUse: slot.holder() != "", Capabilities: []string{}}
		c.Features = environmentFeatures(ComputerRow{ID: slot.id, Kind: slot.rowKind}, c.Online, s.browserHub(slot.id))
		macs = append(macs, c)
	}
	if s.computer() != nil {
		c := computerSummary{ID: microVMComputerID, Name: "Bot computer", Kind: computer.RowKindVM, Default: true, Capabilities: s.computerCapabilities().Capabilities, Online: true}
		// The VM's state matters only for ranking it against a Mac; with no Mac it is the only environment, and building a
		// tool list must not call the computer.
		if len(macs) > 0 {
			c.Online = false
			if info, err := s.cachedEnvironmentInfo(); err == nil {
				c.State = info.State
				c.Online = info.State == "ready" || info.State == "asleep" || info.State == "resuming"
			}
		}
		if slot := s.slotFor(c.ID); slot != nil {
			c.InUse = slot.holder() != ""
			c.Region = slot.region
			c.Online = c.Online && slot.currentState() == slotAttached
		}
		c.Features = environmentFeatures(ComputerRow{Kind: computer.RowKindVM}, c.Online, nil)
		bot = &c
	}
	sort.SliceStable(macs, func(i, j int) bool { return macs[i].Online && !macs[j].Online })
	origin := s.runOrigin(r)
	web, _ := s.autoStored("environment_order_web")
	app, _ := s.autoStored("environment_order_app")
	var out []computerSummary
	addBot := func() {
		if bot != nil {
			out = append(out, *bot)
		}
	}
	if origin == "" {
		if web != "mac" {
			addBot()
		}
		out = append(out, macs...)
		if web == "mac" {
			addBot()
		}
		return out
	}
	if app == "bot_computer" {
		addBot()
	}
	for _, c := range macs {
		if c.ID == origin {
			out = append(out, c)
		}
	}
	if app != "bot_computer" {
		addBot()
	}
	for _, c := range macs {
		if c.ID != origin {
			out = append(out, c)
		}
	}
	return out
}

type browserCandidate struct {
	Computer computerSummary
	Tool     string
}

func (s *Server) browserOrder(r Run) []browserCandidate {
	state := s.environmentRun(r)
	state.mu.Lock()
	denied := map[string]bool{}
	for id, v := range state.denied {
		denied[id] = v
	}
	state.mu.Unlock()
	var out []browserCandidate
	for _, c := range s.environmentOrder(r) {
		if denied[c.ID] {
			continue
		}
		if featureConnected(c, "browser") {
			out = append(out, browserCandidate{c, "computer_browser"})
		}
		if c.Kind == computer.RowKindPersonal && featureConnected(c, "desktop") {
			out = append(out, browserCandidate{c, "computer_desktop"})
		}
	}
	return out
}
func (s *Server) browserBotFirst(r Run) bool {
	order := s.browserOrder(r)
	return len(order) > 0 && order[0].Computer.Kind != computer.RowKindPersonal
}
func (s *Server) resolveBrowserComputer(r Run, explicit string) string {
	if explicit != "" {
		if s.isVMRoute(explicit) {
			return microVMComputerID
		}
		return explicit
	}
	state := s.environmentRun(r)
	state.mu.Lock()
	route := state.route
	state.mu.Unlock()
	if route != "" {
		return route
	}
	for _, c := range s.browserOrder(r) {
		if c.Tool == "computer_browser" {
			return c.Computer.ID
		}
	}
	return ""
}
func (s *Server) setBrowserRoute(r Run, id string) {
	state := s.environmentRun(r)
	state.mu.Lock()
	state.route = id
	state.mu.Unlock()
}

type servedBy struct {
	Environment string `json:"environment"`
	ID          string `json:"environment_id"`
	Kind        string `json:"kind"`
	Browser     string `json:"browser,omitempty"`
}

func (s *Server) servedBy(id string, browser bool) servedBy {
	v := servedBy{Environment: "Bot computer", ID: id, Kind: "bot_computer"}
	if id != "" && !s.isVMRoute(id) {
		v.Kind = "mac"
		v.Environment = id
		if row, e := s.store.computerRow(id); e == nil {
			v.Environment = row.Name
		}
		if browser {
			v.Browser = "built_in"
		}
	} else if browser {
		v.Browser = "chrome"
	}
	return v
}
func servedLabel(v servedBy) string {
	label := "served_by: " + v.Environment
	if v.Browser != "" {
		b := v.Browser
		if b == "chrome" {
			b = "Chrome"
		}
		label += ", " + b
	}
	return label
}
func (s *Server) identifyResult(id string, browser bool, out string, err error, extra map[string]any) (string, error) {
	v := s.servedBy(id, browser)
	if extra == nil {
		extra = map[string]any{}
	}
	extra["served_by"] = v
	if err != nil {
		if o, ok := tooloutcome.FromError(err); ok {
			o.ServedBy = v
			o.Next = extra["next"]
			o.Failover = extra["failover"]
			o.Features = extra["features"]
			o.Message = servedLabel(v) + "\n" + o.Message
			return out, o.Err()
		}
		return out, fmt.Errorf("%s\n%w", servedLabel(v), err)
	}
	var m map[string]any
	if json.Unmarshal([]byte(out), &m) == nil && m != nil {
		for k, v := range extra {
			m[k] = v
		}
		b, _ := json.Marshal(m)
		return string(b), nil
	}
	return servedLabel(v) + "\n" + out, nil
}
func (s *Server) environmentTool(r Run, t Tool, browser bool) Tool {
	execute := t.Execute
	t.Execute = func(ctx context.Context, raw json.RawMessage) (string, error) {
		id := microVMComputerID
		if !browser {
			raw = s.desktopRouteInput(r, t.Name, raw)
			id = s.desktopToolComputer(t.Name, raw)
		}
		out, err := execute(ctx, raw)
		if browser {
			o, typed := tooloutcome.FromError(err)
			if err == nil || (typed && o.Certainty == "unknown") {
				s.setBrowserRoute(r, id)
			}
		}
		return s.identifyResult(id, browser, out, err, nil)
	}
	if !browser {
		identity, resolve := t.Identity, t.ResolveIdentity
		if identity != nil {
			t.Identity = func(raw json.RawMessage) tooloutcome.Identity { return identity(s.desktopRouteInput(r, t.Name, raw)) }
		}
		if resolve != nil {
			t.ResolveIdentity = func(ctx context.Context, raw json.RawMessage) (tooloutcome.Identity, error) {
				return resolve(ctx, s.desktopRouteInput(r, t.Name, raw))
			}
		}
	}
	return t
}
func (s *Server) desktopRouteInput(r Run, name string, raw json.RawMessage) json.RawMessage {
	if name != "computer_desktop" {
		return raw
	}
	var in map[string]json.RawMessage
	if json.Unmarshal(raw, &in) != nil || in == nil {
		return raw
	}
	var id, action string
	_ = json.Unmarshal(in["computer_id"], &id)
	_ = json.Unmarshal(in["action"], &action)
	action = strings.TrimPrefix(action, "desktop.")
	if id != "" {
		return raw
	}
	id = microVMComputerID
	if action != "start" && action != "stop" && action != "trace" {
		id = desktopNeedsComputerID
		for _, c := range s.environmentOrder(r) {
			if featureConnected(c, "desktop") {
				id = c.ID
				break
			}
		}
	}
	in["computer_id"], _ = json.Marshal(id)
	b, _ := json.Marshal(in)
	return b
}

func (s *Server) environmentsRunContext(r Run) string {
	if !s.hasOtherComputers() && s.runOrigin(r) == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("Environments, in the order of this run:\n")
	for i, c := range s.environmentOrder(r) {
		label := c.Name
		state := c.State
		if c.Kind == computer.RowKindPersonal {
			label += " (Mac, id=" + c.ID + ")"
			state = "connected"
			if !c.Online {
				state = "disconnected"
			}
		} else {
			label += " (id=" + c.ID + ")"
		}
		fmt.Fprintf(&b, "%d. %s: %s.", i+1, label, state)
		for _, f := range c.Features {
			name := map[string]string{"browser": "Browser", "desktop": "Desktop", "files": "Files", "shell": "Shell"}[f.Key]
			if c.Kind == computer.RowKindPersonal {
				name = map[string]string{"browser": "Built-in browser", "desktop": "Desktop control", "files": "Files", "shell": "Commands"}[f.Key]
			}
			fmt.Fprintf(&b, " %s: %s.", name, f.Status)
		}
		b.WriteByte('\n')
	}
	origin := s.runOrigin(r)
	if origin == "" {
		b.WriteString("This message came from the Web.\n")
	} else {
		fmt.Fprintf(&b, "This message came from the Tofi app on %s.\n", s.servedBy(origin, false).Environment)
	}
	b.WriteString("Browser order: ")
	var names []string
	for _, c := range s.browserOrder(r) {
		name := "Chrome on the Bot computer"
		if c.Computer.Kind == computer.RowKindPersonal {
			name = "built-in browser on " + c.Computer.Name
			if c.Tool == "computer_desktop" {
				name = "Safari or Chrome on " + c.Computer.Name + " through computer_desktop"
			}
		}
		names = append(names, name)
	}
	b.WriteString(strings.Join(names, "; "))
	b.WriteString(".\nA browser or desktop call without computer_id uses the first one that is connected. When a browser cannot do the job, or a site needs a sign-in that the browser does not have, go to the next one and do not ask. An instruction from the person overrides the order. Name the environment and browser from served_by when you report.")
	return b.String()
}

// entryBrowserAction gives an opening request the action of the browser that serves it. An opening request has a url and no
// session or target. A Mac's built-in browser opens it in a new page (browser.open: it has no current page to navigate);
// the Bot computer navigates its current tab (browser.navigate). The model then need not know each browser's verbs.
func (s *Server) entryBrowserAction(id string, in map[string]json.RawMessage) {
	_, hasURL := in["url"]
	_, hasSession := in["session_epoch"]
	_, hasTarget := in["target_id"]
	var action string
	_ = json.Unmarshal(in["action"], &action)
	if bare := strings.TrimPrefix(action, "browser."); !hasURL || hasSession || hasTarget || (bare != "navigate" && bare != "open") {
		return
	}
	want := "browser.navigate"
	if !s.isVMRoute(id) {
		want = "browser.open"
	}
	in["action"], _ = json.Marshal(want)
}

var automaticBrowserCodes = map[string]bool{
	"computer_offline": true, "computer_lost": true, "device_revoked": true, "browser_transport_unavailable": true, "browser_client_unsupported": true, "browser_server_unsupported": true, "browser_not_enabled": true, "bot_not_allowed": true, "app_not_allowed": true, "computer_not_configured": true,
}

func (s *Server) routeBrowserCall(ctx context.Context, r Run, raw json.RawMessage, dispatch func(context.Context, json.RawMessage) (string, error)) (string, error) {
	var in map[string]json.RawMessage
	if browserDecode(raw, &in) != nil || in == nil || !browserString(in, "action", 64, true) || !browserString(in, "computer_id", 128, false) {
		out, err := dispatch(ctx, raw)
		return s.identifyResult(s.resolveBrowserComputer(r, ""), true, out, err, nil)
	}
	var explicit string
	_ = json.Unmarshal(in["computer_id"], &explicit)
	id := s.resolveBrowserComputer(r, explicit)
	if id == "" {
		environments := s.environmentOrder(r)
		target := ""
		if len(environments) > 0 {
			target = environments[0].ID
		}
		return s.identifyResult(target, true, "", browserError("browser_unavailable", "not_executed"), map[string]any{"features": environments})
	}
	if explicit == "" {
		state := s.environmentRun(r)
		state.mu.Lock()
		route := state.route
		state.mu.Unlock()
		if route == "" {
			order := s.environmentOrder(r)
			if len(order) > 0 && order[0].Kind != computer.RowKindPersonal && (order[0].State == "failed" || order[0].State == "stopped") {
				id = order[0].ID
			}
		}
	}
	if explicit != "" {
		s.setBrowserRoute(r, id)
	}
	var first *servedBy
	firstCode := ""
	visited := map[string]bool{}
	for {
		visited[id] = true
		in["computer_id"], _ = json.Marshal(id)
		s.entryBrowserAction(id, in)
		forward, _ := json.Marshal(in)
		var out string
		var err error
		if s.isVMRoute(id) {
			for _, c := range s.environmentOrder(r) {
				if c.ID == id && (c.State == "failed" || c.State == "stopped") {
					o := tooloutcome.New(tooloutcome.Denied, "computer_not_configured", "not_executed", "Bot computer: "+c.State, "explain_blocker")
					o.Detail = &tooloutcome.Detail{Code: c.State}
					err = o.Err()
					break
				}
			}
		}
		if err == nil {
			out, err = dispatch(ctx, forward)
		}
		extra := map[string]any{}
		if first != nil {
			extra["failover"] = map[string]any{"from": first, "code": firstCode}
		}
		if err == nil {
			s.setBrowserRoute(r, id)
			return s.identifyResult(id, true, out, nil, extra)
		}
		o, typed := tooloutcome.FromError(err)
		if !typed || o.Certainty != "not_executed" || o.Code == "user_takeover" || o.Code == "user_stop" {
			if typed && o.Certainty == "unknown" {
				s.setBrowserRoute(r, id)
			}
			return s.identifyResult(id, true, out, err, extra)
		}
		if o.Code == "bot_not_allowed" {
			state := s.environmentRun(r)
			state.mu.Lock()
			state.denied[id] = true
			if state.route == id {
				state.route = ""
			}
			state.mu.Unlock()
		}
		if !automaticBrowserCodes[o.Code] && o.Code != "browser_login_required" && o.Code != "browser_action_unsupported" {
			return s.identifyResult(id, true, out, err, extra)
		}
		var next *browserCandidate
		order := s.browserOrder(r)
		// Preserve the position even when denial removed the failed environment.
		position := -1
		for i, c := range order {
			if c.Computer.ID == id && c.Tool == "computer_browser" {
				position = i
				break
			}
		}
		if position < 0 {
			envs := s.environmentOrder(r)
			rank := map[string]int{}
			for i, c := range envs {
				rank[c.ID] = i
			}
			for i, c := range order {
				if (rank[c.Computer.ID] > rank[id] && !visited[c.Computer.ID]) || (c.Computer.ID == id && c.Tool == "computer_desktop") {
					next = &order[i]
					break
				}
			}
		} else {
			for i := position + 1; i < len(order); i++ {
				c := order[i]
				if !visited[c.Computer.ID] || c.Tool == "computer_desktop" {
					next = &order[i]
					break
				}
			}
		}
		if next == nil {
			o.Message += " Tell the person which sign-in or feature is necessary."
			return s.identifyResult(id, true, out, o.Err(), extra)
		}
		extra["next"] = map[string]any{"environment": next.Computer.Name, "environment_id": next.Computer.ID, "tool": next.Tool}
		enabled, _ := s.autoStored("environment_failover")
		_, hasURL := in["url"]
		_, hasSession := in["session_epoch"]
		_, hasTarget := in["target_id"]
		if explicit != "" || enabled == "false" || !hasURL || hasSession || hasTarget || !automaticBrowserCodes[o.Code] || next.Tool != "computer_browser" {
			return s.identifyResult(id, true, out, err, extra)
		}
		if first == nil {
			v := s.servedBy(id, true)
			first = &v
			firstCode = o.Code
		}
		id = next.Computer.ID
		action := "browser.navigate"
		if next.Computer.Kind == computer.RowKindPersonal {
			action = "browser.open"
		}
		effect := in["effect"]
		in = map[string]json.RawMessage{"url": in["url"]}
		if effect != nil {
			in["effect"] = effect
		}
		in["action"], _ = json.Marshal(action)
	}
}
