package app

// Restraint for proactive follow-ups (docs/agent-plan/proactive-followups.md
// section 4). Everything here is a pure function of its inputs: no store, no
// clock, no model. The model only labels what it saw; these functions decide
// what the label is worth and whether anything may be said, so the rules are
// unit-testable and cannot be argued with by an email or a chat message.

import (
	"errors"
	"sort"
	"strings"
	"time"
)

const (
	loopKindAwaitingOther = "awaiting_other"
	loopKindOwnerPromise  = "owner_promise"
	loopKindOwnerReminder = "owner_reminder"

	bandNudge  = "nudge"
	bandDigest = "digest"
	bandReject = "reject"

	nudgeBandMin  = 60
	digestBandMin = 40

	secondNudgeMinImportance = 70
	secondNudgeMinGap        = 7 * 24 * time.Hour
	counterpartyNudgeGap     = 7 * 24 * time.Hour
	ownerActivityQuietWindow = 24 * time.Hour
	checkGrace               = 24 * time.Hour
	expireAfter              = 30 * 24 * time.Hour
)

var followupLoopKinds = map[string]bool{loopKindAwaitingOther: true, loopKindOwnerPromise: true, loopKindOwnerReminder: true}

// followupCategories is the closed enum the model self-labels with.
var followupCategories = map[string]bool{"home": true, "money": true, "legal_gov": true, "health": true, "travel": true, "work": true, "social": true, "admin": true, "other": true}

var highStakesCategories = map[string]bool{"money": true, "legal_gov": true, "health": true, "travel": true}

// modelSignals are the importance signals the model may claim (section 4.1).
// The rest of the rubric (date, category, owner promise, correspondence) is
// derived by the host from fields it can verify.
var modelSignals = map[string]int{
	"owner_asked":           50,  // "remind me", "wait until he replies"
	"third_party_committed": 40,  // "X will call you"
	"vague_social":          -30, // "let's catch up sometime"
}

type importanceInput struct {
	Kind     string
	Category string
	Signals  []string // model-claimed; unknown names are ignored
	HasDate  bool     // a concrete date or deadline (host-parsed hint)
	// NoExpectation is true when the model gave no timing at all.
	NoExpectation bool
	// AutomatedSender is derived by the host from mail headers (P1); false for chat.
	AutomatedSender bool
	// HumanCorrespondent is host-derived: the owner has written to this person before.
	HumanCorrespondent bool
	// DigestOnlySuppressed is a followup_suppressions digest_only hit.
	DigestOnlySuppressed bool
}

// importanceScore applies the section 4.1 rubric plus the owner decision on
// the owner's own promises: dated -> nudge band (at the date), undated ->
// digest band only.
func importanceScore(in importanceInput) int {
	score := 0
	seen := map[string]bool{}
	vague := false
	for _, signal := range in.Signals {
		delta, ok := modelSignals[signal]
		if !ok || seen[signal] {
			continue
		}
		seen[signal] = true
		score += delta
		if signal == "vague_social" {
			vague = true
		}
	}
	if in.HasDate {
		score += 15
	}
	if highStakesCategories[in.Category] {
		score += 15
	}
	if in.HumanCorrespondent {
		score += 10
	}
	if in.Kind == loopKindOwnerPromise {
		score += 20
	}
	if in.NoExpectation && in.Kind == loopKindAwaitingOther {
		score -= 10
	}
	switch in.Kind {
	case loopKindOwnerPromise:
		if !vague {
			if in.HasDate && score < nudgeBandMin {
				score = nudgeBandMin
			}
			if !in.HasDate {
				score = clampInt(score, digestBandMin, nudgeBandMin-1)
			}
		}
	case loopKindOwnerReminder:
		// An explicit "remind me" is the owner's own request; a dated one is
		// delivered on its date.
		if !vague && in.HasDate && score < nudgeBandMin {
			score = nudgeBandMin
		}
	}
	if in.AutomatedSender {
		limit := digestBandMin - 1
		if highStakesCategories[in.Category] && in.HasDate {
			limit = 65
		}
		if score > limit {
			score = limit
		}
	}
	if in.DigestOnlySuppressed && score > nudgeBandMin-1 {
		score = nudgeBandMin - 1
	}
	return clampInt(score, 0, 100)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// importanceBand maps a score to what may be done with it: nudge (>=60),
// digest only (40-59), or reject (<40: not stored).
func importanceBand(score int) string {
	switch {
	case score >= nudgeBandMin:
		return bandNudge
	case score >= digestBandMin:
		return bandDigest
	default:
		return bandReject
	}
}

// expectedByHint is the model's timing, one of explicit / relative /
// next_week / end_of_month / none.
type expectedByHint struct {
	Type string `json:"type"`
	Date string `json:"date,omitempty"` // YYYY-MM-DD for explicit
	Days int    `json:"days,omitempty"` // business days for relative
}

var errBadHint = errors.New("invalid expected_by hint")

func isBusinessDay(t time.Time) bool {
	return t.Weekday() != time.Saturday && t.Weekday() != time.Sunday
}

func addBusinessDays(t time.Time, n int) time.Time {
	for n > 0 {
		t = t.AddDate(0, 0, 1)
		if isBusinessDay(t) {
			n--
		}
	}
	return t
}

func atClock(t time.Time, hour, min int, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, hour, min, 0, 0, loc)
}

