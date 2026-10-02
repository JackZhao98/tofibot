package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

func (s *Server) reactionTool(c Conversation, r Run) Tool {
	return Tool{Name: "react_to_message", Description: "Optionally add or remove one emoji reaction on a message in this conversation. Use a reaction sparingly when it naturally acknowledges the user's or another Bot's message, conveys a fitting emotion, or adds personality without a redundant reply. Do not react mechanically to every message. Omit message_id only to target this run's triggering message; use an exact message_id from recent conversation context or search_history for another message. The action is idempotent.", Parameters: objectSchema(map[string]any{
		"message_id": map[string]any{"type": "string", "description": "Exact message id; omit for the message that triggered this run"},
		"emoji":      map[string]any{"type": "string", "description": "One fitting emoji reaction"},
		"action":     map[string]any{"type": "string", "enum": []string{"add", "remove"}, "description": "Defaults to add"},
	}, []string{"emoji"}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
		var in struct {
			MessageID string `json:"message_id"`
			Emoji     string `json:"emoji"`
			Action    string `json:"action"`
		}
		if json.Unmarshal(raw, &in) != nil {
			return "", errors.New("invalid reaction")
		}
		messageID := strings.TrimSpace(in.MessageID)
		if messageID == "" {
			messageID = r.TriggerMessageID
		}
		if messageID == "" || in.Action != "" && in.Action != "add" && in.Action != "remove" {
			return "", errors.New("message_id and a valid action are required")
		}
		_, err := s.store.SetMessageReaction(ctx, c.ID, messageID, "bot:"+r.BotID, in.Emoji, in.Action != "remove", &r)
		if err != nil {
			return "", err
		}
		return "Reaction updated.", nil
	}}
}
