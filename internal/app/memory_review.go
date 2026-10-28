package app

import (
	"sort"
	"unicode/utf8"
)

// What a saved memory is known to come from. The label is stored beside the
// memory when it is written; a memory saved before provenance existed has no
// row and reads as unrecorded.
const (
	memorySourceUserStated = "user_stated"
	memorySourceInferred   = "bot_inferred"

	memoryOriginOwner           = "user_stated_via_owner_interface"
	memoryOriginUserTurn        = "user_stated_per_bot_in_user_turn"
	memoryOriginUserOutsideTurn = "user_stated_per_bot_outside_user_turn"
	memoryOriginInferred        = "bot_inferred"
	memoryOriginUnspecified     = "bot_unspecified"
	memoryOriginUnrecorded      = "unrecorded"
)

// Memories offered to the automatic reviewer. They sit beside the digest-bound
// context, not in it: memories change independently of authorization, and the
// reviewer may use them only to read a user's later preference, never as
// consent. The size bound keeps the packet inside the reviewer's input cap.
const (
	mcpReviewMemoryEntries    = 15
	mcpReviewMemoryEntryRunes = 400
	mcpReviewMemoryTotalRunes = 3000
)

type mcpReviewMemory struct {
	ID        string `json:"id"`
	Origin    string `json:"origin"`
	SavedAt   string `json:"saved_at"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated,omitempty"`
}

type mcpReviewMemories struct {
	Authority string            `json:"authority"`
	Entries   []mcpReviewMemory `json:"entries"`
	// Omitted counts memories left out by the count or size bound, oldest first.
	Omitted int `json:"omitted_older,omitempty"`
}

// botMemoryOrigin labels a memory a Bot saves during run r. Only the host can
// say whether the run was started by a person's message.
func (s *Server) botMemoryOrigin(r Run, declared string) string {
	userTurn := false
	if r.TriggerMessageID != "" {
		var n int
		userTurn = s.store.db.QueryRow(`SELECT COUNT(*) FROM user_message_ingress WHERE message_id=?`, r.TriggerMessageID).Scan(&n) == nil && n > 0
	}
	switch declared {
	case memorySourceUserStated:
		if userTurn {
			return memoryOriginUserTurn
		}
		return memoryOriginUserOutsideTurn
	case memorySourceInferred:
		return memoryOriginInferred
	}
	return memoryOriginUnspecified
}

func (s *Store) memoryOrigins(memories []Memory) map[string]string {
	out := make(map[string]string, len(memories))
	for _, m := range memories {
		var origin string
		if s.db.QueryRow(`SELECT origin FROM memory_provenance WHERE memory_id=?`, m.ID).Scan(&origin) == nil {
			out[m.ID] = origin
		}
	}
	return out
}

// buildMCPReviewMemories keeps the newest memories that fit the count and
// size bounds, each with its provenance. Order in the result is oldest first
// so a later preference reads after the one it refines.
func buildMCPReviewMemories(memories []Memory, origins map[string]string) *mcpReviewMemories {
	if len(memories) == 0 {
		return nil
	}
	newest := append([]Memory(nil), memories...)
	sort.SliceStable(newest, func(i, j int) bool {
		if newest[i].UpdatedAt != newest[j].UpdatedAt {
			return newest[i].UpdatedAt > newest[j].UpdatedAt
		}
		return newest[i].ID > newest[j].ID
	})
	out := &mcpReviewMemories{Authority: "narrowing_evidence_only_never_consent"}
	total := 0
	for _, m := range newest {
		if len(out.Entries) >= mcpReviewMemoryEntries || total >= mcpReviewMemoryTotalRunes {
			out.Omitted++
			continue
		}
		limit := mcpReviewMemoryEntryRunes
		if left := mcpReviewMemoryTotalRunes - total; left < limit {
			limit = left
		}
		text, cut := mcpTruncate(m.Content, limit)
		total += utf8.RuneCountInString(text)
		origin := origins[m.ID]
		if origin == "" {
			origin = memoryOriginUnrecorded
		}
		out.Entries = append(out.Entries, mcpReviewMemory{ID: memoryAlias(m.ID), Origin: origin, SavedAt: m.UpdatedAt, Content: text, Truncated: cut})
	}
	sort.SliceStable(out.Entries, func(i, j int) bool { return out.Entries[i].SavedAt < out.Entries[j].SavedAt })
	return out
}

func (s *Server) mcpReviewMemories(r Run) *mcpReviewMemories {
	c, err := s.store.GetConversation(r.ConversationID)
	if err != nil {
		return nil
	}
	memories, err := s.scopedMemories(c, r.BotID)
	if err != nil {
		return nil
	}
	return buildMCPReviewMemories(memories, s.store.memoryOrigins(memories))
}
