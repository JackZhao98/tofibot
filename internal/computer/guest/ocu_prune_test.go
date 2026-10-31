package guest

import (
	"fmt"
	"strings"
	"testing"
)

// bigOCUTree builds a tree the way a large app prints one: a window with unlabeled
// wrapper chains, separators and images, off-screen menu frames, and a list of
// thousands of rows.
func bigOCUTree() (text string, elements int) {
	var b strings.Builder
	n := 0
	line := func(depth int, rest string) {
		fmt.Fprintf(&b, "%s%d %s\n", strings.Repeat(" ", depth), n, rest)
		n++
	}
	b.WriteString("App=bigapp\n")
	line(0, "window Big App")
	line(1, "menu bar ")
	for i := 0; i < 8; i++ {
		line(2, fmt.Sprintf("menu File%d", i))
		for j := 0; j < 12; j++ {
			line(3, fmt.Sprintf("menu item Action %d.%d (offscreen)", i, j))
		}
	}
	// Wrapper chains around the real controls.
	for i := 0; i < 20; i++ {
		line(1, "panel ")
		line(2, "filler ")
		line(3, "panel ")
		line(4, fmt.Sprintf("push button Tool %d", i))
		line(4, "separator ")
	}
	line(1, "check box Remember me")
	line(1, "scroll pane ")
	line(2, "list Messages")
	for i := 0; i < 3000; i++ {
		line(3, fmt.Sprintf("list item Message number %d from someone@example.com about the quarterly report", i))
		line(4, "image ")
	}
	line(1, "push button OK")
	return b.String(), n
}

func TestLargeTreeIsPrunedWellUnderTheLimit(t *testing.T) {
	text, elements := bigOCUTree()
	if len(text) < 200*1024 {
		t.Fatalf("the fixture is only %d bytes; it must be a large tree", len(text))
	}
	out := pruneOCUTree(text, ocuTreeBudget)
	t.Logf("%d elements, %d bytes -> %d bytes", elements, len(text), len(out))
	if len(out) > 22*1024 {
		t.Fatalf("pruned tree is %d bytes; the target is well under 25 KB", len(out))
	}
	for _, want := range []string{"App=bigapp", "0 window Big App", "check box Remember me", "Tool 0", "Tool 19", "more elements not shown", "[tree pruned from"} {
		if !strings.Contains(out, want) {
			t.Errorf("pruned tree lacks %q", want)
		}
	}
	// Off-screen frames are gone; the controls keep the index the engine gave them.
	if strings.Contains(out, "(offscreen)") {
		t.Error("an off-screen element survived")
	}
	if !strings.Contains(out, "Tool 3") || strings.Contains(out, "separator") {
		t.Error("decorative separators must go and labeled controls stay")
	}
	idx := map[string]string{}
	for _, l := range strings.Split(text, "\n") {
		if m := ocuTreeLine.FindStringSubmatch(l); m != nil {
			idx[m[2]] = strings.TrimSpace(m[3])
		}
	}
	for _, l := range strings.Split(out, "\n") {
		if m := ocuTreeLine.FindStringSubmatch(l); m != nil && idx[m[2]] != strings.TrimSpace(m[3]) {
			t.Fatalf("index %s changed: %q became %q", m[2], idx[m[2]], m[3])
		}
	}
	// The wrapper chain of one control is collapsed: panel, filler, panel around a button.
	if strings.Contains(out, "filler") {
		t.Error("an unlabeled single-child chain was not collapsed")
	}
}

func TestSmallTreeIsReturnedUnchanged(t *testing.T) {
	if out := pruneOCUTree(ocuSettingsTree, ocuTreeBudget); out != ocuSettingsTree {
		t.Fatalf("a small tree must not change:\n%s", out)
	}
}

func TestPrunedTreeKeepsNestingByDepth(t *testing.T) {
	var b strings.Builder
	b.WriteString("App=a\n0 window W\n 1 panel \n  2 panel \n   3 list L\n")
	for i := 0; i < 900; i++ {
		fmt.Fprintf(&b, "    %d list item Row %d with a fairly long label to take space in the tree text\n", 4+i, i)
	}
	out := pruneOCUTree(b.String(), 4000)
	if len(out) > 4000+400 {
		t.Fatalf("not pruned to the budget: %d", len(out))
	}
	// The two wrapper panels collapse, so the list sits one level under the window.
	if !strings.Contains(out, "\n 3 list L\n") {
		t.Fatalf("collapsed chain not re-indented:\n%.300s", out)
	}
	if !strings.Contains(out, "… ") || !strings.Contains(out, " more elements not shown") {
		t.Fatal("the cut rows need an N more marker")
	}
}

// A label that holds the word hidden is an ordinary element; only a hidden state in brackets drops one.
func TestHiddenWordInALabelIsNotAHiddenState(t *testing.T) {
	text := "App=a\n0 window W\n 1 check box Show hidden files\n 2 push button Old [hidden]\n" + strings.Repeat(" 3 push button Row with a long enough label\n", 400)
	out := pruneOCUTree(text, 2000)
	if !strings.Contains(out, "Show hidden files") || strings.Contains(out, "Old [hidden]") {
		t.Fatalf("hidden handling is wrong:\n%.400s", out)
	}
}
