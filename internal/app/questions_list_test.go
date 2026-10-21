package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// seedSettledApprovals inserts n answered approvals that each carry a large
// payload and facts, one minute apart, ending an hour ago.
func seedSettledApprovals(t *testing.T, s *Store, c Conversation, run Run, n int) {
	t.Helper()
	big := strings.Repeat("x", 20000)
	base := time.Now().Add(-time.Hour)
	for i := 0; i < n; i++ {
		at := stampTime(base.Add(time.Duration(i-n) * time.Minute))
		approval, _ := json.Marshal(ApprovalDetails{Action: "call", Target: fmt.Sprint("tool", i), Impact: "none", Payload: big, Facts: &ApprovalFacts{Kind: "mcp_create", Name: "n", Source: big}, Review: &MCPReviewDisplay{Source: "auto-review", Status: "approved", Reason: "ok"}})
		if _, err := s.db.Exec(`INSERT INTO questions(id,run_id,conversation_id,bot_id,type,prompt,options_json,min_selections,max_selections,status,answer_json,answered_by,created_at,updated_at,fields_json,source_url,allow_other,approval_json,private_input,handover_target) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			uuid.NewString(), run.ID, c.ID, run.BotID, questionApproval, "Allow?", "[]", 0, 0, questionAnswered, "true", "human", at, at, "[]", "", false, string(approval), false, ""); err != nil {
			t.Fatal(err)
		}
	}
}

func listQuestionsHTTP(t *testing.T, server *Server, target, etag string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	rec := httptest.NewRecorder()
	if !server.routeQuestions(rec, req, "questions") {
		t.Fatal("route not handled")
	}
	return rec
}

func TestQuestionListIsCompactForSettledAndFullForPending(t *testing.T) {
	s, c, run := questionFixture(t)
	defer s.Close()
	server := &Server{store: s}
	seedSettledApprovals(t, s, c, run, 120)
	in, err := normalizeQuestionInput(askQuestionInput{Question: "Restart now?", Type: questionApproval, Approval: &ApprovalDetails{Action: "Restart", Target: "vm", Impact: "down"}})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.CreateQuestion(c.ID, run, in)
	if err != nil {
		t.Fatal(err)
	}
	// Give the pending card a payload as the internal gate does.
	pendingApproval := *pending.Approval
	pendingApproval.Payload = `{"a":1}`
	raw, _ := json.Marshal(pendingApproval)
	if _, err = s.db.Exec(`UPDATE questions SET approval_json=? WHERE id=?`, string(raw), pending.ID); err != nil {
		t.Fatal(err)
	}

	rec := listQuestionsHTTP(t, server, "/api/questions?conversation_id="+c.ID, "")
	if rec.Code != 200 {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}
	var body struct {
		Questions []QuestionCard `json:"questions"`
	}
	if err = json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Questions) != questionListRecent+1 {
		t.Fatalf("settled not bounded: %d rows", len(body.Questions))
	}
	if rec.Body.Len() > 60_000 {
		t.Fatalf("list is %d bytes", rec.Body.Len())
	}
	for _, card := range body.Questions {
		if card.QuestionID == pending.ID {
			if card.DetailOmitted || card.Approval == nil || card.Approval.Payload != `{"a":1}` {
				t.Fatalf("pending question not full: %+v", card)
			}
			continue
		}
		if !card.DetailOmitted || card.Approval == nil || card.Approval.Payload != "" || card.Approval.Facts != nil {
			t.Fatalf("settled question not compact: %+v", card.Approval)
		}
		if card.Approval.Action == "" || card.Approval.Target == "" || card.Approval.Review == nil || card.Status != questionAnswered || string(card.Answer) != "true" || card.Question == "" {
			t.Fatalf("compact row lost a collapsed-state field: %+v", card)
		}
	}

	// A caller naming its history start gets settled rows since then, capped.
	since := stampTime(time.Now().Add(-4 * time.Hour))
	rec = listQuestionsHTTP(t, server, "/api/questions?conversation_id="+c.ID+"&since="+since, "")
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Questions) != 121 {
		t.Fatalf("since window returned %d rows", len(body.Questions))
	}
	if rec = listQuestionsHTTP(t, server, "/api/questions?conversation_id="+c.ID+"&since=nope", ""); rec.Code != 400 {
		t.Fatalf("bad since accepted: %d", rec.Code)
	}
}

func TestQuestionDetailReturnsWhatTheListOmitted(t *testing.T) {
	s, c, run := questionFixture(t)
	defer s.Close()
	server := &Server{store: s}
	seedSettledApprovals(t, s, c, run, 1)
	qs, _ := s.ListQuestions(c.ID)
	id := qs[0].ID
	req := httptest.NewRequest(http.MethodGet, "/api/questions/"+id, nil)
	rec := httptest.NewRecorder()
	if !server.routeQuestions(rec, req, "questions/"+id) || rec.Code != 200 {
		t.Fatalf("detail: %d %s", rec.Code, rec.Body)
	}
	var body struct {
		Question QuestionCard `json:"question"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Question.DetailOmitted || len(body.Question.Approval.Payload) != 20000 || body.Question.Approval.Facts == nil {
		t.Fatalf("detail is not full: %+v", body.Question.Approval)
	}
	rec = httptest.NewRecorder()
	server.routeQuestions(rec, httptest.NewRequest(http.MethodGet, "/api/questions/missing", nil), "questions/missing")
	if rec.Code != 404 {
		t.Fatalf("missing detail: %d", rec.Code)
	}
}

func TestQuestionListRevalidatesWith304(t *testing.T) {
	s, c, run := questionFixture(t)
	defer s.Close()
	server := &Server{store: s}
	seedSettledApprovals(t, s, c, run, 3)
	target := "/api/questions?conversation_id=" + c.ID
	first := listQuestionsHTTP(t, server, target, "")
	etag := first.Header().Get("ETag")
	if etag == "" || first.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("missing validators: %q %q", etag, first.Header().Get("Cache-Control"))
	}
	again := listQuestionsHTTP(t, server, target, etag)
	if again.Code != http.StatusNotModified || again.Body.Len() != 0 {
		t.Fatalf("unchanged list: %d, %d bytes", again.Code, again.Body.Len())
	}
	in, _ := normalizeQuestionInput(askQuestionInput{Question: "Name?", Type: questionText})
	if _, err := s.CreateQuestion(c.ID, run, in); err != nil {
		t.Fatal(err)
	}
	if changed := listQuestionsHTTP(t, server, target, etag); changed.Code != 200 || changed.Header().Get("ETag") == etag {
		t.Fatalf("changed list served stale: %d", changed.Code)
	}
}