// computeExpectedBy converts a hint into the instant after which silence is
// meaningful (section 1.3). dated reports whether the hint named a concrete
// date (explicit, next week, end of month). For the owner's own promises and
// reminders an explicit date means that morning at 09:00 with no grace day;
// the grace day is for people the owner is waiting on.
func computeExpectedBy(kind string, hint expectedByHint, created time.Time, loc *time.Location) (at time.Time, dated bool, err error) {
	if loc == nil {
		loc = time.UTC
	}
	created = created.In(loc)
	switch hint.Type {
	case "explicit":
		day, e := time.ParseInLocation("2006-01-02", hint.Date, loc)
		if e != nil {
			return time.Time{}, false, errBadHint
		}
		if day.Before(atClock(created, 0, 0, loc).AddDate(0, 0, -1)) {
			return time.Time{}, false, errBadHint // already more than a day in the past
		}
		if kind == loopKindAwaitingOther {
			at = addBusinessDays(atClock(day, 18, 0, loc), 1)
		} else {
			at = atClock(day, 9, 0, loc)
		}
		if at.Before(created) {
			at = created
		}
		return at.UTC(), true, nil
	case "relative":
		if hint.Days < 1 || hint.Days > 30 {
			return time.Time{}, false, errBadHint
		}
		return addBusinessDays(created, hint.Days).UTC(), false, nil
	case "next_week":
		d := atClock(created, 9, 0, loc).AddDate(0, 0, 1)
		for d.Weekday() != time.Monday {
			d = d.AddDate(0, 0, 1)
		}
		return d.UTC(), true, nil
	case "end_of_month":
		y, m, _ := created.Date()
		last := time.Date(y, m+1, 0, 18, 0, 0, 0, loc)
		for !isBusinessDay(last) {
			last = last.AddDate(0, 0, -1)
		}
		at = last.AddDate(0, 0, 1)
		if at.Before(created) {
			at = created
		}
		return at.UTC(), true, nil
	case "none", "":
		return addBusinessDays(created, 5).UTC(), false, nil
	}
	return time.Time{}, false, errBadHint
}

// followupAccountSettings are the account-wide limits (owner decision 3).
type followupAccountSettings struct {
	MaxNudgesPerDay      int     `json:"max_nudges_per_day"`
	QuietStart           string  `json:"quiet_start"`
	QuietEnd             string  `json:"quiet_end"`
	WeekendMinImportance int     `json:"weekend_min_importance"`
	DigestWeekday        string  `json:"digest_weekday"`
	DigestTime           string  `json:"digest_time"`
	DailyBudgetUSD       float64 `json:"daily_budget_usd"`
}

func defaultFollowupAccountSettings() followupAccountSettings {
	return followupAccountSettings{MaxNudgesPerDay: 3, QuietStart: "22:00", QuietEnd: "08:00", WeekendMinImportance: 80, DigestWeekday: "mon", DigestTime: "09:00", DailyBudgetUSD: 0.5}
}

