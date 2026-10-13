package app

import (
	"testing"
	"time"
)

func TestImportanceScoreAndBands(t *testing.T) {
	cases := []struct {
		name string
		in   importanceInput
		want int
		band string
	}{
		{"remind me with a date", importanceInput{Kind: loopKindOwnerReminder, Category: "admin", Signals: []string{"owner_asked"}, HasDate: true}, 65, bandNudge},
		{"third party committed, no date", importanceInput{Kind: loopKindAwaitingOther, Category: "home", Signals: []string{"third_party_committed"}}, 40, bandDigest},
		{"third party committed, dated, high stakes", importanceInput{Kind: loopKindAwaitingOther, Category: "money", Signals: []string{"third_party_committed"}, HasDate: true}, 70, bandNudge},
		{"third party, no timing at all", importanceInput{Kind: loopKindAwaitingOther, Category: "home", Signals: []string{"third_party_committed"}, NoExpectation: true}, 30, bandReject},
		{"owner promise with a date nudges at the date", importanceInput{Kind: loopKindOwnerPromise, Category: "work", HasDate: true}, 60, bandNudge},
		{"owner promise without a date is digest only", importanceInput{Kind: loopKindOwnerPromise, Category: "work"}, 40, bandDigest},
		{"owner promise, high stakes, undated stays below the nudge band", importanceInput{Kind: loopKindOwnerPromise, Category: "legal_gov", Signals: []string{"owner_asked", "third_party_committed"}}, 59, bandDigest},
		{"vague social is rejected even as a promise", importanceInput{Kind: loopKindOwnerPromise, Category: "social", Signals: []string{"vague_social"}}, 0, bandReject},
		{"unknown model signals are worthless", importanceInput{Kind: loopKindAwaitingOther, Category: "other", Signals: []string{"urgent!!!", "owner_asked", "owner_asked"}}, 50, bandDigest},
		{"automated sender is capped under the digest band", importanceInput{Kind: loopKindAwaitingOther, Category: "work", Signals: []string{"owner_asked", "third_party_committed"}, AutomatedSender: true}, 39, bandReject},
		{"automated but dated high-stakes passes up to 65", importanceInput{Kind: loopKindAwaitingOther, Category: "legal_gov", Signals: []string{"owner_asked", "third_party_committed"}, HasDate: true, AutomatedSender: true}, 65, bandNudge},
		{"digest_only suppression caps below the nudge band", importanceInput{Kind: loopKindAwaitingOther, Category: "money", Signals: []string{"owner_asked", "third_party_committed"}, DigestOnlySuppressed: true}, 59, bandDigest},
	}
	for _, c := range cases {
		got := importanceScore(c.in)
		if got != c.want || importanceBand(got) != c.band {
			t.Errorf("%s: score=%d band=%s, want %d %s", c.name, got, importanceBand(got), c.want, c.band)
		}
	}
	for score, want := range map[int]string{100: bandNudge, 60: bandNudge, 59: bandDigest, 40: bandDigest, 39: bandReject, 0: bandReject} {
		if importanceBand(score) != want {
			t.Errorf("band(%d) = %s", score, importanceBand(score))
		}
	}
}

