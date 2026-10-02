package app

import "database/sql"

// runReturnTargetTx resolves the logical assignment behind a continuation.
// Parent links retain chronological causality for cancellation; this resolver
// walks through completed returns and retries to find who is actually waiting.
// Callers still validate destination availability and persist the return and
// any waiting-requester settlement in their transaction.
func runReturnTargetTx(tx *sql.Tx, done Run) (Run, bool, error) {
	const columns = `SELECT id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at FROM runs WHERE id=?`
	steps := 0
	load := func(id string) (Run, bool, error) {
		steps++
		if id == "" || steps > 64 {
			return Run{}, false, nil
		}
		r, err := scanRun(tx.QueryRow(columns, id))
		if err == sql.ErrNoRows {
			return Run{}, false, nil
		}
		if err != nil {
			return Run{}, false, err
		}
		return r, true, nil
	}
	visiting := make(map[string]bool)
	var original func(Run) (Run, bool, error)
	var requester func(Run) (Run, bool, error)
	original = func(r Run) (Run, bool, error) {
		if r.ID == "" || visiting[r.ID] {
			return Run{}, false, nil
		}
		visiting[r.ID] = true
		defer delete(visiting, r.ID)
		if r.ParentRunID == "" {
			return r, true, nil
		}
		parent, ok, err := load(r.ParentRunID)
		if err != nil || !ok {
			return Run{}, false, err
		}
		// A retry retains the entire assignment identity, including its origin.
		// Its failed/interrupted predecessor is not the waiting requester.
		if r.ConversationID == parent.ConversationID && r.BotID == parent.BotID &&
			r.Kind == parent.Kind && r.TriggerMessageID != "" && r.TriggerMessageID == parent.TriggerMessageID &&
			r.OriginConversationID == parent.OriginConversationID {
			return original(parent)
		}
		if r.Kind == runKindFollowup {
			// Only a completed child result can anchor a return. This differs
			// from the requester, which may still be waiting or running.
			if parent.Status != "done" || r.TriggerMessageID == "" {
				return Run{}, false, nil
			}
			var anchored bool
			if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM messages WHERE id=? AND conversation_id=? AND run_id=? AND role='assistant' AND sender_bot_id=? AND kind IN ('','forward_result','bot_result'))`, r.TriggerMessageID, r.ConversationID, parent.ID, parent.BotID).Scan(&anchored); err != nil {
				return Run{}, false, err
			}
			if !anchored {
				return Run{}, false, nil
			}
			caller, ok, err := requester(parent)
			if err != nil || !ok || caller.BotID != r.BotID || caller.ConversationID != r.ConversationID {
				return Run{}, false, err
			}
			return original(caller)
		}
		return r, true, nil
	}
	requester = func(r Run) (Run, bool, error) {
		assignment, ok, err := original(r)
		if err != nil || !ok || assignment.ParentRunID == "" {
			return Run{}, false, err
		}
		caller, ok, err := load(assignment.ParentRunID)
		if err != nil || !ok || caller.BotID == "" || caller.BotID == assignment.BotID {
			return Run{}, false, err
		}
		if caller.ConversationID == assignment.ConversationID {
			if assignment.Kind != runKindGroupTask || (caller.Status != "done" && caller.Status != runWaiting) {
				return Run{}, false, nil
			}
		} else {
			// Messages and legacy forwards record the immediate source. Team
			// dispatches inherit the requester's origin across group hops, but
			// still return to that requester so it can integrate the result.
			expectedOrigin := caller.ConversationID
			if assignment.Kind == runKindTeam && caller.OriginConversationID != "" {
				expectedOrigin = caller.OriginConversationID
			}
			if assignment.OriginConversationID == "" || assignment.OriginConversationID != expectedOrigin {
				return Run{}, false, nil
			}
			switch assignment.Kind {
			case "", runKindMessage, runKindTeam:
			default:
				return Run{}, false, nil
			}
			if caller.Status != "done" && caller.Status != runWaiting && caller.Status != "running" {
				return Run{}, false, nil
			}
		}
		return caller, true, nil
	}
	return requester(done)
}

// groupReturnTargetTx keeps group-only callers inside the current conversation.
func groupReturnTargetTx(tx *sql.Tx, done Run) (Run, bool, error) {
	caller, ok, err := runReturnTargetTx(tx, done)
	if err != nil || !ok || caller.ConversationID != done.ConversationID {
		return Run{}, false, err
	}
	return caller, true, nil
}

// A result addressed to the waiting requester is a reply, not a new task.
// Explicit handoff calls remain available for intentional additional work.
func (s *Store) isGroupReturnMention(r Run, target string) bool {
	tx, err := s.db.Begin()
	if err != nil {
		return false
	}
	defer tx.Rollback()
	caller, ok, err := groupReturnTargetTx(tx, r)
	return err == nil && ok && caller.BotID == target
}
