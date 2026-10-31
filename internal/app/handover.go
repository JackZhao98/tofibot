package app

// Bot-initiated handover cards ride the same durable questions rendezvous as
// every other human question (see questions.go): the run suspends, survives a
// restart, expires, refuses hidden conversations through requireHumanPresent,
// and the card renders through ui/src/QuestionCard.tsx. Two card types:
//
//	takeover   - the person acts on the Bot's shared computer screen.
//	connection - the person connects an OAuth service from chat.
//
// The answer of either is only a short enumerated word. Nothing the person
// types, copies or sees on the remote screen is ever part of an answer, a
// result, an event or a table: the input path (computer_control.go) goes from
// the browser to the guest and never touches the model-facing run.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const (
	takeoverDone   = "done"
	takeoverCannot = "cannot"

	connectionConnected = "connected"
	connectionFailed    = "failed"
	connectionDeclined  = "declined"

	// The guest collects a desktop that nobody touched for 15 minutes; a
	// takeover card must not outlive the desktop it points at.
	defaultTakeoverWait = 10 * time.Minute
	maxTakeoverWait     = 14 * time.Minute
)

func isHandoverType(t string) bool { return t == questionTakeover || t == questionConnection }

func handoverAnswerAllowed(qtype, v string) bool {
	switch qtype {
	case questionTakeover:
		return v == takeoverDone || v == takeoverCannot
	case questionConnection:
		return v == connectionConnected || v == connectionFailed || v == connectionDeclined
	}
	return false
}

func normalizeHandover(x askQuestionInput) (askQuestionInput, error) {
	if utf8.RuneCountInString(x.Question) > 500 {
		return x, errors.New("question must be at most 500 characters")
	}
	if len(x.Options) > 0 || len(x.Fields) > 0 || x.Approval != nil || x.MinSelections != 0 || x.MaxSelections != 0 {
		return x, errors.New("a handover card takes no options, fields or approval details")
	}
	x.Options = nil
	switch x.Type {
	case questionTakeover:
		site := strings.TrimSpace(x.SourceURL)
		if site != "" && !strings.Contains(site, "://") {
			site = "https://" + site
		}
		u, err := formSourceURL(site)
		if err != nil {
			return x, errors.New("site must be the website's address")
		}
		// Only the origin is stored: paths and queries can carry page state.
		x.SourceURL, x.Target = formOrigin(u), ""
	case questionConnection:
		x.Target = strings.TrimSpace(x.Target)
		if x.Target == "" || utf8.RuneCountInString(x.Target) > 128 {
			return x, errors.New("service is required")
		}
		x.SourceURL = ""
	}
	return x, nil
}

