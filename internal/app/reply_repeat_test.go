package app

import (
	"context"
	"strings"
	"testing"
)

func TestRepliesNearlyEqual(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"两个单次定时都已建好，今天太平洋时间 17:50 和 17:53 各跑一次。17:50 由比喻君发新闻比喻，17:53 由我作为组长汇报他发了什么、有没有出问题。", "两个单次定时都建好了：今天 17:50 比喻君发新闻比喻，17:53 我在群里汇报他发了什么、有没有出问题。跑完只会各一次，之后不会再重复。", true},
		{"Both schedules are set: 17:50 for the metaphor post and 17:53 for my report.", "Both schedules are set — 17:50 metaphor post, 17:53 my report.", true},
		{"我先去看一下记账表。", "记账表里本周一共 12 笔，合计 486 美元，最大一笔是周二的房租分摊 300 美元，其余都是餐饮和交通。", false},
		{"今天的热门话题写好了，头条是教皇确诊肺癌。", "今天的热门话题写好了，头条是教皇确诊肺癌、手术成功，其他四条：俄乌供电、OpenAI 录音设备案、约克郡老虎伤人、沙特撤销死刑判决。", false},
		{"Completed batch at turn 16; continuing.", "Completed batch at turn 31; continuing.", false},
		{"", "anything", false},
	}
	for _, tc := range cases {
		if got := repliesNearlyEqual(tc.a, tc.b); got != tc.want {
			t.Errorf("repliesNearlyEqual(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// A final answer restating the run's visible segment is not published again,
// and the segment stands as the run's result; a substantive answer still is.
func TestFinalAnswerRestatingSegmentIsNotRepeated(t *testing.T) {
	for _, tc := range []struct {
		name, segment, final string
		wantVisible          int
	}{
		{"restated", "Both schedules are set: 17:50 for the metaphor post and 17:53 for my report.", "Both schedules are set — 17:50 metaphor post, 17:53 my report.", 1},
		{"substantive", "Checking the ledger.", "The ledger has 12 entries this week totalling 486 dollars; the largest is Tuesday's 300 dollar rent share.", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, r, c := streamFixture(t)
			defer s.Close()
			if _, err := s.BeginStream(r); err != nil {
				t.Fatal(err)
			}
			if _, ok, err := s.PublishAssistantTurn(context.Background(), r.ID, 1, tc.segment); err != nil || !ok {
				t.Fatalf("publish ok=%v err=%v", ok, err)
			}
			result, done, err := s.FinishRun(r.ID, c.ID, r.BotID, tc.final)
			if err != nil || !done {
				t.Fatalf("done=%v err=%v", done, err)
			}
			var visible int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE run_id=? AND role='assistant'`, r.ID).Scan(&visible); err != nil {
				t.Fatal(err)
			}
			if visible != tc.wantVisible {
				t.Fatalf("visible=%d want %d", visible, tc.wantVisible)
			}
			if tc.wantVisible == 1 && (result.ID == "" || result.Content != tc.segment) {
				t.Fatalf("result=%+v, want the segment", result)
			}
			if got, _ := s.GetRun(r.ID); got.Status != "done" {
				t.Fatalf("status=%s", got.Status)
			}
		})
	}
}

// Short step narration folds when a substantial answer follows; a short line
// before a short final answer may be the answer and stays visible.
func TestStepNarrationFoldsBeforeASubstantialAnswer(t *testing.T) {
	long := "Verge 今天最重要的是八个州起诉 OpenAI，说 ChatGPT 对孩子不安全。多州同时出手，等于把 AI 儿童安全推到了州一级的消费者保护诉讼，这个分量比单个州的动作重得多。TechCrunch 的重点是 Flock 的麦克风宣传和官网写法对不上。"
	for _, tc := range []struct {
		name, segment, final, wantKind string
	}{
		{"narration folds", "Now TechCrunch's AI page.", long, "progress"},
		{"short answer stays", "改好了，日报时间换成 2:30。", "好了。", "segment"},
		{"long segment stays", strings.Repeat("这是一段很长的中途结论，包含了具体的发现和数字。", 4), long, "segment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, r, c := streamFixture(t)
			defer s.Close()
			if _, err := s.BeginStream(r); err != nil {
				t.Fatal(err)
			}
			seg, ok, err := s.PublishAssistantTurn(context.Background(), r.ID, 1, tc.segment)
			if err != nil || !ok {
				t.Fatalf("publish ok=%v err=%v", ok, err)
			}
			if _, done, err := s.FinishRun(r.ID, c.ID, r.BotID, tc.final); err != nil || !done {
				t.Fatalf("done=%v err=%v", done, err)
			}
			var kind string
			if err := s.db.QueryRow(`SELECT kind FROM messages WHERE id=?`, seg.ID).Scan(&kind); err != nil {
				t.Fatal(err)
			}
			if kind != tc.wantKind {
				t.Fatalf("kind=%q want %q", kind, tc.wantKind)
			}
			var updates int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE type='message' AND json_extract(data,'$.id')=? AND json_extract(data,'$.kind')='progress'`, seg.ID).Scan(&updates); err != nil {
				t.Fatal(err)
			}
			if (tc.wantKind == "progress") != (updates == 1) {
				t.Fatalf("live update events=%d for kind %s", updates, kind)
			}
		})
	}
}

// A second mid-run message restating the first folds instead of standing as
// another visible message.
func TestRepeatedSegmentFoldsLikeANote(t *testing.T) {
	s, r, _ := streamFixture(t)
	defer s.Close()
	if _, err := s.BeginStream(r); err != nil {
		t.Fatal(err)
	}
	first, ok, err := s.PublishAssistantTurn(context.Background(), r.ID, 1, "I keep the books; what to eat tonight is your call. Tell me what it cost and I will log it in dollars.")
	if err != nil || !ok {
		t.Fatalf("first ok=%v err=%v", ok, err)
	}
	second, ok, err := s.PublishAssistantTurn(context.Background(), r.ID, 2, "What to eat tonight is your call; tell me what it cost and I will log it in dollars.")
	if err != nil || !ok {
		t.Fatalf("second ok=%v err=%v", ok, err)
	}
	if first.Kind != "segment" || second.Kind != "progress" {
		t.Fatalf("kinds first=%s second=%s", first.Kind, second.Kind)
	}
}
