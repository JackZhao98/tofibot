package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// execMessage sends one one-time message from the running Bot to one or more
// Bots, each in a hidden trace of its own. It reports the runs it started.
func (s *Server) execMessage(ctx context.Context, c Conversation, r Run, b json.RawMessage) (string, []Run, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if c.ID == "" {
		return "", nil, errors.New("message requires a conversation")
	}
	var x struct {
		BotID   string   `json:"bot_id"`
		BotIDs  []string `json:"bot_ids"`
		Content string   `json:"content"`
	}
	if json.Unmarshal(b, &x) != nil {
		return "", nil, errors.New("invalid message")
	}
	x.Content = strings.TrimSpace(x.Content)
	recipients := append([]string(nil), x.BotIDs...)
	if strings.TrimSpace(x.BotID) != "" {
		recipients = append([]string{x.BotID}, recipients...)
	}
	if len(recipients) == 0 || x.Content == "" {
		return "", nil, errors.New("bot_id or bot_ids and content are required")
	}
	for _, recipient := range recipients {
		if strings.TrimSpace(recipient) == r.BotID {
			return "", nil, errors.New("cannot message the current bot")
		}
	}
	for _, recipient := range recipients {
		if _, err := s.store.GetBot(strings.TrimSpace(recipient)); err != nil {
			return "", nil, errors.New("bot not found")
		}
	}
	if err := s.yieldComputerOwner(r); err != nil {
		return "", nil, fmt.Errorf("cannot release shared desktop for message: %w", err)
	}
	_, children, err := s.store.AddBotMessages(c.ID, r.BotID, recipients, r.ID, x.Content)
	if err != nil {
		return "", nil, err
	}
	for _, child := range children {
		childConv, err := s.store.GetConversation(child.ConversationID)
		if err != nil {
			return "", nil, err
		}
		s.enqueue(childConv, child)
	}
	return fmt.Sprintf("message sent to %d Bot(s)", len(children)), children, nil

}

// execHandoff publishes a bounded task to one member and queues the member's
// run. It reports the run it started.
func (s *Server) execHandoff(ctx context.Context, c Conversation, r Run, b json.RawMessage) (string, []Run, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if c.Kind != "group" && c.Kind != "dm" {
		return "", nil, errors.New("handoff requires a conversation")
	}
	var x struct {
		BotID string `json:"bot_id"`
		Task  string `json:"task"`
	}
	if json.Unmarshal(b, &x) != nil {
		return "", nil, errors.New("invalid handoff")
	}
	x.Task = strings.TrimSpace(x.Task)
	if c.Kind == "group" && x.Task == "" {
		return "", nil, errors.New("group handoff task is required")
	}
	if x.BotID == r.BotID {
		return "", nil, errors.New("cannot handoff to the current bot")
	}
	if c.Kind == "group" {
		ok, e := s.store.IsMember(c.ID, x.BotID)
		if e != nil || !ok {
			return "", nil, errors.New("bot is not a group member")
		}
	} else if _, e := s.store.GetBot(x.BotID); e != nil {
		return "", nil, errors.New("bot not found")
	}
	if err := s.yieldComputerOwner(r); err != nil {
		return "", nil, fmt.Errorf("cannot release shared desktop for handoff: %w", err)
	}
	var child Run
	var e error
	if c.Kind == "dm" {
		_, child, e = s.store.AddForwardHandoff(c.ID, r.BotID, x.BotID, r.ID, x.Task)
	} else {
		_, child, e = s.store.AddHandoff(c.ID, r.BotID, x.BotID, r.ID, x.Task)
	}
	if e != nil {
		var conflict *delegationConflictError
		if errors.As(e, &conflict) {
			return "", nil, delegationRefusal(conflict.botName)
		}
		var addressed *recipientAddressedError
		if errors.As(e, &addressed) {
			return "", nil, recipientAddressedRefusal(addressed.botName)
		}
		return "", nil, e
	}
	childConv := c
	if child.ConversationID != c.ID {
		if resolved, ce := s.store.GetConversation(child.ConversationID); ce == nil {
			childConv = resolved
		}
	}
	s.enqueue(childConv, child)
	if s.store.steersOwnRun(child) {
		target := x.BotID
		if bot, be := s.store.GetBot(x.BotID); be == nil {
			target = bot.Name
		}
		return fmt.Sprintf("delivered to %s's current turn; it is already working on this user message and will pick up your handover. Do not repeat it.", target), []Run{child}, nil
	}
	return child.ID, []Run{child}, nil

}
