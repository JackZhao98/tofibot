package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// Response dispositions: every Bot turn in a group ends with one explicit
// choice, decided after reading the new messages.
const (
	dispReply     = "reply"
	dispSilent    = "silent"
	dispConcluded = "concluded"
	dispFollowUp  = "follow_up"
)

const defaultWaitTimeout = 10 * time.Minute

const groupDispositionGuidance = `[group response]
End each group turn with respond: reply (post; list Bots to start in wake with a concrete request, a plain @mention starts nothing), silent (not for you), concluded (thanks/ok/done: post nothing, never answer acknowledgements), follow_up (waiting on a Bot: set wait_for instead of posting "I'll wait"). Default is reply, no wakes.
[/group response]
`

type groupWakeEntry struct {
	BotID   string `json:"bot_id"`
	Request string `json:"request"`
}

type groupChoice struct {
	disposition string
	wakes       []groupWakeEntry
}

// lineageKey identifies the originating user message (or scheduled occurrence)
// of a run: the trigger of its root run, else the root run itself.
func lineageKey(q interface {
	QueryRow(string, ...any) *sql.Row
}, runID string) string {
	key := runID
	for id, steps := runID, 0; id != "" && steps <= 64; steps++ {
		var parent, trigger sql.NullString
		if q.QueryRow(`SELECT parent_run_id,trigger_message_id FROM runs WHERE id=?`, id).Scan(&parent, &trigger) != nil {
			break
		}
		key = id
		if parent.String == "" && trigger.String != "" {
			key = trigger.String
		}
		id = parent.String
	}
	return key
}

func (s *Server) groupRespondTools(c Conversation, r Run) []runtime.Tool {
	if c.Kind != "group" || !c.UserVisible || r.Kind == runKindTriage {
		return nil
	}
	return []runtime.Tool{{Name: "respond", Description: "End your group turn with one explicit response disposition: reply (post a message; list Bots to start in wake with a concrete request), silent (nothing to add; post nothing), concluded (acknowledgement or closing such as thanks/ok/done; post nothing), or follow_up (waiting on another Bot's output: set wait_for). Call it once, before your final message for reply, or instead of a message for the others.", Parameters: objectSchema(map[string]any{
		"disposition": map[string]any{"type": "string", "enum": []string{dispReply, dispSilent, dispConcluded, dispFollowUp}},
		"wake":        map[string]any{"type": "array", "items": objectSchema(map[string]any{"bot_id": map[string]any{"type": "string"}, "request": map[string]any{"type": "string"}}, []string{"bot_id", "request"})},
		"wait_for":    map[string]any{"type": "string", "description": "Bot id you are waiting on (follow_up only)"},
	}, []string{"disposition"}), Execute: func(ctx context.Context, data json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var in struct {
			Disposition string           `json:"disposition"`
			Wake        []groupWakeEntry `json:"wake"`
			WaitFor     string           `json:"wait_for"`
		}
		if json.Unmarshal(data, &in) != nil {
			return "", errors.New("invalid respond arguments")
		}
		members := map[string]bool{}
		for _, id := range c.BotIDs {
			members[id] = true
		}
		for _, w := range in.Wake {
			if !members[w.BotID] || w.BotID == r.BotID || strings.TrimSpace(w.Request) == "" {
				return "", errors.New("each wake entry needs another group member's bot_id and a concrete request")
			}
		}
		if len(in.Wake) > 3 {
			return "", errors.New("wake at most 3 Bots")
		}
		switch in.Disposition {
		case dispReply:
			s.dispositions.Store(r.ID, groupChoice{disposition: dispReply, wakes: in.Wake})
			return "Recorded. Now post your reply; the listed Bots start after it is published.", nil
		case dispSilent, dispConcluded, dispFollowUp:
			if len(in.Wake) > 0 {
				return "", errors.New("only a reply can wake Bots")
			}
			if r.Kind == runKindSchedule || r.scheduleTask != nil || r.Kind == runKindGroupTask || r.Kind == runKindTeam || r.Kind == runKindMessage {
				return "", errors.New("this assignment needs a posted result; use reply")
			}
			if in.Disposition == dispFollowUp {
				if !members[in.WaitFor] || in.WaitFor == r.BotID {
					return "", errors.New("follow_up needs wait_for set to another group member's bot_id")
				}
				if err := s.registerWait(c, r, in.WaitFor); err != nil {
					return "", err
				}
			} else if r.Kind != runKindGroupMention && r.Kind != runKindFollowup && r.Kind != runKindGroupChat {
				return "", errors.New("you were asked directly; reply with your result (or follow_up if you depend on another Bot)")
			}
			if _, done, err := s.store.finishRun(r.ID, c.ID, r.BotID, "", true); err != nil {
				return "", err
			} else if !done {
				return "", errors.New("run is no longer active")
			}
			s.mu.Lock()
			cancel := s.runs[r.ID]
			s.mu.Unlock()
			if cancel != nil {
				cancel()
			}
			return "Turn completed without a message.", nil
		}
		return "", errors.New("disposition must be reply, silent, concluded or follow_up")
	}}}
}

