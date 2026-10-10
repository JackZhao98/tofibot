package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
	"testing"

	"github.com/JackZhao98/tofibot/internal/provider"
)

// lockedLog captures log lines while background goroutines may still write.
type lockedLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}
func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func captureLog(t *testing.T) *lockedLog {
	t.Helper()
	sink := &lockedLog{}
	previous := log.Writer()
	log.SetOutput(sink)
	t.Cleanup(func() { log.SetOutput(previous) })
	return sink
}

func reviewFailureRow(t *testing.T, f *autoReviewFixture, id string) (status, category, detail string) {
	t.Helper()
	if err := f.s.store.db.QueryRow(`SELECT status,failure_category,failure_detail FROM mcp_auto_reviews WHERE question_id=?`, id).Scan(&status, &category, &detail); err != nil {
		t.Fatal(err)
	}
	return status, category, detail
}

// Every technical failure records its category and a content-free detail on
// the review row, in the stored reason and in exactly one log line per
// failed request. Provider bodies and arguments never reach any of them.
func TestAutoReviewFailureCategoriesStoredAndLogged(t *testing.T) {
	const secret = "private provider body must not leak"
	cases := []struct {
		name, category, detail string
		calls                  int32
	}{
		{"reviewer_not_configured", mcpReviewFailureNotConfigured, "", 0},
		{"input_too_large", mcpReviewFailureInputTooLarge, "_bytes_limit_", 0},
		{"provider_error", mcpReviewFailureProvider, "http_529", 1},
		{"timeout", mcpReviewFailureTimeout, "deadline_exceeded", 1},
		{"malformed_response", mcpReviewFailureMalformed, "missing_field", 2},
		{"empty_response", mcpReviewFailureEmpty, "no_text_with_reasoning", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAutoReviewFixture(t)
			sink := captureLog(t)
			f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
				switch tc.name {
				case "provider_error":
					return nil, provider.NewAPIError("anthropic", 529, secret)
				case "timeout":
					return nil, context.DeadlineExceeded
				case "malformed_response":
					resp := v5ReviewReply(req, "allow", "low", false)
					var data map[string]any
					_ = json.Unmarshal([]byte(resp.Content), &data)
					delete(data, "risk_level")
					raw, _ := json.Marshal(data)
					resp.Content = string(raw)
					return resp, nil
				case "empty_response":
					return &provider.ChatResponse{Reasoning: "thinking only"}, nil
				}
				t.Error("unexpected reviewer request")
				return nil, errors.New("unexpected")
			}
			switch tc.name {
			case "reviewer_not_configured":
				f.s.autoReviewProvider = nil // no Codex, no API key in the fixture
			case "input_too_large":
				raw, _ := json.Marshal(map[string]string{"target": strings.Repeat("x", mcpReviewInputLimit+1)})
				f.call.Arguments = raw
			}
			q := newMCPReviewProposal(t, f)
			if err := f.s.reviewNewMCPProposal(context.Background(), f.c, f.r, f.call, q); err == nil {
				t.Fatal("technical gap did not block the proposal")
			}
			if f.p.calls.Load() != tc.calls || f.effects.Load() != 0 {
				t.Fatalf("calls=%d effects=%d", f.p.calls.Load(), f.effects.Load())
			}
			status, category, detail := reviewFailureRow(t, f, q.ID)
			if status != "unavailable" || category != tc.category || !strings.Contains(detail, tc.detail) {
				t.Fatalf("row status=%s category=%s detail=%s", status, category, detail)
			}
			q, _ = f.s.store.GetQuestion(q.ID)
			review := q.Approval.Review
			if review.Status != "unavailable" || review.FailureCategory != tc.category || !strings.Contains(review.Reason, tc.category) || !strings.Contains(review.Reason, tc.detail) {
				t.Fatalf("display %+v", review)
			}
			logged := sink.String()
			want := "category=" + tc.category
			if strings.Count(logged, "[auto-review] reviewer failure") != max(int(tc.calls), 1) || !strings.Contains(logged, want) || !strings.Contains(logged, "run="+f.r.ID) || !strings.Contains(logged, "tool="+f.call.Tool) || !strings.Contains(logged, "model=codex-auto-review") || !strings.Contains(logged, "latency_ms=") {
				t.Fatalf("log lines missing %q: %s", want, logged)
			}
			for _, leak := range []string{secret, "xxxxxxxx", "alpha", "thinking only"} {
				if strings.Contains(logged, leak) || strings.Contains(review.Reason, leak) || strings.Contains(detail, leak) {
					t.Fatalf("content leaked into diagnostics: %q", leak)
				}
			}
		})
	}
}

// A malformed first answer earns one fresh request with the identical input;
// a valid second answer approves the call after exactly two provider calls.
func TestAutoReviewRetriesMalformedAnswerOnce(t *testing.T) {
	f := newAutoReviewFixture(t)
	_ = f.s.store.putAutoReviewMode("auto")
	var first string
	f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		if first == "" {
			first = req.Messages[0].Content
			return &provider.ChatResponse{Content: `{"decision":"allow"`}, nil
		}
		if req.Messages[0].Content != first || req.System != mcpAutoReviewPrompt {
			t.Error("retry changed the bound reviewer input")
		}
		return v5ReviewReply(req, "allow", "low", false), nil
	}
	if err := f.execute(context.Background()); err != nil || f.effects.Load() != 1 || f.p.calls.Load() != 2 {
		t.Fatalf("retry did not approve: err=%v effects=%d calls=%d", err, f.effects.Load(), f.p.calls.Load())
	}
	q := waitReviewQuestion(t, f, "approved")
	status, category, detail := reviewFailureRow(t, f, q.ID)
	if status != "approved" || category != "" || detail != "" || q.Approval.Review.FailureCategory != "" {
		t.Fatalf("approved review kept a failure: %s %s %s", status, category, detail)
	}
	var reviews int
	_ = f.s.store.db.QueryRow(`SELECT COUNT(*) FROM mcp_auto_reviews WHERE run_id=?`, f.r.ID).Scan(&reviews)
	if reviews != 1 {
		t.Fatalf("retry created %d reservations", reviews)
	}
}

