package app

import (
	"context"
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