func (s *Server) registerWait(c Conversation, r Run, waitFor string) error {
	lineage := lineageKey(s.store.db, r.ID)
	if _, err := s.store.db.Exec(`INSERT OR REPLACE INTO run_waits(run_id,conversation_id,bot_id,wait_for,lineage,fired,created_at) VALUES(?,?,?,?,?,0,?)`, r.ID, c.ID, r.BotID, waitFor, lineage, now()); err != nil {
		return err
	}
	timeout := s.waitTimeout
	if timeout <= 0 {
		timeout = defaultWaitTimeout
	}
	time.AfterFunc(timeout, func() {
		s.fireWait(r.ID, "", "The wait timed out: the Bot you were waiting for has not posted. Say plainly what is still missing, or do your part with what is available.")
	})
	return nil
}

// fireWait wakes a registered waiter exactly once. msgID empty means timeout.
func (s *Server) fireWait(waitRunID, msgID, request string) {
	res, err := s.store.db.Exec(`UPDATE run_waits SET fired=1 WHERE run_id=? AND fired=0`, waitRunID)
	if err != nil {
		return
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return
	}
	var conv, bot string
	if s.store.db.QueryRow(`SELECT conversation_id,bot_id FROM run_waits WHERE run_id=?`, waitRunID).Scan(&conv, &bot) != nil {
		return
	}
	waiter, err := s.store.GetRun(waitRunID)
	if err != nil {
		return
	}
	if msgID == "" {
		if s.store.db.QueryRow(`SELECT id FROM messages WHERE conversation_id=? AND kind<>'bot_result' ORDER BY seq DESC LIMIT 1`, conv).Scan(&msgID) != nil {
			return
		}
	}
	c, err := s.store.GetConversation(conv)
	if err != nil {
		return
	}
	if wake, ok, err := s.store.addMentionWake(conv, waiter, msgID, bot, request, true); err == nil && ok {
		s.enqueue(c, wake)
	} else if err != nil {
		log.Printf("[run] wait wake %s: %v", waitRunID, err)
	}
}

// applyDisposition runs once a group run has published its final message:
// explicit wake entries start their Bots, and a post from a Bot someone
// registered to wait for wakes that waiter once.
func (s *Server) applyDisposition(c Conversation, r Run, msg Message) {
	v, _ := s.dispositions.LoadAndDelete(r.ID)
	if c.Kind != "group" || !c.UserVisible || msg.ID == "" {
		return
	}
	if choice, ok := v.(groupChoice); ok && choice.disposition == dispReply {
		for _, w := range choice.wakes {
			wake, ok, err := s.store.AddMentionWake(c.ID, r, msg.ID, w.BotID, strings.TrimSpace(w.Request))
			if err != nil {
				log.Printf("[run] mention wake %s -> %s: %v", r.ID, w.BotID, err)
				continue
			}
			if ok {
				s.enqueue(c, wake)
			}
		}
	}
	rows, err := s.store.db.Query(`SELECT run_id FROM run_waits WHERE conversation_id=? AND wait_for=? AND fired=0 AND bot_id<>? AND lineage=?`, c.ID, r.BotID, r.BotID, lineageKey(s.store.db, r.ID))
	if err != nil {
		return
	}
	var waiters []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			waiters = append(waiters, id)
		}
	}
	rows.Close()
	name := r.BotID
	if b, e := s.store.GetBot(r.BotID); e == nil {
		name = b.Name
	}
	for _, id := range waiters {
		s.fireWait(id, msg.ID, fmt.Sprintf("%s posted the content you were waiting for (see the message above). Continue your part with it.", name))
	}
}
