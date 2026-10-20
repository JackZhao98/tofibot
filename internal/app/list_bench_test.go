package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// seedLargeAccount builds an account shaped like the production one that hung
// the loading spinner: 90 Bots (85 archived), 104 conversations (99 archived)
// and a long message and run history.
func seedLargeAccount(tb testing.TB, perConversation int) *Store {
	tb.Helper()
	s, err := openStoreWithLimit(tb.TempDir(), false, 0)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { s.Close() })
	tx, err := s.db.Begin()
	if err != nil {
		tb.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	body := strings.Repeat("lorem ipsum dolor sit amet ", 20)
	for i := 0; i < 104; i++ {
		archived := 0
		if i >= 5 {
			archived = 1
		}
		conv := fmt.Sprintf("conv_%03d", i)
		bot := fmt.Sprintf("bot_%03d", i)
		ts := stampTime(base.Add(time.Duration(i) * time.Hour))
		if i < 90 {
			if _, err = tx.Exec(`INSERT INTO bots(id,name,instructions,model,dm_conversation_id,created_at,archived) VALUES(?,?,?,?,?,?,?)`, bot, "Bot"+bot, "do things", "default", conv, ts, archived); err != nil {
				tb.Fatal(err)
			}
		}
		botArg := any(nil)
		kind := "group"
		if i < 90 {
			botArg, kind = bot, "dm"
		}
		if _, err = tx.Exec(`INSERT INTO conversations(id,kind,name,bot_id,updated_at,archived,user_visible) VALUES(?,?,?,?,?,?,1)`, conv, kind, "Conv "+conv, botArg, ts, archived); err != nil {
			tb.Fatal(err)
		}
		if i < 90 {
			if _, err = tx.Exec(`INSERT INTO members(conversation_id,bot_id) VALUES(?,?)`, conv, bot); err != nil {
				tb.Fatal(err)
			}
		}
		for m := 1; m <= perConversation; m++ {
			role, runID := "user", any(nil)
			if m%2 == 0 {
				role = "assistant"
				runID = fmt.Sprintf("run_%03d_%05d", i, m)
			}
			mts := stampTime(base.Add(time.Duration(i)*time.Hour + time.Duration(m)*time.Second))
			if _, err = tx.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,run_id,content,created_at) VALUES(?,?,?,?,?,?,?,?)`,
				fmt.Sprintf("msg_%03d_%05d", i, m), conv, m, role, "", runID, body, mts); err != nil {
				tb.Fatal(err)
			}
			if role == "assistant" && i < 90 {
				if _, err = tx.Exec(`INSERT INTO runs(id,conversation_id,bot_id,status,origin_conversation_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, runID, conv, bot, "done", conv, mts, mts); err != nil {
					tb.Fatal(err)
				}
			}
		}
	}
	if err = tx.Commit(); err != nil {
		tb.Fatal(err)
	}
	return s
}

func BenchmarkListConversationsLarge(b *testing.B) {
	s := seedLargeAccount(b, 1500)
	for _, include := range []bool{true, false} {
		b.Run(fmt.Sprintf("archived=%v", include), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.ListConversations(include); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// A conversation like the owner's main one: 73 tool activities over 20 runs
// whose results total ~480 KB. The list the UI loads on every open must stay small.
func TestToolActivityListPayloadOnBusyConversation(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("bot", "", "model")
	if err = migrateToolActivity(s.store.db); err != nil {
		t.Fatal(err)
	}
	result := strings.Repeat("r", 6600)
	for i := 0; i < 73; i++ {
		if _, err = s.store.db.Exec(`INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,arguments,result,status,started_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			b.DMConversationID, b.ID, fmt.Sprintf("run-%02d", i%20), fmt.Sprintf("call-%02d", i), "tofi_shell", `{"command":"ls"}`, result, "completed",
			fmt.Sprintf("2026-10-01T00:%02d:00Z", i%60), fmt.Sprintf("2026-10-01T01:%02d:00Z", i%60)); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/conversations/"+b.DMConversationID+"/tools", nil)
	res := httptest.NewRecorder()
	s.routeToolActivities(res, req, "conversations/"+b.DMConversationID+"/tools")
	full := 73 * len(result)
	t.Logf("list payload %d bytes (full results would be >= %d)", res.Body.Len(), full)
	if res.Body.Len() > 60*1024 {
		t.Fatalf("list payload %d bytes", res.Body.Len())
	}
}
