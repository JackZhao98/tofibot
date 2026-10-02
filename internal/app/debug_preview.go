package app

import (
	"net/http"
	"strings"
	"time"
)

type debugPreviewTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type debugPreviewResponse struct {
	Bot            Bot                `json:"bot"`
	ConversationID string             `json:"conversation_id"`
	SystemPrompt   string             `json:"system_prompt"`
	Tools          []debugPreviewTool `json:"tools"`
	LastRunTools   []debugPreviewTool `json:"last_run_tools"`
	LastRunAt      string             `json:"last_run_at"`
	PreviewScope   string             `json:"preview_scope"`
	Caveats        []string           `json:"caveats"`
}

type toolSnapshotKey struct {
	BotID          string
	ConversationID string
}

type toolSnapshot struct {
	Tools      []debugPreviewTool
	RecordedAt time.Time
}

func (s *Server) recordToolSnapshot(botID, conversationID string, registered []Tool) {
	if botID == "" || conversationID == "" {
		return
	}
	tools := make([]debugPreviewTool, 0, len(registered))
	for _, tool := range registered {
		if tool.Name == "" {
			continue
		}
		tools = append(tools, debugPreviewTool{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters})
	}
	now := time.Now().UTC()
	s.toolSnapshotMu.Lock()
	defer s.toolSnapshotMu.Unlock()
	if s.toolSnapshots == nil {
		s.toolSnapshots = make(map[toolSnapshotKey]toolSnapshot)
	}
	s.toolSnapshots[toolSnapshotKey{BotID: botID, ConversationID: conversationID}] = toolSnapshot{Tools: tools, RecordedAt: now}
	if len(s.toolSnapshots) > 256 {
		var oldest toolSnapshotKey
		var oldestAt time.Time
		for key, snapshot := range s.toolSnapshots {
			if oldestAt.IsZero() || snapshot.RecordedAt.Before(oldestAt) {
				oldest, oldestAt = key, snapshot.RecordedAt
			}
		}
		delete(s.toolSnapshots, oldest)
	}
}

func (s *Server) toolSnapshot(botID, conversationID string) (toolSnapshot, bool) {
	s.toolSnapshotMu.RLock()
	defer s.toolSnapshotMu.RUnlock()
	snapshot, ok := s.toolSnapshots[toolSnapshotKey{BotID: botID, ConversationID: conversationID}]
	return snapshot, ok
}

const runtimeDurablePrompt = "\nBefore saving memory, list_memory; correct facts with update_memory. Keep session/compaction details private."

const runtimeCapabilityPrompt = "\nFor requested settings changes, first inspect workspace_capabilities and current state; preserve omitted fields and respect busy-target conflicts."

// botDebugPreview returns the same static prompt and core tool registry used
// by a normal run, while deliberately avoiding tool execution and MCP
// discovery. Dynamic history, memory, extension discovery, and VM status can
// change between preview and execution and are called out in caveats.
func (s *Server) botDebugPreview(w http.ResponseWriter, r *http.Request, botID string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	bot, err := s.store.GetBot(strings.TrimSpace(botID))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "bot not found")
		return
	}
	conversationID := strings.TrimSpace(r.URL.Query().Get("conversation_id"))
	if conversationID == "" {
		conversationID = bot.DMConversationID
	}
	conversation, err := s.store.GetConversation(conversationID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "conversation not found")
		return
	}
	if conversation.Kind == "dm" {
		if conversation.BotID != bot.ID {
			writeErr(w, http.StatusNotFound, "not_found", "Bot is not part of this conversation")
			return
		}
	} else if conversation.Kind == "group" {
		member, memberErr := s.store.IsMember(conversation.ID, bot.ID)
		if memberErr != nil || !member {
			writeErr(w, http.StatusNotFound, "not_found", "Bot is not a member of this group")
			return
		}
	} else {
		writeErr(w, http.StatusBadRequest, "invalid_conversation", "unsupported conversation kind")
		return
	}

	run := Run{BotID: bot.ID, ConversationID: conversation.ID}
	_, core := s.buildContextParts(conversation, run, bot)
	system, promptErr := assembleRunSystem(runSystemPrompt{Core: core, BotInstructions: bot.Instructions, Durable: runtimeDurablePrompt, Computer: s.microVMEnvironmentPrompt(r.Context(), bot.ID), Capabilities: runtimeCapabilityPrompt})
	if promptErr != nil {
		writeErr(w, http.StatusBadRequest, "prompt_budget", promptErr.Error())
		return
	}
	registered := append(s.tools(conversation, run), s.longTermMemoryTools(conversation, run)...)
	registered = append(registered, capabilityTool(registered))
	lastRun, hasLastRun := s.toolSnapshot(bot.ID, conversation.ID)
	lastTools := make([]debugPreviewTool, 0)
	var lastAt string
	if hasLastRun {
		lastTools = lastRun.Tools
		lastAt = lastRun.RecordedAt.Format(time.RFC3339Nano)
	}
	tools := make([]debugPreviewTool, 0, len(registered))
	for _, tool := range registered {
		if tool.Name == "" {
			continue
		}
		tools = append(tools, debugPreviewTool{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters})
	}
	writeJSON(w, http.StatusOK, debugPreviewResponse{
		Bot: bot, ConversationID: conversation.ID, SystemPrompt: system, Tools: tools, LastRunTools: lastTools, LastRunAt: lastAt,
		PreviewScope: "current_configuration",
		Caveats: []string{
			"This is a read-only preview of the current Bot configuration and conversation roster.",
			"A run adds bounded history, summaries, memory, attachments, and handoff context dynamically.",
			"MCP and skill discovery is not started by this endpoint; configured extensions may add tools at run time.",
			"Tool definitions contain schemas only. No tool is executed and no credential value is returned.",
		},
	})
}
