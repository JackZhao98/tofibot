package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReviewPortableMembershipChangeRemainsExportable(t *testing.T) {
	s, b := portableFixture(t)
	var group portableConversation
	for _, c := range b.Conversations {
		if c.Kind == "group" {
			group = c
		}
	}
	var historicalBot string
	for _, m := range b.Memories {
		if m.ConversationID == group.ID {
			historicalBot = m.BotID
		}
	}
	if _, _, err := s.AddMessage(group.ID, "assistant", historicalBot, "", "Historical reply by former member.", "review-historical-message"); err != nil {
		t.Fatal(err)
	}
	replacement, err := s.CreateBot("Replacement member", "synthetic", "synthetic-model")
	if err != nil {
		t.Fatal(err)
	}
	members := []string{replacement.ID}
	for _, id := range group.BotIDs {
		if id != historicalBot {
			members = append(members, id)
		}
	}
	if _, err = s.UpdateGroup(group.ID, GroupUpdate{BotIDs: &members}); err != nil {
		t.Fatal(err)
	}
	for _, category := range []string{"chats", "memories", "schedules"} {
		t.Run(category, func(t *testing.T) {
			_, err := s.exportPortable(context.Background(), "synthetic-review", portableSelection{Categories: []string{"bot_config", category}}, "account")
			if err != nil {
				t.Fatalf("normal member replacement makes %s export fail: %v", category, err)
			}
		})
	}
}

