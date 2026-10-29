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
				if isFollowGlobalModel(model) {
					return
				}
				if _, ok := seen[model]; ok {
					return
				}
				seen[model] = struct{}{}
				models = append(models, model)
			}
			defaultModel := s.effectiveDefaultModel(ctx)
			addModel(defaultModel)
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
			}{Bots: s.withEffectiveModels(ctx, bots...), Groups: groups, ConfiguredModels: models, DefaultModel: defaultModel, Provider: s.activeProvider()}
			encoded, err := json.Marshal(result)
			return string(encoded), err
		}),
		tool("workspace_update_bot", "Update a Bot's name, durable instructions, or configured model. Omitted fields are preserved; use workspace_list first and do not invent model IDs. This changes metadata only and never deletes history or changes the canonical DM ID. The settings tool makes the same changes. A change to another Bot, a model, group members or a deletion is applied only after the person approves a card: when the call refuses with need_approval, ask with request_approval using the target it returns, then repeat the same call. Runs that no person attends change nothing.", map[string]any{
			"bot_id":           map[string]any{"type": "string"},
			"name":             map[string]any{"type": "string"},
			"instructions":     map[string]any{"type": "string"},
			"model":            map[string]any{"type": "string", "description": "Exact configured model ID to pin, or \"default\" (or an empty string) to follow the workspace's global model."},
			"reasoning_effort": map[string]any{"type": "string", "description": "Supported reasoning effort for the selected model, or \"default\" (or empty): the global effort when following the global model, else the pinned model's default."},
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
				value := s.normalizeBotModel(*input.Model)
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
				if value == "" {
					value = followGlobalModel
				}
				model := ""
				if input.Model != nil {
					model = *input.Model
				}
				if input.Model == nil {
					if current, e := s.store.GetBot(input.BotID); e == nil {
						model = current.Model
					}
				}
				if e := s.validateModelChoice(ctx, model, value); e != nil {
					return "", e
				}
				input.ReasoningEffort = &value
			} else if input.Model != nil && *input.Model == followGlobalModel {
				// Following the global model follows its effort too.
				effort := followGlobalModel
				input.ReasoningEffort = &effort
			}
			// Each field is one settings change, so it meets the same rules as the settings tool: the Bot's own name and instructions are
			// applied and recorded, another Bot's fields and every model change wait for the person's card, an unattended run changes nothing.
			sr := settingRun{s: s, c: c, r: r}
			if input.Name != nil {
				if _, err := sr.aliasSet(ctx, "bot.name", Target{BotID: input.BotID}, *input.Name); err != nil {
					return "", err
				}
			}
			if input.Instructions != nil {
				if _, err := sr.aliasSet(ctx, "bot.instructions", Target{BotID: input.BotID}, *input.Instructions); err != nil {
					return "", err
				}
			}
			if input.Model != nil {
				if _, err := sr.aliasSet(ctx, "bot.model", Target{BotID: input.BotID}, *input.Model); err != nil {
					return "", err
				}
			}
			if input.ReasoningEffort != nil {
				if _, err := sr.aliasSet(ctx, "bot.reasoning_effort", Target{BotID: input.BotID}, strings.TrimSpace(*input.ReasoningEffort)); err != nil {
					return "", err
				}
			}
			updated, err := s.store.GetBot(input.BotID)
			if err != nil {
				return "", err
			}
			encoded, err := json.Marshal(s.withEffectiveModel(ctx, updated))
			return string(encoded), err
		}),
		tool("workspace_update_group", "Rename a discussion group or replace its members. Members must be 2-8 distinct existing active Bots; use expected_name and expected_bot_ids from workspace_list to avoid overwriting another client's change. A queued or running task makes membership changes fail with an actionable busy error. History and sender identities are preserved. The settings tool makes the same changes. A change to another Bot, a model, group members or a deletion is applied only after the person approves a card: when the call refuses with need_approval, ask with request_approval using the target it returns, then repeat the same call. Runs that no person attends change nothing.", map[string]any{
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
			current, err := s.store.GetConversation(input.GroupID)
			if err != nil {
				return "", mapSettingsWriteError(err)
			}
			// The expectations guard against another client's change. A field this call already put in place (the call is repeated
			// after the person's card) is not held to them again.
			renamed := input.Name != nil && *input.Name == current.Name
			if input.ExpectedName != nil && !renamed && *input.ExpectedName != current.Name {
				return "", ErrGroupNameConflict
			}
			if input.ExpectedBotIDs != nil {
				expected, e := normalizeMaintainedMembers(*input.ExpectedBotIDs)
				requested, e2 := normalizeMaintainedMembers(derefStrings(input.BotIDs))
				membersDone := input.BotIDs != nil && e2 == nil && sameStringSet(current.BotIDs, requested)
				if !membersDone && (e != nil || !sameStringSet(current.BotIDs, expected)) {
					return "", ErrGroupMemberConflict
				}
			}
			// The same rules as the settings tool: a rename is applied and recorded, a member change waits for the person's card.
			sr := settingRun{s: s, c: c, r: r}
			if input.Name != nil {
				if _, err := sr.aliasSet(ctx, "conversation.name", Target{ConversationID: input.GroupID}, *input.Name); err != nil {
					return "", err
				}
			}
			if input.BotIDs != nil {
				if _, err := sr.aliasSet(ctx, "conversation.members", Target{ConversationID: input.GroupID}, *input.BotIDs); err != nil {
					return "", err
				}
			}
			updated, err := s.store.GetConversation(input.GroupID)
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
	if isFollowGlobalModel(model) || model == s.defaultModel {
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

func derefStrings(p *[]string) []string {
	if p == nil {
		return nil
	}
	return *p
}