// handoverResult is the only model-visible form of a handover card. It states
// the outcome in words and carries no content from the person.
func handoverResult(q Question) string {
	out := map[string]any{}
	answer := ""
	if q.Status == questionAnswered {
		_ = json.Unmarshal(q.Answer, &answer)
	}
	switch {
	case q.Type == questionTakeover && answer == takeoverDone:
		out["status"], out["completed"] = "handed_back", true
		out["message"] = "The person finished their part on the Bot computer and returned control. Nothing they typed, copied or saw is shared with you. Reinspect the screen and continue from its actual state."
	case q.Type == questionTakeover && answer == takeoverCannot:
		out["status"], out["completed"] = "declined", false
		out["message"] = "The person could not or chose not to do this step. Do not request the same handover again; report the blocker."
	case q.Type == questionConnection && answer == connectionConnected:
		out["status"], out["connected"] = "connected", true
		out["message"] = "The person connected the service. Its tools may only appear from your next turn; check what the service now offers before relying on it."
	case q.Type == questionConnection && answer == connectionFailed:
		out["status"], out["connected"] = "failed", false
		out["message"] = "The connection attempt failed. Do not retry on your own; report the blocker and let the person start again."
	case q.Type == questionConnection && answer == connectionDeclined:
		out["status"], out["connected"] = "declined", false
		out["message"] = "The person chose not to connect this service. Continue without it or report the blocker."
	case q.Status == questionExpired:
		out["status"] = "expired"
		out["message"] = "The person did not respond within the window. Do not repeat the request; report that this step is waiting on them."
	default:
		out["status"] = questionCancelled
		out["message"] = "The request was withdrawn without an answer. Do not repeat it; report that this step is waiting on the person."
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func (s *Server) takeoverTool(c Conversation, r Run) Tool {
	return Tool{Name: "request_takeover", Description: "Hand the Bot computer's screen to the person for one step that only they can perform or that must stay private from you, because the website requires their own identity, possession or presence. Say what is needed in question and name the website in site. The run suspends while they act; waiting does not use active time and survives a service restart. The result says only whether they finished (status handed_back), could not or would not (declined), or the request was cancelled or expired. You never see what they typed, copied or saw, so after the handover reinspect the page and continue from its actual state. Leave the browser on the page where their step begins. Do not use it for anything you can do yourself, and never ask for secrets in chat instead. The response window is at most 14 minutes because the Bot computer is collected after longer inactivity.", Parameters: objectSchema(map[string]any{
		"question":           map[string]any{"type": "string", "description": "What the person needs to do, in one or two plain sentences"},
		"site":               map[string]any{"type": "string", "description": "The website where the step happens"},
		"expires_in_seconds": map[string]any{"type": "integer", "minimum": 0, "maximum": int(maxTakeoverWait / time.Second), "description": "Response window in seconds; omitted or 0 means 10 minutes, at most 14 minutes."},
	}, []string{"question", "site"}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := requireHumanPresent(c); err != nil {
			return "", err
		}
		var arg struct {
			Question         string `json:"question"`
			Site             string `json:"site"`
			ExpiresInSeconds int    `json:"expires_in_seconds"`
		}
		if json.Unmarshal(raw, &arg) != nil {
			return "", ErrQuestionInvalidAnswer
		}
		if s.computer() == nil {
			return "", tooloutcome.New(tooloutcome.Permanent, "no_shared_computer", "not_executed", "There is no Bot computer to hand over; explain the blocker in your reply.", "explain_blocker").Err()
		}
		if arg.ExpiresInSeconds < 0 || arg.ExpiresInSeconds > int(maxTakeoverWait/time.Second) {
			return "", errors.New("expires_in_seconds must be between 0 and 840")
		}
		if arg.ExpiresInSeconds == 0 {
			arg.ExpiresInSeconds = int(defaultTakeoverWait / time.Second)
		}
		in, err := normalizeQuestionInput(askQuestionInput{Question: arg.Question, Type: questionTakeover, SourceURL: arg.Site, ExpiresInSeconds: arg.ExpiresInSeconds})
		if err != nil {
			return "", err
		}
		return s.postHandoverCard(ctx, c, r, in)
	}}
}

func (s *Server) connectionTool(c Conversation, r Run) Tool {
	return Tool{Name: "request_connection", Description: "Ask the person to connect one configured OAuth service, right here in the chat, using the same sign-in flow Settings offers on their device. Name the service exactly as manage_extensions lists it and say in question why it is needed. The run suspends until the connection succeeds, fails, is declined, is cancelled or expires; waiting does not use active time and survives a service restart. The result is status connected, failed, declined, cancelled or expired. The service's credentials are never returned. A service that is already connected needs no card and returns already_connected. Do not repeat a request the person declined or that failed.", Parameters: objectSchema(map[string]any{
		"service":            map[string]any{"type": "string", "description": "Name of an existing MCP service that uses OAuth"},
		"question":           map[string]any{"type": "string", "description": "Why the service is needed, in one or two plain sentences"},
		"expires_in_seconds": questionExpirySchema(),
	}, []string{"service", "question"}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := requireHumanPresent(c); err != nil {
			return "", err
		}
		var arg struct {
			Service          string `json:"service"`
			Question         string `json:"question"`
			ExpiresInSeconds int    `json:"expires_in_seconds"`
		}
		if json.Unmarshal(raw, &arg) != nil {
			return "", ErrQuestionInvalidAnswer
		}
		arg.Service = strings.TrimSpace(arg.Service)
		oauth, connected, err := s.connectionState(arg.Service)
		if err != nil {
			return "", err
		}
		if !oauth {
			return "", tooloutcome.InvalidArguments("service is not configured or does not use OAuth sign-in")
		}
		if connected {
			return `{"status":"already_connected","connected":true}`, nil
		}
		in, err := normalizeQuestionInput(askQuestionInput{Question: arg.Question, Type: questionConnection, Target: arg.Service, ExpiresInSeconds: arg.ExpiresInSeconds})
		if err != nil {
			return "", err
		}
		return s.postHandoverCard(ctx, c, r, in)
	}}
}

func (s *Server) postHandoverCard(ctx context.Context, c Conversation, r Run, in askQuestionInput) (string, error) {
	q, err := s.CreateQuestion(c.ID, r, in)
	if err != nil {
		return "", err
	}
	if _, err = s.store.Event(c.ID, "question", q.Card()); err != nil {
		_, _ = s.store.CancelQuestion(q.ID)
		return "", err
	}
	answer, err := s.WaitQuestion(ctx, q.ID)
	return string(answer), err
}

// connectionState reports whether name is a configured OAuth service and
// whether its credentials are currently present.
func (s *Server) connectionState(name string) (oauth, connected bool, err error) {
	if s.extensions == nil || name == "" {
		return false, false, nil
	}
	servers, err := s.extensions.ListMCP()
	if err != nil {
		return false, false, errors.New("extension configuration could not be read")
	}
	for _, server := range servers {
		if server.Name == name && server.OAuth != nil {
			return true, server.OAuth.Connected, nil
		}
	}
	return false, false, nil
}

func (s *Server) connectionEstablished(name string) bool {
	oauth, connected, err := s.connectionState(name)
	return err == nil && oauth && connected
}

// handoverOpen says whether the takeover card is still waiting for the person
// and belongs to this Bot.
func (s *Server) handoverOpen(questionID, botID string) bool {
	q, err := s.store.GetQuestion(questionID)
	if err != nil || q.Type != questionTakeover || q.Status != questionPending || q.BotID != botID {
		return false
	}
	if q.ExpiresAt != "" {
		if t, e := time.Parse(time.RFC3339Nano, q.ExpiresAt); e == nil && !time.Now().UTC().Before(t) {
			return false
		}
	}
	return true
}

// endHandoverControl releases the person's hold on the desktop once their
// takeover card has ended, so the resumed run never queues behind it.
func (s *Server) endHandoverControl(q Question) {
	if q.Type != questionTakeover || q.ID == "" {
		return
	}
	s.computerOwnerMu.Lock()
	c := s.computerControls[q.BotID]
	s.computerOwnerMu.Unlock()
	if c == nil || c.handover != q.ID {
		return
	}
	c.mu.Lock()
	s.releaseControl(c)
	c.mu.Unlock()
}

// holdsPendingTakeover says whether the run is parked on a takeover card; its
// browser tabs are the page the person is about to act on.
func (s *Store) holdsPendingTakeover(runID string) bool {
	var found bool
	_ = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM questions WHERE run_id=? AND type=? AND status=?)`, runID, questionTakeover, questionPending).Scan(&found)
	return found
}
