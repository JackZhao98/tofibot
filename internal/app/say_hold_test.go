package app

import (
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

type holdProbe struct {
	raw    strings.Builder
	resets int
}

func (p *holdProbe) state(owed func() bool) *sayRunState {
	return &sayRunState{raw: func(s string) { p.raw.WriteString(s) }, reset: func() { p.resets++ }, owed: owed}
}

func feedSay(st *sayStreamer, idx int) {
	st.Delta(runtime.ToolCallDelta{Index: idx, ID: "c", Name: "say"})
	for _, f := range []string{`{"purpose":"answer","resolution":"resolved",`, `"text":"Hel`, `lo there"}`} {
		st.Delta(runtime.ToolCallDelta{Index: idx, Arguments: f})
	}
}

// An answer that the one-time review may refuse never reaches the draft, so a
// refusal cannot delete a bubble the person already watched type out.
func TestSayAnswerOwedReviewIsNotStreamed(t *testing.T) {
	p := &holdProbe{}
	st := newSayStreamer(p.state(func() bool { return true }))
	feedSay(st, 0)
	if p.raw.Len() != 0 || p.resets != 0 {
		t.Fatalf("held answer reached the draft: %q resets=%d", p.raw.String(), p.resets)
	}
}

func TestSayAnswerStreamsWhenNoReviewIsOwed(t *testing.T) {
	p := &holdProbe{}
	st := newSayStreamer(p.state(func() bool { return false }))
	feedSay(st, 0)
	if p.raw.String() != "Hello there" {
		t.Fatalf("draft = %q", p.raw.String())
	}
}

// Tool work earlier in the same response arms the review before the run has
// observed it, so the answer in that response is held too.
func TestSayAnswerAfterToolCallInSameResponseIsHeld(t *testing.T) {
	p := &holdProbe{}
	st := newSayStreamer(p.state(func() bool { return false }))
	st.Delta(runtime.ToolCallDelta{Index: 0, ID: "t", Name: "shell_exec"})
	feedSay(st, 1)
	if p.raw.Len() != 0 {
		t.Fatalf("held answer reached the draft: %q", p.raw.String())
	}
	st.Reset()
	feedSay(st, 0)
	if p.raw.String() != "Hello there" {
		t.Fatalf("after reset draft = %q", p.raw.String())
	}
}
