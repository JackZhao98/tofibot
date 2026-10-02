package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestQuestionResponseWindows(t *testing.T) {
	for _, kind := range []string{questionText, questionForm} {
		for _, seconds := range []int{0, 10, 86400, -1, 86401} {
			in := askQuestionInput{Question: "Supply the missing value", Type: kind, ExpiresInSeconds: seconds}
			if kind == questionForm {
				in.SourceURL = "https://forms.example.test/profile"
				in.Fields = []UserFormField{{ID: "name", Label: "Name", Type: "text", Required: true}}
			}
			got, err := normalizeQuestionInput(in)
			if seconds < 0 || seconds > 86400 {
				if err == nil {
					t.Fatalf("%s accepted expiry %d", kind, seconds)
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			want := seconds
			if want == 0 {
				want = 1800
			}
			if got.ExpiresInSeconds != want {
				t.Fatalf("%s expiry=%d, want %d", kind, got.ExpiresInSeconds, want)
			}
		}
	}
}

func TestQuestionDefaultWindowPersistsAndStopStillCancels(t *testing.T) {
	s, c, run := questionFixture(t)
	defer s.Close()
	in, err := normalizeQuestionInput(askQuestionInput{Question: "Continue?", Type: questionText})
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	q, err := s.CreateQuestion(c.ID, run, in)
	if err != nil {
		t.Fatal(err)
	}
	expires, err := time.Parse(time.RFC3339Nano, q.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	if expires.Before(before.Add(30*time.Minute)) || expires.After(time.Now().Add(30*time.Minute)) {
		t.Fatalf("unexpected response deadline: %s", q.ExpiresAt)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	answer, err := (&Server{store: s}).WaitQuestion(ctx, q.ID)
	if !errors.Is(err, context.Canceled) || len(answer) != 0 {
		t.Fatalf("answer=%s error=%v", answer, err)
	}
	q, err = s.GetQuestion(q.ID)
	if err != nil || q.Status != questionCancelled {
		t.Fatalf("question=%+v error=%v", q, err)
	}
}

func TestQuestionAndFormExposeSameExpiryLimits(t *testing.T) {
	s, c, run := questionFixture(t)
	defer s.Close()
	tools := (&Server{store: s}).questionTools(c, run)
	for _, tool := range tools {
		properties := tool.Parameters["properties"].(map[string]any)
		expiry, ok := properties["expires_in_seconds"].(map[string]any)
		if !ok || expiry["maximum"] != 86400 || expiry["minimum"] != 0 {
			t.Fatalf("%s expiry schema = %+v", tool.Name, expiry)
		}
	}
}