func TestComputeExpectedBy(t *testing.T) {
	la, _ := time.LoadLocation("America/Los_Angeles")
	// Tuesday 2026-10-13 14:00 PDT.
	created := time.Date(2026, 10, 13, 14, 0, 0, 0, la)
	at := func(kind string, h expectedByHint) (time.Time, bool, error) {
		return computeExpectedBy(kind, h, created, la)
	}

	got, dated, err := at(loopKindAwaitingOther, expectedByHint{Type: "explicit", Date: "2026-10-16"}) // Friday: 18:00 + 1 business day = Monday 18:00
	if err != nil || !dated || !got.Equal(time.Date(2026, 10, 19, 18, 0, 0, 0, la)) {
		t.Fatalf("awaiting explicit: %v %v %v", got.In(la), dated, err)
	}
	got, dated, err = at(loopKindOwnerPromise, expectedByHint{Type: "explicit", Date: "2026-10-16"}) // owner's own: that morning, no grace
	if err != nil || !dated || !got.Equal(time.Date(2026, 10, 16, 9, 0, 0, 0, la)) {
		t.Fatalf("promise explicit: %v %v %v", got.In(la), dated, err)
	}
	got, dated, _ = at(loopKindAwaitingOther, expectedByHint{Type: "relative", Days: 2})
	if dated || !got.Equal(time.Date(2026, 10, 15, 14, 0, 0, 0, la)) {
		t.Fatalf("relative 2 business days: %v", got.In(la))
	}
	got, _, _ = at(loopKindAwaitingOther, expectedByHint{Type: "relative", Days: 3}) // skips the weekend
	if !got.Equal(time.Date(2026, 10, 16, 14, 0, 0, 0, la)) {
		t.Fatalf("relative 3: %v", got.In(la))
	}
	got, dated, _ = at(loopKindOwnerReminder, expectedByHint{Type: "next_week"})
	if !dated || !got.Equal(time.Date(2026, 10, 19, 9, 0, 0, 0, la)) {
		t.Fatalf("next week: %v", got.In(la))
	}
	got, _, _ = at(loopKindAwaitingOther, expectedByHint{Type: "end_of_month"}) // Oct 30 is a Friday; 18:00 + 1 day
	if !got.Equal(time.Date(2026, 10, 31, 18, 0, 0, 0, la)) {
		t.Fatalf("end of month: %v", got.In(la))
	}
	got, dated, _ = at(loopKindAwaitingOther, expectedByHint{Type: "none"})
	if dated || !got.Equal(time.Date(2026, 10, 20, 14, 0, 0, 0, la)) {
		t.Fatalf("none: %v", got.In(la))
	}
	for _, bad := range []expectedByHint{{Type: "explicit", Date: "friday"}, {Type: "explicit", Date: "2026-10-01"}, {Type: "relative", Days: 0}, {Type: "relative", Days: 99}, {Type: "soon"}} {
		if _, _, err := at(loopKindOwnerPromise, bad); err == nil {
			t.Errorf("hint %+v accepted", bad)
		}
	}
}

func TestQuietHoursWrapAroundMidnight(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 10, 14, h, m, 0, 0, time.UTC) }
	for _, c := range []struct {
		h, m int
		want bool
	}{{21, 59, false}, {22, 0, true}, {23, 30, true}, {3, 0, true}, {7, 59, true}, {8, 0, false}, {12, 0, false}} {
		if got := inQuietHours(at(c.h, c.m), "22:00", "08:00"); got != c.want {
			t.Errorf("%02d:%02d quiet=%v want %v", c.h, c.m, got, c.want)
		}
	}
	if inQuietHours(at(3, 0), "08:00", "08:00") {
		t.Error("equal bounds mean no quiet hours")
	}
	if !inQuietHours(at(13, 0), "12:00", "14:00") || inQuietHours(at(14, 0), "12:00", "14:00") {
		t.Error("same-day window")
	}
}

func baseLoop(now time.Time) followupLoop {
	return followupLoop{ID: "l1", BotID: "b1", Kind: loopKindAwaitingOther, CounterpartyKey: "x.example", Importance: 70, State: "open",
		ExpectedBy: now.Add(-2 * time.Hour), CreatedAt: now.Add(-72 * time.Hour)}
}

