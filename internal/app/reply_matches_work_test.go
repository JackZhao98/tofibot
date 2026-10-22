package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// D45: a reply is held to the tool record (extent, counts, outcome) and names
// where the work's results live. The principle sits in the final review, in the
// say contract at the moment of writing, and in the tool results that carry the
// facts. Prompts state principles only: no examples, contrasts or word lists.
func TestEveryFinalReviewHoldsTheReplyToTheRecordAndNamesWhereResultsLive(t *testing.T) {
	for name, text := range map[string]string{
		"chat review":                 taskCompletionFinalReminder,
		"scheduled review":            scheduleFinalReminder,
		"scheduled structured review": scheduleFinalReminderStructured,
	} {
		for _, want := range []string{
			"sentence by sentence against the tool results",
			"holds for exactly what the results show",
			"a check that reached part of a set is reported as reaching that part",
			"a count names what was counted",
			"says what exists and where it lives, using the locations the tool results gave",
			"working files stay on the computer",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%s lost %q", name, want)
			}
		}
		lower := strings.ToLower(text)
		for _, banned := range []string{"for example", "e.g.", "such as", "not just", "rather than", "\"", "i checked everything"} {
			if strings.Contains(lower, banned) {
				t.Errorf("%s carries example or contrast wording %q", name, banned)
			}
		}
	}
	if strings.Index(taskCompletionFinalReminder, "sentence by sentence") > strings.Index(taskCompletionFinalReminder, "requested deliverable") {
		t.Error("the reply-against-record check must come before the completeness check")
	}
}

func TestSayContractStatesExtentCountsAndWhereThingsLive(t *testing.T) {
	for _, want := range []string{
		"reports the work at the extent and counts the tool results show",
		"names where anything you leave for the person to rely on lives",
	} {
		if !strings.Contains(sayToolDescription, want) {
			t.Errorf("say description lost %q", want)
		}
	}
}

// publish_file states the facts a reply about publishing needs: where the file
// came from on the computer, and everything the reply will carry.
func TestPublishFileResultNamesTheSourceAndEverythingAttachedToTheReply(t *testing.T) {
	s, root, _ := newGuestInputServer(t)
	bot, _ := s.store.CreateBot("synthetic", "", "model")
	conv, _ := s.store.GetConversation(bot.DMConversationID)
	_, run, _ := sendWithFiles(t, s, conv.ID, bot.ID, "make the report", "rm-1", nil, nil)
	if ok, err := s.store.SetRunStatus(run.ID, "running", ""); err != nil || !ok {
		t.Fatalf("running=%v %v", ok, err)
	}
	dir := filepath.Join(root, "bots", bot.ID)
	if err := os.MkdirAll(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"report.txt", "scratch.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("synthetic "+n), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tool := s.publishAttachmentTools(conv, run)[0]
	if !strings.Contains(tool.Description, "working files stay on the computer") {
		t.Errorf("publish_file description lost the deliverables-only principle: %s", tool.Description)
	}
	var last struct {
		SourcePath string   `json:"source_path"`
		Attached   []string `json:"attached_to_reply"`
	}
	for _, n := range []string{"report.txt", "scratch.txt"} {
		out, err := tool.Execute(context.Background(), json.RawMessage(`{"path":`+jsonQuote(n)+`}`))
		if err != nil {
			t.Fatal(err)
		}
		last.SourcePath, last.Attached = "", nil
		if json.Unmarshal([]byte(out), &last) != nil || last.SourcePath != n {
			t.Fatalf("result lacks source_path: %s", out)
		}
	}
	if strings.Join(last.Attached, ",") != "report.txt,scratch.txt" {
		t.Fatalf("attached_to_reply=%v", last.Attached)
	}
}

// computer_files writes come back with the absolute place the file lives, via
// the real guest, through the host tool.
func TestComputerFilesWriteResultCarriesLocation(t *testing.T) {
	s, root, _ := newGuestInputServer(t)
	bot, _ := s.store.CreateBot("synthetic", "", "model")
	conv, _ := s.store.GetConversation(bot.DMConversationID)
	_, run, _ := sendWithFiles(t, s, conv.ID, bot.ID, "save a note", "rm-2", nil, nil)
	out, err := s.microVMAction(context.Background(), run, "files.write", json.RawMessage(`{"path":"state/baseline.md","content":"synthetic"}`))
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Location string `json:"location"`
	}
	if json.Unmarshal([]byte(out), &res) != nil || !strings.HasPrefix(res.Location, root) || !strings.HasSuffix(res.Location, filepath.Join("state", "baseline.md")) {
		t.Fatalf("write result lacks the absolute location: %s (root %s)", out, root)
	}
}
