package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// workspaceTools exposes the workspace-level settings that can otherwise be
// changed through the user-facing HTTP API. The caller must still be an
// active run; the Store methods below remain the source of truth for
// transactions, busy checks, archive semantics, and workspace events.
//
// This function is intentionally separate from Server.tools so the main tool
// registry can choose when to expose these mutations without duplicating the
// HTTP handlers or their persistence logic.
func (s *Server) workspaceTools(c Conversation, r Run) []Tool {
	tool := func(name, description string, properties map[string]any, required []string, execute func(context.Context, json.RawMessage) (string, error)) Tool {
		return Tool{Name: name, Description: description, Parameters: objectSchema(properties, required), Execute: execute}
	}

	return append([]Tool{
		tool("workspace_list", "List all Bots and discussion groups in this workspace, including archived entries and their stable IDs. Also return the configured model IDs; use this before changing workspace settings.", map[string]any{}, nil, func(ctx context.Context, _ json.RawMessage) (string, error) {
			if err := requireWorkspaceToolRun(s, c, r); err != nil {
				return "", err
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
			bots, err := s.store.ListBots(true)
			if err != nil {
				return "", err
			}
			conversations, err := s.store.ListConversations(true)
			if err != nil {
				return "", err
			}
			groups := make([]Conversation, 0, len(conversations))
			for _, conversation := range conversations {
				if conversation.Kind == "group" {
					groups = append(groups, conversation)
				}
			}
			models := make([]string, 0, len(bots)+1)
			seen := make(map[string]struct{}, len(bots)+1)
			addModel := func(model string) {
				model = strings.TrimSpace(model)
				if model == "" {
					return
				}
				if _, ok := seen[model]; ok {
					return
				}
				seen[model] = struct{}{}
				models = append(models, model)
			}
			addModel(s.defaultModel)
			for _, bot := range bots {
				addModel(bot.Model)
			}
			if s.strictModelValidation() {
				catalog, _, _ := s.loadModels(ctx)
				for _, option := range catalog {
					addModel(option.ID)
				}
			}
			sort.Strings(models)
			result := struct {
				Bots             []Bot          `json:"bots"`
				Groups           []Conversation `json:"groups"`
				ConfiguredModels []string       `json:"configured_models"`
				DefaultModel     string         `json:"default_model"`
				Provider         string         `json:"provider"`
			}{Bots: bots, Groups: groups, ConfiguredModels: models, DefaultModel: s.defaultModel, Provider: s.activeProvider()}
			encoded, err := json.Marshal(result)
			return string(encoded), err
		}),
		tool("workspace_update_bot", "Update a Bot's name, durable instructions, or configured model. Omitted fields are preserved; use workspace_list first and do not invent model IDs. This changes metadata only and never deletes history or changes the canonical DM ID.", map[string]any{
			"bot_id":           map[string]any{"type": "string"},
			"name":             map[string]any{"type": "string"},
			"instructions":     map[string]any{"type": "string"},
			"model":            map[string]any{"type": "string", "description": "Exact configured model ID; an empty string uses the workspace default."},
			"reasoning_effort": map[string]any{"type": "string", "description": "Supported reasoning effort for the selected model; empty uses the model default."},
		}, []string{"bot_id"}, func(ctx context.Context, raw json.RawMessage) (string, error) {
			if err := requireWorkspaceToolRun(s, c, r); err != nil {
				return "", err
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var input struct {
				BotID           string  `json:"bot_id"`
				Name            *string `json:"name"`
				Instructions    *string `json:"instructions"`
				Model           *string `json:"model"`
				ReasoningEffort *string `json:"reasoning_effort"`
			}
			if err := decodeWorkspaceTool(raw, &input); err != nil {
				return "", errors.New("invalid workspace_update_bot request")
			}
			input.BotID = strings.TrimSpace(input.BotID)
			if input.BotID == "" {
				return "", errors.New("bot_id is required")
			}
			if input.Name == nil && input.Instructions == nil && input.Model == nil && input.ReasoningEffort == nil {
				return "", errors.New("at least one of name, instructions, or model is required")
			}
			if input.Name != nil {
				value := strings.TrimSpace(*input.Name)
				if value == "" {
					return "", errors.New("name is required")
				}
				if len([]rune(value)) > maxTeamNameRunes {
					return "", fmt.Errorf("name is too long")
				}
				input.Name = &value
			}
			if input.Instructions != nil {
				value := strings.TrimSpace(*input.Instructions)
				if len([]rune(value)) > maxSystemRunes {
					return "", fmt.Errorf("instructions is too long")
				}
				input.Instructions = &value
			}
			if input.Model != nil {
				value := strings.TrimSpace(*input.Model)
				switch strings.ToLower(value) {
				case "default", "auto", "inherit":
					value = ""
				}
				if err := s.validateWorkspaceModel(ctx, value); err != nil {
					return "", err
				}
				if err := s.validateModelID(ctx, value); err != nil {
					return "", err
				}
				input.Model = &value
			}
			if input.ReasoningEffort != nil {
				value := strings.TrimSpace(*input.ReasoningEffort)
				model := ""
				if input.Model != nil {
					model = *input.Model
				}
				if model == "" {
					if current, e := s.store.GetBot(input.BotID); e == nil {
						model = current.Model
					}
				}
				if e := s.validateModelChoice(ctx, model, value); e != nil {
					return "", e
				}
				input.ReasoningEffort = &value
			}
			updated, err := s.store.UpdateBot(input.BotID, input.Name, input.Instructions, input.Model)
			if err != nil {
				return "", err
			}
			if input.ReasoningEffort != nil {
				updated, err = s.store.UpdateBotReasoningEffort(input.BotID, strings.TrimSpace(*input.ReasoningEffort))
				if err != nil {
					return "", err
				}
			}
			encoded, err := json.Marshal(updated)
			return string(encoded), err
		}),
		tool("workspace_update_group", "Rename a discussion group or replace its members. Members must be 2-8 distinct existing active Bots; use expected_name and expected_bot_ids from workspace_list to avoid overwriting another client's change. A queued or running task makes membership changes fail with an actionable busy error. History and sender identities are preserved.", map[string]any{
			"group_id":         map[string]any{"type": "string"},
			"name":             map[string]any{"type": "string"},
			"bot_ids":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"expected_name":    map[string]any{"type": "string"},
			"expected_bot_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		}, []string{"group_id"}, func(ctx context.Context, raw json.RawMessage) (string, error) {
			if err := requireWorkspaceToolRun(s, c, r); err != nil {
				return "", err
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var input struct {
				GroupID        string    `json:"group_id"`
				Name           *string   `json:"name"`
				BotIDs         *[]string `json:"bot_ids"`
				ExpectedName   *string   `json:"expected_name"`
				ExpectedBotIDs *[]string `json:"expected_bot_ids"`
			}
			if err := decodeWorkspaceTool(raw, &input); err != nil {
				return "", errors.New("invalid workspace_update_group request")
			}
			input.GroupID = strings.TrimSpace(input.GroupID)
			if input.GroupID == "" {
				return "", errors.New("group_id is required")
			}
			if input.Name == nil && input.BotIDs == nil {
				return "", errors.New("name or bot_ids is required")
			}
			if input.Name != nil {
				value := strings.TrimSpace(*input.Name)
				if value == "" {
					return "", errors.New("name is required")
				}
				if len([]rune(value)) > maxTeamNameRunes {
					return "", errors.New("name is too long")
				}
				input.Name = &value
			}
			if input.ExpectedName != nil {
				value := strings.TrimSpace(*input.ExpectedName)
				input.ExpectedName = &value
			}
			updated, err := s.store.UpdateGroup(input.GroupID, GroupUpdate{Name: input.Name, BotIDs: input.BotIDs, ExpectedName: input.ExpectedName, ExpectedBotIDs: input.ExpectedBotIDs})
			if err != nil {
				return "", err
			}
			encoded, err := json.Marshal(updated)
			return string(encoded), err
		}),
	}, s.workspaceDeletionTools(c, r)...)
}

func requireWorkspaceToolRun(s *Server, conversation Conversation, run Run) error {
	if s == nil || s.store == nil || strings.TrimSpace(run.ID) == "" {
		return errors.New("workspace management requires an active run")
	}
	current, err := s.store.GetRun(run.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("workspace management requires an active run")
		}
		return err
	}
	if current.Status != "running" ||
		(run.BotID != "" && current.BotID != run.BotID) ||
		(run.ConversationID != "" && current.ConversationID != run.ConversationID) ||
		(conversation.ID != "" && current.ConversationID != conversation.ID) {
		return errors.New("workspace management requires an active run")
	}
	return nil
}

// Tool schemas advertise additionalProperties:false. Keep that contract at
// execution time as well so a model cannot silently believe a misspelled
// setting was applied.
func decodeWorkspaceTool(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func (s *Server) validateWorkspaceModel(ctx context.Context, model string) error {
	if model == "" || model == s.defaultModel {
		return nil
	}
	if s.strictModelValidation() {
		catalog, _, _ := s.loadModels(ctx)
		if _, ok := s.matchModel(catalog, model); ok {
			return nil
		}
	}
	var configured int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM bots WHERE model=?`, model).Scan(&configured); err != nil {
		return err
	}
	if configured == 0 {
		return errors.New("model is not configured; use workspace_list and choose one of configured_models")
	}
	return nil
}
