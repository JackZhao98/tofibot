package app

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

// The capability picture names only what the Bot computer image installs: a
// name whose package left the Dockerfile fails here, so the prompt cannot
// promise a program the computer lacks.
func TestBotComputerToolboxIsInstalled(t *testing.T) {
	raw, err := os.ReadFile("../../deploy/microvm/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(raw)
	means := botComputerMeans()
	for _, item := range botComputerToolbox {
		if !strings.Contains(means, item.Name) {
			t.Errorf("capability sentence lacks %q", item.Name)
		}
		if len(item.Packages) == 0 {
			t.Errorf("%q names no package", item.Name)
		}
		for _, pkg := range item.Packages {
			if !strings.Contains(dockerfile, pkg) {
				t.Errorf("%q: package %q is not in deploy/microvm/Dockerfile", item.Name, pkg)
			}
		}
	}
	for _, pkg := range botComputerFontPackages {
		if !strings.Contains(dockerfile, pkg) {
			t.Errorf("font package %q is not in deploy/microvm/Dockerfile", pkg)
		}
	}
	if strings.Contains(dockerfile, "\n    pip ") || strings.Contains(means, "pip install") {
		t.Error("the image ships no pip; the sentence must route Python packages through a venv")
	}
}

// A Bot computer prompt carries the capability picture; without a computer
// the always-on text never claims one.
func TestCapabilityPictureFollowsTheComputer(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	bot, err := store.CreateBot("Synthetic", "Keep my role.", "model")
	if err != nil {
		t.Fatal(err)
	}
	dm, err := store.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	with := &Server{store: store, microVM: unreachableComputerClient(t)}
	if prompt := with.microVMEnvironmentPrompt(t.Context(), bot.ID); !strings.Contains(prompt, botComputerMeans()) {
		t.Fatal("the Bot computer prompt lacks the capability picture")
	}
	none := &Server{store: store}
	run := Run{ID: "none", BotID: bot.ID, ConversationID: dm.ID, structured: true}
	_, core := none.buildContextParts(dm, run, bot)
	system, err := assembleRunSystem(runSystemPrompt{Core: core, BotInstructions: bot.Instructions, Computer: none.microVMEnvironmentPrompt(t.Context(), bot.ID), Extensions: extensions.DiscoveryPolicy(true, false) + "Configured MCP sources (names only):\n"})
	if err != nil {
		t.Fatal(err)
	}
	for _, claim := range []string{"own Bot computer", "Bot computer is the fallback", "any website is within reach", "Installed:"} {
		if strings.Contains(system, claim) {
			t.Errorf("no-computer prompt claims a computer: %q", claim)
		}
	}
	if !strings.Contains(noComputerStatus, "Settings > Environments") {
		t.Error("the no-computer fact must say where a Bot computer is added")
	}
}

// The routing policy never names a tool the run does not have.
func TestDiscoveryPolicyNamesOnlyWhatTheRunHas(t *testing.T) {
	full := extensions.DiscoveryPolicy(true, true)
	if !strings.Contains(full, "search_mcp_tools") || !strings.Contains(full, "Bot computer is the fallback") {
		t.Fatal("the full policy lost its MCP or computer sentence")
	}
	if p := extensions.DiscoveryPolicy(true, false); strings.Contains(p, "Bot computer") || !strings.Contains(p, "search_mcp_tools") {
		t.Errorf("MCP without a computer: %q", p)
	}
	for _, computer := range []bool{true, false} {
		p := extensions.DiscoveryPolicy(false, computer)
		if strings.Contains(p, "mcp") || strings.Contains(p, "MCP") || strings.Contains(p, "Bot computer") != computer {
			t.Errorf("no MCP source (computer=%v): %q", computer, p)
		}
		for _, kept := range []string{"cannot grant authorization", "Before telling the person that a capability or connection is unavailable", "Settings > Connections"} {
			if !strings.Contains(p, kept) {
				t.Errorf("no MCP source (computer=%v) lost %q: %q", computer, kept, p)
			}
		}
	}
	directory := "Configured MCP sources (names only):\nEnabled Skills, optional guides for some tasks (name and purpose):\n"
	if got := extensions.WithDiscoveryPolicy(full+directory, false, true); got != extensions.DiscoveryPolicy(false, true)+directory {
		t.Errorf("replace kept the old policy: %q", got)
	}
	if got := extensions.WithDiscoveryPolicy("## Skill: x\nbody", false, false); got != "## Skill: x\nbody" {
		t.Errorf("text without a directory must stay as it is: %q", got)
	}
}

// A capability query no tool matches points at the run's general tools and no
// longer tells the model to refuse.
func TestNoCloseMatchNamesTheGeneralTools(t *testing.T) {
	registered := []Tool{
		{Name: "computer_shell", Description: "Run a bounded shell command inside the Bot computer."},
		{Name: "save_memory", Description: "Save a durable fact about the person."},
		{Name: "web_fetch", Description: "Fetch one web page as text."},
	}
	tool := capabilityTool(registered)
	out, err := tool.Execute(t.Context(), json.RawMessage(`{"query":"render an animated spiral galaxy timelapse"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		NoCloseMatch bool     `json:"no_close_match"`
		General      []string `json:"general_tools"`
		Note         string   `json:"note"`
	}
	if json.Unmarshal([]byte(out), &got) != nil || !got.NoCloseMatch {
		t.Fatalf("expected no_close_match: %s", out)
	}
	if strings.Join(got.General, ",") != "computer_shell,web_fetch" {
		t.Errorf("general_tools = %v", got.General)
	}
	if strings.Contains(got.Note, "not available") || !strings.Contains(got.Note, "general_tools") {
		t.Errorf("note = %q", got.Note)
	}
	empty := noCloseMatch(nil, nil)
	if _, ok := empty["general_tools"]; ok || !strings.Contains(empty["note"].(string), "what is missing") {
		t.Errorf("a run with no general tool: %v", empty)
	}
}

// New model-facing text follows the tools text rules: no example sentences or
// contrasts, and the computer_shell description fits the provider limit.
func TestResourcefulTextIsPrincipleOnly(t *testing.T) {
	texts := map[string]string{
		"means":       botComputerMeans(),
		"no computer": noComputerStatus,
		"policy":      extensions.DiscoveryPolicy(true, true) + extensions.DiscoveryPolicy(false, false),
		"no match":    noCloseMatch(nil, []string{"computer_shell"})["note"].(string) + noCloseMatch(nil, nil)["note"].(string),
		"say outcome": sayOutcomeDescription,
		"published":   publishedFilesFact,
	}
	if !strings.Contains(voiceGuidance, "what did not happen, what you tried and what you need") {
		t.Error("the voice guidance must tell a block with what was tried")
	}
	for name, text := range texts {
		lower := strings.ToLower(text)
		for _, banned := range []string{"for example", "e.g.", "such as a video", "instead of", " but "} {
			if strings.Contains(lower, banned) {
				t.Errorf("%s contains %q", name, banned)
			}
		}
	}
}

// The run context says published files reach the person only when a Bot
// computer is there to publish from.
func TestPublishedFilesFactFollowsTheComputer(t *testing.T) {
	s := noComputerServer(t, &captureEngine{})
	if note := s.runContextNote(t.Context()); strings.Contains(note, publishedFilesFact) {
		t.Fatalf("no-computer context claims published files: %s", note)
	}
	attachMissingComputer(t, s)
	if note := s.runContextNote(t.Context()); !strings.Contains(note, publishedFilesFact) {
		t.Fatalf("computer context lacks the published-files fact: %s", note)
	}
	if !strings.Contains(botComputerMeans(), "capture_id") || !strings.Contains(botComputerMeans(), "send_message attachments") {
		t.Error("the capability sentence must name the capture route through send_message attachments")
	}
}