func TestReviewPortableSettingsRetryDoesNotReapply(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := parsePortableBundle([]byte(portableLegacyFixture))
	if err != nil {
		t.Fatal(err)
	}
	b.Kind = "account"
	b.Included = append(b.Included, "settings")
	b.Settings = &portableSettings{Timezone: "UTC", Model: "imported-original", ReasoningEffort: "low"}
	b.Counts = b.counts()
	p, err := s.previewPortable(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(b)
	body, _ := json.Marshal(portableImportRequest{Bundle: raw, Selection: portableSelection{Categories: b.Included}, PreviewID: p.ID})
	server := &Server{store: s, defaultModel: "destination-original"}
	apply := func() {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/portability/apply", strings.NewReader(string(body)))
		server.routePortability(w, r, "portability/apply")
		if w.Code != 200 {
			t.Fatalf("apply: %d %s", w.Code, w.Body.String())
		}
	}
	apply()
	updated := modelSettings{Model: "user-new-choice", ReasoningEffort: "medium"}
	if err = s.putModelSettings(updated); err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	server.defaultModel = updated.Model
	server.defaultReasoning = updated.ReasoningEffort
	server.mu.Unlock()
	apply()
	durable, err := s.getModelSettings()
	if err != nil {
		t.Fatal(err)
	}
	cached, reasoning := server.modelDefaults()
	if cached != durable.Model || reasoning != durable.ReasoningEffort {
		t.Fatalf("idempotent retry changed live defaults: DB=%+v, live=%s/%s", durable, cached, reasoning)
	}
}

func TestReviewPortableRejectConflictingFieldAlias(t *testing.T) {
	b, _ := parsePortableBundle([]byte(portableLegacyFixture))
	raw, _ := json.Marshal(b)
	source := strings.Replace(string(raw), `"name":"Synthetic Bot"`, `"name":"Synthetic Bot","NAME":"Different Bot"`, 1)
	got, err := parsePortableBundle([]byte(source))
	if err == nil {
		t.Fatalf("ambiguous name accepted; browser name=Synthetic Bot, server name=%s", got.Bots[0].Name)
	}
}

func TestPortableHistoricalSelectionClosureAndOrphanProvenance(t *testing.T) {
	s, b := portableFixture(t)
	var group portableConversation
	for _, c := range b.Conversations {
		if c.Kind == "group" {
			group = c
		}
	}
	former := group.BotIDs[0]
	replacement, e := s.CreateBot("Replacement", "data only", "synthetic-model")
	if e != nil {
		t.Fatal(e)
	}
	members := []string{replacement.ID, group.BotIDs[1]}
	if _, e = s.UpdateGroup(group.ID, GroupUpdate{BotIDs: &members}); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.AddMessage(group.ID, "assistant", former, "", "Former member fact.", "former-fact"); e != nil {
		t.Fatal(e)
	}
	exported, e := s.exportPortable(context.Background(), "source", portableSelection{Categories: []string{"bot_config", "chats", "memories", "schedules"}, BotIDs: members}, "account")
	if e != nil {
		t.Fatal(e)
	}
	chosen, e := selectPortable(exported, portableSelection{BotIDs: members})
	if e != nil {
		t.Fatal(e)
	}
	if len(chosen.Bots) != 3 {
		t.Fatalf("historical dependency missing: %d", len(chosen.Bots))
	}
	for _, c := range chosen.Conversations {
		if c.ID == group.ID {
			for _, id := range c.BotIDs {
				if id == former {
					t.Fatal("former Bot added to current group")
				}
			}
		}
	}
	dst, e := OpenStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer dst.Close()
	p, e := dst.previewPortable(context.Background(), chosen)
	if e != nil {
		t.Fatal(e)
	}
	result, e := dst.applyPortable(context.Background(), chosen, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	var n int
	if e = dst.db.QueryRow(`SELECT COUNT(*) FROM members WHERE conversation_id=? AND bot_id=?`, result.IDMap[group.ID], result.IDMap[former]).Scan(&n); e != nil || n != 0 {
		t.Fatal("membership changed", e)
	}
	orphan := "00000000-0000-0000-0000-000000000077"
	if _, e = s.db.Exec(`UPDATE messages SET sender_bot_id=? WHERE client_message_id='former-fact'`, orphan); e != nil {
		t.Fatal(e)
	}
	exported, e = s.exportPortable(context.Background(), "source", portableSelection{Categories: []string{"bot_config", "chats"}}, "account")
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, m := range exported.Messages {
		if m.Content == "Former member fact." {
			found = m.SenderBotID == "" && m.Origin.SenderBotID == orphan
		}
	}
	if !found {
		t.Fatal("deleted source sender lost provenance")
	}
}

func TestPortableCanonicalNamesAtEveryProtocolLevel(t *testing.T) {
	b, e := parsePortableBundle([]byte(portableLegacyFixture))
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(b)
	for _, pair := range [][2]string{{`"format":`, `"Format":`}, {`"name":`, `"NAME":`}, {`"created_at":`, `"Created_At":`}, {`"record_id":`, `"RECORD_ID":`}} {
		if _, e = parsePortableBundle([]byte(strings.Replace(string(raw), pair[0], pair[1], 1))); e == nil {
			t.Fatalf("alias accepted: %s", pair[1])
		}
	}
	var request portableImportRequest
	body := `{"bundle":` + string(raw) + `,"Selection":{"categories":["bot_config"]}}`
	if e = portableJSON([]byte(body), &request); e == nil {
		t.Fatal("request alias accepted")
	}
	b.Bots[0].Avatar = json.RawMessage(`{"UserChosenMetadata":"Preserved"}`)
	raw, _ = json.Marshal(b)
	if _, e = parsePortableBundle(raw); e != nil {
		t.Fatal("opaque user metadata changed", e)
	}
}

func TestPortableSettingsImportAndOrdinaryWritesStayOrdered(t *testing.T) {
	for i := 0; i < 12; i++ {
		s, e := OpenStore(t.TempDir())
		if e != nil {
			t.Fatal(e)
		}
		b, e := parsePortableBundle([]byte(portableLegacyFixture))
		if e != nil {
			t.Fatal(e)
		}
		b.Kind = "account"
		b.Included = append(b.Included, "settings")
		b.Settings = &portableSettings{Timezone: "UTC", Model: "imported", ReasoningEffort: "low"}
		b.Counts = b.counts()
		p, e := s.previewPortable(context.Background(), b)
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(b)
		body, _ := json.Marshal(portableImportRequest{Bundle: raw, Selection: portableSelection{Categories: b.Included}, PreviewID: p.ID})
		server := &Server{store: s, provider: "local", defaultModel: "before", modelCatalog: []ModelOption{{ID: "ordinary", ReasoningEfforts: []string{"medium"}}}, modelCatalogAt: time.Now(), modelCatalogSource: "synthetic"}
		var wg sync.WaitGroup
		start := make(chan struct{})
		var applyCode, settingsCode int
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/api/portability/apply", strings.NewReader(string(body)))
			server.routePortability(w, r, "portability/apply")
			applyCode = w.Code
		}()
		go func() {
			defer wg.Done()
			<-start
			w := httptest.NewRecorder()
			r := httptest.NewRequest("PUT", "/api/model-settings", strings.NewReader(`{"model":"ordinary","reasoning_effort":"medium"}`))
			server.modelSettings(w, r)
			settingsCode = w.Code
		}()
		close(start)
		wg.Wait()
		if (applyCode != 200 && applyCode != 409) || settingsCode != 200 {
			t.Fatalf("unexpected codes: %d %d", applyCode, settingsCode)
		}
		saved, e := s.getModelSettings()
		if e != nil {
			t.Fatal(e)
		}
		model, reasoning := server.modelDefaults()
		if model != saved.Model || reasoning != saved.ReasoningEffort {
			t.Fatalf("durable/cache order diverged: %+v vs %s/%s", saved, model, reasoning)
		}
		s.Close()
	}
}

func TestPortableStandalonePreservesForeignSenderAsProvenanceOnly(t *testing.T) {
	s, b := portableFixture(t)
	own, other := b.Bots[0], b.Bots[1]
	if _, _, e := s.AddMessage(own.DMConversationID, "assistant", other.ID, "", "Historical forwarded fact.", "foreign-dm"); e != nil {
		t.Fatal(e)
	}
	exported, e := s.exportPortable(context.Background(), "source", portableSelection{Categories: []string{"bot_config", "chats"}, BotIDs: []string{own.ID}}, "bot")
	if e != nil {
		t.Fatal(e)
	}
	if len(exported.Bots) != 1 || len(exported.Conversations) != 1 {
		t.Fatal("standalone scope expanded")
	}
	found := false
	for _, m := range exported.Messages {
		if m.Content == "Historical forwarded fact." {
			found = m.SenderBotID == "" && m.Origin.SenderBotID == other.ID
		}
	}
	if !found {
		t.Fatal("standalone foreign sender provenance lost")
	}
}
