package app

import (
	"context"
	"strings"
	"testing"
)

func TestOnboardingClosesWithProvenHandovers(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), DefaultModel: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	block := s.provenHandoversBlock(context.Background())
	if !strings.HasPrefix(block, "\n[proven handovers]\n") || !strings.HasSuffix(block, "[/proven handovers]") {
		t.Fatalf("block = %q", block)
	}
	if strings.Contains(block, "computer script") || strings.Contains(block, "inbox digest") || strings.Contains(block, "landlord") {
		t.Fatalf("no computer, no Gmail, no glance tier, yet: %q", block)
	}
	if !strings.Contains(block, "reminder in minutes | schedules | nothing outside the chat") {
		t.Fatalf("a reminder line is missing: %q", block)
	}
	s.microVM = newFakeComputer("firecracker")
	if with := s.provenHandoversBlock(context.Background()); !strings.Contains(with, "computer script | computer") {
		t.Fatalf("with a computer: %q", with)
	}
	for _, want := range []string{"listed under proven handovers", "nothing that sends outward"} {
		if !strings.Contains(onboardingSystemPrompt, want) {
			t.Fatalf("principle lacks %q", want)
		}
	}
	for _, banned := range []string{"for example", "e.g.", "such as", " but "} {
		if strings.Contains(onboardingSystemPrompt+block, banned) {
			t.Fatalf("model-facing text holds %q", banned)
		}
	}
	total := len(onboardingSystemPrompt) + len(block)
	t.Logf("first-turn onboarding text: %d chars (about %d tokens), block %d chars", total, total/4, len(block))
}