func TestNudgeAllowedHardLimits(t *testing.T) {
	cfg := defaultFollowupAccountSettings()
	la, _ := time.LoadLocation("America/Los_Angeles")
	wed := time.Date(2026, 10, 14, 10, 0, 0, 0, la) // Wednesday 10:00 local
	type tc struct {
		name   string
		now    time.Time
		mut    func(*followupLoop)
		hist   nudgeHistory
		cfg    func(*followupAccountSettings)
		allow  bool
		reason string
		defr   bool
	}
	cases := []tc{
		{name: "baseline allowed", now: wed, allow: true},
		{name: "below nudge band", now: wed, mut: func(l *followupLoop) { l.Importance = 59 }, reason: "band"},
		{name: "not due yet", now: wed, mut: func(l *followupLoop) { l.ExpectedBy = wed.Add(time.Hour) }, reason: "not_due"},
		{name: "never checked before created+24h", now: wed, mut: func(l *followupLoop) { l.CreatedAt = wed.Add(-23 * time.Hour); l.ExpectedBy = wed.Add(-time.Hour) }, reason: "too_new"},
		{name: "the owner's own reminder is not held for 24h", now: wed, mut: func(l *followupLoop) {
			l.Kind = loopKindOwnerReminder
			l.CreatedAt = wed.Add(-2 * time.Hour)
		}, allow: true},
		{name: "snoozed", now: wed, mut: func(l *followupLoop) { l.State = "snoozed"; l.SnoozedUntil = wed.Add(time.Hour) }, reason: "snoozed"},
		{name: "snooze elapsed", now: wed, mut: func(l *followupLoop) { l.State = "snoozed"; l.SnoozedUntil = wed.Add(-time.Hour) }, allow: true},
		{name: "closed loop", now: wed, mut: func(l *followupLoop) { l.State = "handled_by_owner" }, reason: "state_handled_by_owner"},
		{name: "second nudge too soon", now: wed, mut: func(l *followupLoop) {
			l.NudgeCount = 1
			l.State = "nudged"
			l.LastNudgedAt = wed.Add(-6 * 24 * time.Hour)
		}, reason: "nudge_limit"},
		{name: "second nudge after 7 days and important", now: wed, mut: func(l *followupLoop) {
			l.NudgeCount = 1
			l.State = "nudged"
			l.LastNudgedAt = wed.Add(-8 * 24 * time.Hour)
		}, allow: true},
		{name: "second nudge needs importance 70", now: wed, mut: func(l *followupLoop) {
			l.NudgeCount = 1
			l.State = "nudged"
			l.Importance = 65
			l.LastNudgedAt = wed.Add(-8 * 24 * time.Hour)
		}, reason: "nudge_limit"},
		{name: "no second nudge after a dismissal", now: wed, mut: func(l *followupLoop) {
			l.NudgeCount = 1
			l.State = "nudged"
			l.LastNudgedAt = wed.Add(-8 * 24 * time.Hour)
			l.FirstNudgeDismissed = true
		}, reason: "nudge_limit"},
		{name: "never a third", now: wed, mut: func(l *followupLoop) { l.NudgeCount = 2; l.LastNudgedAt = wed.Add(-30 * 24 * time.Hour) }, reason: "nudge_limit"},
		{name: "same counterparty within 7 days", now: wed, mut: func(l *followupLoop) { l.CounterpartyNudgedAt = wed.Add(-3 * 24 * time.Hour) }, reason: "counterparty_recent"},
		{name: "same counterparty after 7 days", now: wed, mut: func(l *followupLoop) { l.CounterpartyNudgedAt = wed.Add(-8 * 24 * time.Hour) }, allow: true},
		{name: "owner active in the last 24h defers", now: wed, mut: func(l *followupLoop) { l.LastOwnerActivityAt = wed.Add(-23 * time.Hour) }, reason: "owner_active", defr: true},
		{name: "owner activity older than 24h is fine", now: wed, mut: func(l *followupLoop) { l.LastOwnerActivityAt = wed.Add(-25 * time.Hour) }, allow: true},
		{name: "quiet hours defer", now: time.Date(2026, 10, 14, 23, 0, 0, 0, la), reason: "quiet_hours", defr: true},
		{name: "early morning defers", now: time.Date(2026, 10, 14, 7, 0, 0, 0, la), reason: "quiet_hours", defr: true},
		{name: "weekend needs importance 80", now: time.Date(2026, 10, 17, 10, 0, 0, 0, la), reason: "weekend", defr: true},
		{name: "weekend with importance 80", now: time.Date(2026, 10, 17, 10, 0, 0, 0, la), mut: func(l *followupLoop) { l.Importance = 85 }, allow: true},
		{name: "daily cap reached", now: wed, hist: nudgeHistory{NudgesToday: 3}, reason: "daily_cap", defr: true},
		{name: "daily cap not reached", now: wed, hist: nudgeHistory{NudgesToday: 2}, allow: true},
		{name: "cap is configurable", now: wed, hist: nudgeHistory{NudgesToday: 1}, cfg: func(c *followupAccountSettings) { c.MaxNudgesPerDay = 1 }, reason: "daily_cap", defr: true},
		{name: "budget spent stops everything until tomorrow", now: wed, hist: nudgeHistory{SpentUSD: 0.5}, reason: "budget", defr: true},
		{name: "budget not yet spent", now: wed, hist: nudgeHistory{SpentUSD: 0.49}, allow: true},
	}
	for _, c := range cases {
		loop := baseLoop(c.now)
		if c.mut != nil {
			c.mut(&loop)
		}
		conf := cfg
		if c.cfg != nil {
			c.cfg(&conf)
		}
		d := nudgeAllowed(c.now, la, conf, loop, c.hist)
		if d.Allowed != c.allow || (!c.allow && d.Reason != c.reason) || d.Defer != c.defr {
			t.Errorf("%s: got %+v, want allow=%v reason=%q defer=%v", c.name, d, c.allow, c.reason, c.defr)
		}
	}
}