// The retry stays inside the review budget: once the context is gone after a
// malformed answer, no second request is made.
func TestAutoReviewRetryRespectsBudget(t *testing.T) {
	f := newAutoReviewFixture(t)
	_ = f.s.store.putAutoReviewMode("auto")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		cancel()
		return &provider.ChatResponse{Content: "not json"}, nil
	}
	if err := f.execute(ctx); err == nil || f.effects.Load() != 0 || f.p.calls.Load() != 1 {
		t.Fatalf("retry escaped the budget: err=%v calls=%d", err, f.p.calls.Load())
	}
}

// A bare code fence around an otherwise valid object is harmless wrapping and
// is accepted; prose around the object, or a fence plus prose, is not.
func TestParseMCPReviewToleratesOnlyCodeFence(t *testing.T) {
	const digest = "d"
	valid := `{"decision":"allow","reason":"ok","context_digest":"d","risk_level":"low","confirmation_required":false}`
	for _, tc := range []struct{ name, content, rule string }{
		{"json fence", "```json\n" + valid + "\n```", ""},
		{"bare fence", "```\n" + valid + "\n```", ""},
		{"fence crlf", "```JSON\r\n" + valid + "\r\n```", ""},
		{"fence then prose", "```json\n" + valid + "\n```\nHope this helps.", "not_json"},
		{"prose then fence", "Here is my assessment:\n```json\n" + valid + "\n```", "not_json"},
		{"trailing prose", valid + "\nLet me know.", "trailing_text"},
		{"fenced digest mismatch", "```json\n" + strings.Replace(valid, `"d"`, `"x"`, 1) + "\n```", "digest_mismatch"},
		{"reason too long", strings.Replace(valid, `"ok"`, `"`+strings.Repeat("长", 201)+`"`, 1), "reason_too_long"},
		{"contradictory", strings.Replace(valid, `"low"`, `"unknown"`, 1), "contradictory"},
		{"duplicate", `{"decision":"deny","decision":"allow","reason":"x","context_digest":"d","risk_level":"low","confirmation_required":false}`, "duplicate_key"},
		{"unknown field", strings.Replace(valid, `"risk_level"`, `"extra":1,"risk_level"`, 1), "unknown_field"},
		{"wrong type", strings.Replace(valid, `false`, `"false"`, 1), "invalid_field_type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _, err := parseMCPReview(&provider.ChatResponse{Content: tc.content}, digest)
			var rule *mcpReviewParseError
			if tc.rule == "" {
				if err != nil || out.Decision != "allow" {
					t.Fatalf("harmless wrapping rejected: %v", err)
				}
				return
			}
			if !errors.As(err, &rule) || rule.Rule != tc.rule {
				t.Fatalf("rule=%v want %s", err, tc.rule)
			}
		})
	}
}

func TestAutoReviewAcceptsFencedAnswerEndToEnd(t *testing.T) {
	f := newAutoReviewFixture(t)
	_ = f.s.store.putAutoReviewMode("auto")
	sink := captureLog(t)
	f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		resp := v5ReviewReply(req, "allow", "low", false)
		resp.Content = "```json\n" + resp.Content + "\n```"
		return resp, nil
	}
	if err := f.execute(context.Background()); err != nil || f.effects.Load() != 1 || f.p.calls.Load() != 1 {
		t.Fatalf("fenced valid answer rejected: %v calls=%d", err, f.p.calls.Load())
	}
	if !strings.Contains(sink.String(), "note=code_fence") {
		t.Fatal("tolerated wrapping was not logged")
	}
}

// Reviewer calls are attributed to the run under the reviewer model, one
// usage row per provider call, including a retried request.
func TestAutoReviewRecordsReviewerUsage(t *testing.T) {
	f := newAutoReviewFixture(t)
	_ = f.s.store.putAutoReviewMode("auto")
	f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		resp := v5ReviewReply(req, "allow", "low", false)
		if f.p.calls.Load() == 1 {
			resp.Content = "not json"
		}
		resp.Usage = provider.Usage{InputTokens: 1200, OutputTokens: 40}
		return resp, nil
	}
	if err := f.execute(context.Background()); err != nil || f.p.calls.Load() != 2 {
		t.Fatalf("execute: %v calls=%d", err, f.p.calls.Load())
	}
	rows, err := f.s.store.db.Query(`SELECT bot_id,model,input_tokens,output_tokens FROM usage_calls WHERE run_id=? ORDER BY id`, f.r.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var n int
	for rows.Next() {
		var botID, model string
		var input, output int64
		if err := rows.Scan(&botID, &model, &input, &output); err != nil {
			t.Fatal(err)
		}
		if botID != f.r.BotID || model != codexReviewModel || input != 1200 || output != 40 {
			t.Fatalf("usage row bot=%s model=%s in=%d out=%d", botID, model, input, output)
		}
		n++
	}
	if n != 2 {
		t.Fatalf("usage rows=%d want 2", n)
	}
	var hourly int64
	if err := f.s.store.db.QueryRow(`SELECT COALESCE(SUM(requests),0) FROM usage_hourly WHERE bot_id=?`, f.r.BotID).Scan(&hourly); err != nil || hourly != 2 {
		t.Fatalf("hourly requests=%d err=%v", hourly, err)
	}
}
