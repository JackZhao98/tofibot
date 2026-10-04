package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/mailread"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

func (s *Server) trustedMailEndpoint(endpoint, connection string) bool {
	if !localMCPID(connection) {
		return false
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	base := s.localRunnerMCPBase
	if s.isolatedWorkspace && s.microVM != nil {
		base, _ = url.Parse(computer.RunnerOrigin)
	}
	// Classify only the frozen service-owned origin; this predicate performs no
	// token reads or transport work. Actual dispatch retains its own checks.
	return exactRunnerMCP(u, base) && u.Path == strings.TrimRight(base.Path, "/")+"/mcp/"+connection
}
func (s *Store) recordMailRead(ctx context.Context, conv, bot, run, call string, snapshot mailread.Snapshot) error {
	if call == "" {
		return errors.New("mail read invocation unavailable")
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	// A runtime-owned call must already be running in the same account Store.
	result, err := s.db.ExecContext(ctx, `INSERT INTO mail_read_snapshots(run_id,call_id,data) SELECT run_id,call_id,? FROM tool_activities WHERE conversation_id=? AND bot_id=? AND run_id=? AND call_id=? AND status='running' AND name='call_mcp_tool' ON CONFLICT(run_id,call_id) DO NOTHING`, string(data), conv, bot, run, call)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return errors.New("mail read snapshot already recorded or invocation unavailable")
	}
	return nil
}
func (s *Server) mailReadContext(ctx context.Context, c Conversation, r Run) context.Context {
	return mailread.WithRecorder(ctx, func(callCtx context.Context, snapshot mailread.Snapshot, result string) (string, error) {
		call := runtime.ToolCallID(callCtx)
		refs := []MailSource{}
		for _, message := range snapshot.Messages {
			refs = append(refs, MailSource{RunID: r.ID, CallID: call, MessageID: message.ID})
		}
		descriptor, _ := json.Marshal(map[string]any{"mail_sources": refs, "provider": snapshot.Provider, "connection": snapshot.Connection, "mailbox": snapshot.Mailbox, "display_only_source": true})
		result += "\n" + string(descriptor)
		snapshot.Digest = mailread.Digest(result)
		if err := s.store.recordMailRead(callCtx, c.ID, r.BotID, r.ID, call, snapshot); err != nil {
			return "", err
		}
		return result, nil
	})
}
