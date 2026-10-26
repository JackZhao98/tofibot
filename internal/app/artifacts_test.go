package app

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type artifactList struct {
	Items      []ArtifactItem `json:"items"`
	NextCursor string         `json:"next_cursor"`
}

// publishArtifact stores a file and binds it to a new assistant (or user)
// message, the state a finished publish_file / generate_image run leaves.
func publishArtifact(t *testing.T, s *Store, conv, role, bot, name, mime, at string) Attachment {
	t.Helper()
	body := []byte("synthetic " + name + at)
	if strings.HasPrefix(mime, "image/") {
		var png bytes.Buffer
		if err := pngEncode(&png); err != nil {
			t.Fatal(err)
		}
		body = png.Bytes()
	}
	a, err := s.AddAttachment(conv, name, mime, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	sender := bot
	if role == "user" {
		sender = ""
	}
	m, _, err := s.AddMessage(conv, role, sender, "", "file "+name, newID())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BindAttachments(conv, m.ID, []string{a.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE attachments SET created_at=? WHERE id=?`, at, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE messages SET created_at=? WHERE id=?`, at, m.ID); err != nil {
		t.Fatal(err)
	}
	return a
}

func pngEncode(w io.Writer) error { return png.Encode(w, image.NewRGBA(image.Rect(0, 0, 2, 2))) }

// insertArtifactRow writes the same rows without touching file storage, for
// account workspaces whose file store is not set up in a unit test.
func insertArtifactRow(t *testing.T, s *Store, conv, bot, name string) string {
	t.Helper()
	id, mid := newID(), newID()
	at := "2026-01-01T00:00:01Z"
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO attachments(id,conversation_id,name,mime,size,disk_name,created_at) VALUES(?,?,?,?,?,?,?)`, []any{id, conv, name, "text/plain", 3, id + ".upload", at}},
		{`INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,content,created_at) VALUES(?,?,1,'assistant','',?,?,?)`, []any{mid, conv, bot, "file", at}},
		{`INSERT INTO attachment_messages(attachment_id,message_id) VALUES(?,?)`, []any{id, mid}},
	} {
		if _, err := s.db.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func getArtifacts(t *testing.T, srv *Server, query string) artifactList {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/artifacts?"+query, nil)
	if !srv.routeArtifacts(w, r, "artifacts") || w.Code != 200 {
		t.Fatalf("GET %s: %d %s", query, w.Code, w.Body.String())
	}
	var out artifactList
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func artifactNames(l artifactList) string {
	names := make([]string, len(l.Items))
	for i, it := range l.Items {
		names[i] = it.Name
	}
	return strings.Join(names, ",")
}

func TestArtifactsScopesUploadsAndVersions(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	srv := &Server{store: s}
	b1, _ := s.CreateBot("one", "", "m")
	b2, _ := s.CreateBot("two", "", "m")
	group, err := s.CreateGroup("team", []string{b1.ID, b2.ID})
	if err != nil {
		t.Fatal(err)
	}
	publishArtifact(t, s, b1.DMConversationID, "assistant", b1.ID, "report.md", "text/markdown", "2026-01-01T00:00:01Z")
	v2 := publishArtifact(t, s, b1.DMConversationID, "assistant", b1.ID, "report.md", "text/markdown", "2026-01-01T00:00:03Z")
	publishArtifact(t, s, b1.DMConversationID, "assistant", b1.ID, "chart.png", "image/png", "2026-01-01T00:00:02Z")
	publishArtifact(t, s, b2.DMConversationID, "assistant", b2.ID, "report.md", "text/markdown", "2026-01-01T00:00:04Z")
	publishArtifact(t, s, group.ID, "assistant", b2.ID, "plan.pdf", "application/pdf", "2026-01-01T00:00:05Z")
	publishArtifact(t, s, group.ID, "user", "", "mine.txt", "text/plain", "2026-01-01T00:00:06Z")
	// A file with no message (a replaced image, a superseded publish) is not listed.
	if _, err = s.AddAttachment(group.ID, "orphan.png", "image/png", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}

	all := getArtifacts(t, srv, "scope=all")
	if got := artifactNames(all); got != "plan.pdf,report.md,report.md,chart.png" {
		t.Fatalf("all: %s", got)
	}
	// Newest first; the bot-one report is one group of two versions.
	for _, it := range all.Items {
		if it.BotID == b1.ID && it.Name == "report.md" && (it.ID != v2.ID || it.VersionCount != 2) {
			t.Fatalf("latest version not shown: %+v", it)
		}
		if it.Uploaded || it.MessageID == "" {
			t.Fatalf("bad item %+v", it)
		}
	}
	if got := artifactNames(getArtifacts(t, srv, "scope=all&include_uploads=1")); got != "mine.txt,plan.pdf,report.md,report.md,chart.png" {
		t.Fatalf("uploads: %s", got)
	}
	if got := artifactNames(getArtifacts(t, srv, "scope=bot&id="+b1.ID)); got != "report.md,chart.png" {
		t.Fatalf("bot: %s", got)
	}
	if got := artifactNames(getArtifacts(t, srv, "scope=bot&id="+b2.ID+"&include_uploads=1")); got != "mine.txt,plan.pdf,report.md" {
		t.Fatalf("bot uploads: %s", got)
	}
	if got := artifactNames(getArtifacts(t, srv, "scope=conversation&id="+group.ID)); got != "plan.pdf" {
		t.Fatalf("conversation: %s", got)
	}
	if got := artifactNames(getArtifacts(t, srv, "scope=all&type=image")); got != "chart.png" {
		t.Fatalf("images: %s", got)
	}
	if got := artifactNames(getArtifacts(t, srv, "scope=all&type=document&q="+url.QueryEscape("REP"))); got != "report.md,report.md" {
		t.Fatalf("documents+search: %s", got)
	}
	// Paging by cursor, newest first.
	page := getArtifacts(t, srv, "scope=all&limit=2")
	if len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatalf("page 1: %+v", page)
	}
	rest := getArtifacts(t, srv, "scope=all&limit=2&cursor="+url.QueryEscape(page.NextCursor))
	if artifactNames(page)+","+artifactNames(rest) != "plan.pdf,report.md,report.md,chart.png" {
		t.Fatalf("paging: %s | %s", artifactNames(page), artifactNames(rest))
	}
	// All versions of one group, newest first.
	versions := getArtifacts(t, srv, "scope=versions&id="+v2.ID)
	if len(versions.Items) != 2 || versions.Items[0].ID != v2.ID || versions.Items[0].VersionGroup != versions.Items[1].VersionGroup {
		t.Fatalf("versions: %+v", versions.Items)
	}
	// Archived conversations stay, marked.
	if _, err = s.db.Exec(`UPDATE conversations SET archived=1 WHERE id=?`, group.ID); err != nil {
		t.Fatal(err)
	}
	listed := getArtifacts(t, srv, "scope=conversation&id="+group.ID).Items
	if len(listed) != 1 || !listed[0].ConversationArchived {
		t.Fatalf("archived not marked: %+v", listed)
	}
	for _, bad := range []string{"scope=nope", "scope=bot", "scope=bot&id=missing", "scope=conversation&id=missing", "type=video", "cursor=!!"} {
		w := httptest.NewRecorder()
		srv.routeArtifacts(w, httptest.NewRequest(http.MethodGet, "/api/artifacts?"+bad, nil), "artifacts")
		if w.Code < 400 {
			t.Fatalf("%s: %d", bad, w.Code)
		}
	}
	w := httptest.NewRecorder()
	srv.routeArtifacts(w, httptest.NewRequest(http.MethodPost, "/api/artifacts", nil), "artifacts")
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", w.Code)
	}
}

func TestArtifactsHiddenTraceCopiesAndDelegation(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	srv := &Server{store: s}
	b1, _ := s.CreateBot("one", "", "m")
	b2, _ := s.CreateBot("two", "", "m")
	hidden, err := s.CreateGroup("trace", []string{b1.ID, b2.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE conversations SET user_visible=0 WHERE id=?`, hidden.ID); err != nil {
		t.Fatal(err)
	}
	publishArtifact(t, s, hidden.ID, "assistant", b1.ID, "secret-trace.txt", "text/plain", "2026-01-01T00:00:01Z")
	if got := artifactNames(getArtifacts(t, srv, "scope=all&include_uploads=1")); got != "" {
		t.Fatalf("hidden trace listed: %s", got)
	}
	w := httptest.NewRecorder()
	srv.routeArtifacts(w, httptest.NewRequest(http.MethodGet, "/api/artifacts?scope=conversation&id="+hidden.ID, nil), "artifacts")
	if w.Code != 404 {
		t.Fatalf("hidden conversation scope: %d", w.Code)
	}
	// A deliverable later bound to a delegation brief in another conversation
	// and to a bot_result copy stays one artifact, at its first message.
	orig := publishArtifact(t, s, b1.DMConversationID, "assistant", b1.ID, "deck.pdf", "application/pdf", "2026-01-01T00:00:02Z")
	brief, _, err := s.AddMessage(b2.DMConversationID, "assistant", b1.ID, "", "brief", newID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO attachment_messages(attachment_id,message_id) VALUES(?,?)`, orig.ID, brief.ID); err != nil {
		t.Fatal(err)
	}
	copyMsg, _, err := s.AddMessage(b1.DMConversationID, "assistant", b1.ID, "", "copy", newID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO attachment_messages(attachment_id,message_id) VALUES(?,?)`, orig.ID, copyMsg.ID); err != nil {
		t.Fatal(err)
	}
	l := getArtifacts(t, srv, "scope=all&include_uploads=1")
	if len(l.Items) != 1 || l.Items[0].ID != orig.ID || l.Items[0].ConversationID != b1.DMConversationID {
		t.Fatalf("copies listed: %+v", l.Items)
	}
	if len(getArtifacts(t, srv, "scope=conversation&id="+b2.DMConversationID).Items) != 0 {
		t.Fatal("brief copy listed in the recipient conversation")
	}
}