func parseHHMM(v string) (minutes int, ok bool) {
	t, err := time.Parse("15:04", v)
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

// inQuietHours reports whether local falls in [start, end). The window may
// wrap midnight (22:00-08:00); start == end means no quiet hours.
func inQuietHours(local time.Time, start, end string) bool {
	s, ok1 := parseHHMM(start)
	e, ok2 := parseHHMM(end)
	if !ok1 || !ok2 || s == e {
		return false
	}
	m := local.Hour()*60 + local.Minute()
	if s < e {
		return m >= s && m < e
	}
	return m >= s || m < e
}

func isWeekend(local time.Time) bool { return !isBusinessDay(local) }

// followupLoop is the slice of an open loop the restraint rules read.
type followupLoop struct {
	ID                   string
	BotID                string
	Kind                 string
	CounterpartyKey      string // lower-cased address or domain; "" for chat
	Importance           int
	State                string
	ExpectedBy           time.Time
	CreatedAt            time.Time
	NudgeCount           int
	LastNudgedAt         time.Time
	SnoozedUntil         time.Time
	FirstNudgeDismissed  bool
	AutomatedSender      bool
	DatedObligation      bool
	LastOwnerActivityAt  time.Time // owner activity on this thread/counterparty, zero if none
	CounterpartyNudgedAt time.Time // last nudge to this counterparty on any loop, zero if none
}

// nudgeHistory is what already happened today, account-wide.
type nudgeHistory struct {
	NudgesToday int     // nudges and digests delivered in the owner-local day
	SpentUSD    float64 // follow-up model spend in the owner-local day
}

type nudgeDecision struct {
	Allowed bool
	// Defer means "not now, maybe later" (quiet hours, daily cap, owner was just
	// active); a non-deferred refusal is final for this evaluation.
	Defer  bool
	Expire bool
	Reason string
}

func refuse(reason string) nudgeDecision { return nudgeDecision{Reason: reason} }
func deferred(reason string) nudgeDecision {
	return nudgeDecision{Defer: true, Reason: reason}
}

// nudgeAllowed applies the hard limits of section 4.3 to one loop at `now`.
func nudgeAllowed(now time.Time, loc *time.Location, cfg followupAccountSettings, loop followupLoop, hist nudgeHistory) nudgeDecision {
	if loc == nil {
		loc = time.UTC
	}
	switch loop.State {
	case "open", "snoozed", "nudged":
	default:
		return refuse("state_" + loop.State)
	}
	if loop.NudgeCount == 0 && !loop.ExpectedBy.IsZero() && now.After(loop.ExpectedBy.Add(expireAfter)) {
		return nudgeDecision{Expire: true, Reason: "expired"}
	}
	if importanceBand(loop.Importance) != bandNudge {
		return refuse("band")
	}
	if loop.State == "snoozed" && now.Before(loop.SnoozedUntil) {
		return refuse("snoozed")
	}
	if now.Before(loop.ExpectedBy) {
		return refuse("not_due")
	}
	if loop.Kind == loopKindAwaitingOther && now.Before(loop.CreatedAt.Add(checkGrace)) {
		return refuse("too_new")
	}
	switch {
	case loop.NudgeCount >= 2:
		return refuse("nudge_limit")
	case loop.NudgeCount == 1:
		if loop.Importance < secondNudgeMinImportance || now.Sub(loop.LastNudgedAt) < secondNudgeMinGap || loop.FirstNudgeDismissed {
			return refuse("nudge_limit")
		}
	}
	if loop.CounterpartyKey != "" && !loop.CounterpartyNudgedAt.IsZero() && now.Sub(loop.CounterpartyNudgedAt) < counterpartyNudgeGap {
		return refuse("counterparty_recent")
	}
	if !loop.LastOwnerActivityAt.IsZero() && now.Sub(loop.LastOwnerActivityAt) < ownerActivityQuietWindow {
		return deferred("owner_active")
	}
	local := now.In(loc)
	if inQuietHours(local, cfg.QuietStart, cfg.QuietEnd) {
		return deferred("quiet_hours")
	}
	if isWeekend(local) && loop.Importance < cfg.WeekendMinImportance {
		return deferred("weekend")
	}
	if cfg.DailyBudgetUSD > 0 && hist.SpentUSD >= cfg.DailyBudgetUSD {
		return deferred("budget")
	}
	if hist.NudgesToday >= cfg.MaxNudgesPerDay {
		return deferred("daily_cap")
	}
	return nudgeDecision{Allowed: true}
}

type nudgeCandidate struct {
	LoopID     string
	BotID      string
	Importance int
	ExpectedBy time.Time
}

// selectNudges spends the day's remaining slots (owner decision 3): the
// Primary Assistant's candidates first, then higher importance, then the one
// that has been due longest, then id for a stable order. The rest wait.
func selectNudges(candidates []nudgeCandidate, remaining int, primaryBotID string) []nudgeCandidate {
	if remaining <= 0 || len(candidates) == 0 {
		return nil
	}
	sorted := append([]nudgeCandidate(nil), candidates...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		ap, bp := a.BotID == primaryBotID && primaryBotID != "", b.BotID == primaryBotID && primaryBotID != ""
		if ap != bp {
			return ap
		}
		if a.Importance != b.Importance {
			return a.Importance > b.Importance
		}
		if !a.ExpectedBy.Equal(b.ExpectedBy) {
			return a.ExpectedBy.Before(b.ExpectedBy)
		}
		return a.LoopID < b.LoopID
	})
	if len(sorted) > remaining {
		sorted = sorted[:remaining]
	}
	return sorted
}

// ownerLocalDay is the key the daily counters use.
func ownerLocalDay(now time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	return now.In(loc).Format("2006-01-02")
}

// sweepWindowOpen reports whether the sweep may run: between 08:00 and 21:00
// owner time (section 2.4).
func sweepWindowOpen(now time.Time, loc *time.Location) bool {
	if loc == nil {
		loc = time.UTC
	}
	h := now.In(loc).Hour()
	return h >= 8 && h < 21
}

// normalizeTopicStem lower-cases and collapses whitespace/punctuation so two
// phrasings of the same loop usually share a digest.
func normalizeTopicStem(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r > 127:
			b.WriteRune(r)
			space = false
		default:
			if !space && b.Len() > 0 {
				b.WriteByte(' ')
				space = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}
