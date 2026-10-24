package app

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const (
	maxGroupPurposeRunes  = 200
	maxGroupGateMatches   = 5
	maxGroupGateSnippet   = 120
	groupGateRetention    = 6 * time.Hour
	groupListFirstCode    = "group_list_first"
	groupListFirstMessage = "No group was created. Groups that already hold all requested members are listed below. To continue in one of them, call send_group_message. If the purpose differs from every listed group, call create_group again with the same arguments. Matching groups: "
	groupNoneMessage      = "No group was created. No existing group holds all requested members. Call create_group again with the same arguments to create it. Call list_groups first next time."
)

// groupListing is a group as list_groups and create_group show it.
type groupListing struct {
	Conversation
	Purpose string `json:"purpose,omitempty"`
}

// groupGate is the per-run record behind the "list before create" rule. It is
// kept in memory only: it must outlive nothing more than the run.
type groupGate struct {
	mu      sync.Mutex
	at      time.Time
	listed  bool
	refused bool
}

func (s *Server) groupGateFor(runID string) *groupGate {
	now := time.Now()
	s.groupGates.Range(func(k, v any) bool {
		if now.Sub(v.(*groupGate).at) > groupGateRetention {
			s.groupGates.Delete(k)
		}
		return true
	})
	v, _ := s.groupGates.LoadOrStore(runID, &groupGate{at: now})
	return v.(*groupGate)
}

func (s *Server) markGroupsListed(runID string) {
	g := s.groupGateFor(runID)
	g.mu.Lock()
	g.listed = true
	g.mu.Unlock()
}

// groupListFirst refuses the first create_group call of a run that has not
// called list_groups. Every later call in the run passes.
func (s *Server) groupListFirst(runID string, members []string) (error, error) {
	g := s.groupGateFor(runID)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.listed || g.refused {
		return nil, nil
	}
	matches, err := s.groupsHolding(members)
	if err != nil {
		return nil, err
	}
	g.refused = true
	body, err := json.Marshal(matches)
	if err != nil {
		return nil, err
	}
	msg := groupListFirstMessage + string(body)
	if len(matches) == 0 {
		msg = groupNoneMessage
	}
	return tooloutcome.New(tooloutcome.Validation, groupListFirstCode, "not_executed", msg, "repair_arguments").Err(), nil
}

type groupMatch struct {
	GroupID       string   `json:"group_id"`
	Name          string   `json:"name"`
	Members       []string `json:"members"`
	Purpose       string   `json:"purpose,omitempty"`
	LastMessageAt string   `json:"last_message_at,omitempty"`
	LastMessage   string   `json:"last_message,omitempty"`
}

// groupsHolding returns visible, non-archived groups that contain every
// member, newest first and bounded.
func (s *Server) groupsHolding(members []string) ([]groupMatch, error) {
	want := map[string]bool{}
	for _, m := range members {
		want[m] = true
	}
	rows, err := s.store.db.Query(`SELECT id,name,purpose FROM conversations WHERE kind='group' AND archived=0 AND user_visible=1 ORDER BY updated_at DESC,id`)
	if err != nil {
		return nil, err
	}
	type cand struct{ id, name, purpose string }
	var cands []cand
	for rows.Next() {
		var c cand
		if err = rows.Scan(&c.id, &c.name, &c.purpose); err != nil {
			rows.Close()
			return nil, err
		}
		cands = append(cands, c)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	out := make([]groupMatch, 0)
	for _, c := range cands {
		if len(out) >= maxGroupGateMatches {
			break
		}
		mr, err := s.store.db.Query(`SELECT b.id,b.name FROM members m JOIN bots b ON b.id=m.bot_id WHERE m.conversation_id=?`, c.id)
		if err != nil {
			return nil, err
		}
		have := 0
		names := []string{}
		for mr.Next() {
			var id, name string
			if err = mr.Scan(&id, &name); err != nil {
				mr.Close()
				return nil, err
			}
			names = append(names, name)
			if want[id] {
				have++
			}
		}
		if err = mr.Close(); err != nil {
			return nil, err
		}
		if have != len(want) {
			continue
		}
		sort.Strings(names)
		m := groupMatch{GroupID: c.id, Name: c.name, Members: names, Purpose: c.purpose}
		var at, content string
		e := s.store.db.QueryRow(`SELECT created_at,content FROM messages WHERE conversation_id=? AND role IN ('user','assistant') AND kind NOT IN ('notice','message_ref','bot_result','progress','internal','scheduled_task') ORDER BY seq DESC LIMIT 1`, c.id).Scan(&at, &content)
		if e == nil {
			m.LastMessageAt = at
			m.LastMessage = groupSnippet(content)
		}
		out = append(out, m)
	}
	return out, nil
}

func groupSnippet(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxGroupGateSnippet {
		return string(r[:maxGroupGateSnippet]) + "..."
	}
	return s
}