func TestLoopExpiresSilentlyAfterThirtyDays(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	l := baseLoop(now)
	l.ExpectedBy = now.Add(-31 * 24 * time.Hour)
	if d := nudgeAllowed(now, time.UTC, defaultFollowupAccountSettings(), l, nudgeHistory{}); d.Allowed || !d.Expire {
		t.Fatalf("overdue loop must expire, not nudge: %+v", d)
	}
	l.NudgeCount = 1 // already nudged once: expiry rule is for loops never nudged
	l.LastNudgedAt = now.Add(-20 * 24 * time.Hour)
	if d := nudgeAllowed(now, time.UTC, defaultFollowupAccountSettings(), l, nudgeHistory{}); d.Expire {
		t.Fatalf("nudged loop should not take the silent-expiry path: %+v", d)
	}
}

func TestSelectNudgesPrimaryFirstThenImportance(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	cands := []nudgeCandidate{
		{LoopID: "c-high", BotID: "coder", Importance: 95, ExpectedBy: t0},
		{LoopID: "p-low", BotID: "primary", Importance: 61, ExpectedBy: t0},
		{LoopID: "c-mid", BotID: "coder", Importance: 80, ExpectedBy: t0},
		{LoopID: "p-high", BotID: "primary", Importance: 90, ExpectedBy: t0},
		{LoopID: "p-tie-late", BotID: "primary", Importance: 61, ExpectedBy: t0.Add(time.Hour)},
	}
	got := selectNudges(cands, 3, "primary")
	want := []string{"p-high", "p-low", "p-tie-late"}
	if len(got) != 3 {
		t.Fatalf("got %d", len(got))
	}
	for i, w := range want {
		if got[i].LoopID != w {
			t.Fatalf("slot %d = %s, want %s (%v)", i, got[i].LoopID, w, got)
		}
	}
	if len(selectNudges(cands, 0, "primary")) != 0 || len(selectNudges(nil, 3, "primary")) != 0 {
		t.Fatal("no slots, no nudges")
	}
	// With no Primary (empty account role) ordering is by importance alone.
	if selectNudges(cands, 1, "")[0].LoopID != "c-high" {
		t.Fatal("no Primary: importance decides")
	}
	if cands[0].LoopID != "c-high" {
		t.Fatal("input slice was reordered")
	}
}

func TestSweepWindowAndLocalDay(t *testing.T) {
	la, _ := time.LoadLocation("America/Los_Angeles")
	if sweepWindowOpen(time.Date(2026, 10, 14, 7, 59, 0, 0, la), la) || !sweepWindowOpen(time.Date(2026, 10, 14, 8, 0, 0, 0, la), la) || !sweepWindowOpen(time.Date(2026, 10, 14, 20, 59, 0, 0, la), la) || sweepWindowOpen(time.Date(2026, 10, 14, 21, 0, 0, 0, la), la) {
		t.Fatal("sweep window is 08:00-21:00 owner time")
	}
	utc := time.Date(2026, 10, 15, 5, 0, 0, 0, time.UTC) // 22:00 on the 14th in Los Angeles
	if ownerLocalDay(utc, la) != "2026-10-14" {
		t.Fatal("local day key")
	}
}

func TestTopicDigestIsStableAcrossPhrasing(t *testing.T) {
	if topicDigest("Elyse", "Pest control to call about roof bees!") != topicDigest("elyse", "pest  control to call about roof bees") {
		t.Fatal("digest must ignore case and punctuation")
	}
	if topicDigest("Elyse", "roof bees") == topicDigest("Marc", "roof bees") {
		t.Fatal("different counterparty, different loop")
	}
}
