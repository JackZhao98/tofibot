package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

const maxMCPApprovalPayloadBytes = 32 << 10

// approveMCPCall binds one human decision to the exact run, MCP configuration
// snapshot, tool and argument bytes. Claiming precedes remote execution: an
// uncertain remote result must never cause an automatic replay.
func (s *Server) approveMCPCall(ctx context.Context, c Conversation, r Run, call extensions.MCPCallApproval) error {
	if err := s.extensionToolActive(ctx, c, r); err != nil {
		return err
	}
	if call.Server == "" || call.Tool == "" || call.ConfigVersion == "" {
		return errors.New("external tool approval target is incomplete")
	}
	payload, err := mcpApprovalPayload(call.Arguments)
	if err != nil {
		return err
	}
	hash := mcpApprovalHash(call)
	var id, status, claimed string
	var answer sql.NullString
	err = s.store.db.QueryRow(`SELECT q.id,q.status,q.answer_json,a.claimed_at
		FROM mcp_call_approvals a JOIN questions q ON q.id=a.question_id
		WHERE a.run_id=? AND a.action_hash=? AND q.conversation_id=? AND q.bot_id=?
		ORDER BY q.created_at DESC,q.id DESC LIMIT 1`, r.ID, hash, c.ID, r.BotID).Scan(&id, &status, &answer, &claimed)
	if errors.Is(err, sql.ErrNoRows) {
		in, normalizeErr := normalizeQuestionInput(askQuestionInput{
			Question: "Allow this external tool call?",
			Type:     questionApproval,
			Approval: &ApprovalDetails{
				Action:  "Call " + call.Tool,
				Target:  "MCP server " + call.Server,
				Impact:  fmt.Sprintf("The external server may change data or contact others. Review the complete %d-byte argument payload below before approving.", len(call.Arguments)),
				Payload: payload,
			},
		})
		if normalizeErr != nil {
			return normalizeErr
		}
		q, createErr := s.store.CreateQuestion(c.ID, r, in)
		if createErr != nil {
			return createErr
		}
		if _, createErr = s.store.db.Exec(`INSERT INTO mcp_call_approvals(question_id,run_id,action_hash) VALUES(?,?,?)`, q.ID, r.ID, hash); createErr != nil {
			_, _ = s.store.db.Exec(`DELETE FROM questions WHERE id=? AND status=?`, q.ID, questionPending)
			return createErr
		}
		if _, createErr = s.store.Event(c.ID, "question", q.Card()); createErr != nil {
			_, _ = s.store.db.Exec(`UPDATE questions SET status=?,updated_at=? WHERE id=? AND status=?`, questionCancelled, now(), q.ID, questionPending)
			return createErr
		}
		id, status = q.ID, q.Status
	} else if err != nil {
		return err
	}
	if claimed != "" {
		return errors.New("this exact external tool call already used its approval; inspect the result before proposing another action")
	}
	if status == questionPending {
		result, waitErr := s.WaitQuestion(ctx, id)
		if waitErr != nil {
			return waitErr
		}
		answer = sql.NullString{String: string(result), Valid: true}
		status = questionAnswered
	}
	if status != questionAnswered || !answer.Valid || strings.TrimSpace(answer.String) != "true" {
		return errors.New("external tool call was not approved")
	}
	if err := s.extensionToolActive(ctx, c, r); err != nil {
		return err
	}
	result, err := s.store.db.Exec(`UPDATE mcp_call_approvals SET claimed_at=? WHERE question_id=? AND claimed_at=''
		AND EXISTS(SELECT 1 FROM questions WHERE id=? AND status='answered' AND answer_json='true')`, now(), id, id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return errors.New("external tool approval was already used or is no longer valid")
	}
	return ctx.Err()
}

func mcpApprovalHash(call extensions.MCPCallApproval) string {
	encoded, _ := json.Marshal(struct {
		Server        string          `json:"server"`
		Tool          string          `json:"tool"`
		ConfigVersion string          `json:"config_version"`
		Arguments     json.RawMessage `json:"arguments"`
	}{call.Server, call.Tool, call.ConfigVersion, call.Arguments})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func mcpApprovalPayload(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || len(raw) > maxMCPApprovalPayloadBytes || !json.Valid(raw) || !utf8.Valid(raw) {
		return "", errors.New("external tool arguments are invalid or exceed the 32 KiB review limit")
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return "", errors.New("external tool arguments must be a JSON object")
	}
	if containsMCPSecretField(value) {
		return "", errors.New("external tool arguments contain a private field; use a dedicated protected input flow")
	}
	return string(raw), nil
}

func containsMCPSecretField(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			name := strings.ToLower(key)
			name = strings.Map(func(r rune) rune {
				if r == '_' || r == '-' || r == ' ' {
					return -1
				}
				return r
			}, name)
			for _, term := range []string{"password", "passwd", "secret", "token", "authorization", "credential", "privatekey", "apikey"} {
				if strings.Contains(name, term) {
					return true
				}
			}
			if containsMCPSecretField(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if containsMCPSecretField(child) {
				return true
			}
		}
	}
	return false
}