func TestArtifactsAccountIsolation(t *testing.T) {
	g := accountFixture(t)
	var cookies []*http.Cookie
	var convs, atts []string
	for _, name := range []string{"first", "second"} {
		a, err := g.create(context.Background(), name, "", "SyntheticPassword123!", false, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0 WHERE id=?`, a.ID); err != nil {
			t.Fatal(err)
		}
		ws, err := g.workspace(a)
		if err != nil {
			t.Fatal(err)
		}
		bot, err := ws.store.CreateBot("bot-"+name, "", "m")
		if err != nil {
			t.Fatal(err)
		}
		att := insertArtifactRow(t, ws.store, bot.DMConversationID, bot.ID, name+"-only.txt")
		cookies = append(cookies, accountCookie(t, g, a))
		convs = append(convs, bot.DMConversationID)
		atts = append(atts, att)
	}
	for i, other := range []int{1, 0} {
		w := accountRequest(g, http.MethodGet, "/api/artifacts?scope=all&include_uploads=1", "", cookies[i])
		if w.Code != 200 || !strings.Contains(w.Body.String(), atts[i]) || strings.Contains(w.Body.String(), atts[other]) {
			t.Fatalf("account %d list: %d %s", i, w.Code, w.Body.String())
		}
		if w = accountRequest(g, http.MethodGet, "/api/artifacts?scope=conversation&id="+convs[other], "", cookies[i]); w.Code != 404 {
			t.Fatalf("account %d read other conversation: %d %s", i, w.Code, w.Body.String())
		}
		if w = accountRequest(g, http.MethodGet, "/api/artifacts?scope=versions&id="+atts[other], "", cookies[i]); w.Code != 404 {
			t.Fatalf("account %d read other versions: %d", i, w.Code)
		}
	}
	if w := accountRequest(g, http.MethodGet, "/api/artifacts?scope=all", "", nil); w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
		t.Fatalf("anonymous: %d", w.Code)
	}
}
