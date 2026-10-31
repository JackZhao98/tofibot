package guest

// Pruning of a long element tree (decision 2026-10-30): invisible, off-screen
// and decorative elements go, a chain of unlabeled containers with one child
// collapses, and a parent with many children keeps the first ones and a
// "N more" line. Element indexes never change, so a ref from a pruned tree
// still names the same element.

import (
	"fmt"
	"regexp"
	"strings"
)

// ocuTreeBudget is the size a returned tree is pruned to. The model-facing
// limit is about 25 KB; this leaves room for the note and for the app header.
const ocuTreeBudget = 20 * 1024

// ocuHiddenMarker matches the words the engine or a toolkit uses for an
// element that cannot be seen.
// Only a state shown in brackets counts, so a label such as "Show hidden files" stays.
var ocuHiddenMarker = regexp.MustCompile(`(?i)[(\[,]\s*(off-?screen|invisible|not showing|hidden)\s*[)\],]`)

// ocuDecorative are roles that never help an action when they have no label.
var ocuDecorative = map[string]bool{
	"separator": true, "image": true, "icon": true, "filler": true, "unknown": true, "static": true,
	"panel": true, "generic": true, "section": true, "group": true, "layered pane": true, "glass pane": true,
	"redundant object": true, "canvas": true, "drawing area": true, "scroll pane": true, "viewport": true, "split pane": true,
	"tool bar": true, "box": true, "container": true,
}

// ocuContainers are the roles whose unlabeled single-child chains collapse.
var ocuContainers = map[string]bool{
	"panel": true, "generic": true, "section": true, "group": true, "filler": true, "layered pane": true, "glass pane": true,
	"scroll pane": true, "viewport": true, "split pane": true, "box": true, "container": true, "redundant object": true,
}

// pruneOCUTree shrinks a tree text to the budget. A text within the budget is
// returned unchanged.
func pruneOCUTree(text string, budget int) string {
	if len(text) <= budget {
		return text
	}
	pt := parseOCUTree(text)
	if pt.count == 0 {
		return text
	}
	before := len(text)
	removed := 0
	// 1. Off-screen, invisible and decorative elements.
	var prune func(list []*ocuNode) []*ocuNode
	prune = func(list []*ocuNode) []*ocuNode {
		var kept []*ocuNode
		for _, n := range list {
			if ocuHiddenMarker.MatchString(n.raw) {
				removed += countNodes(n)
				continue
			}
			n.children = prune(n.children)
			if len(n.children) == 0 && n.label == "" && (ocuDecorative[n.role] || n.role == "") {
				removed++
				continue
			}
			kept = append(kept, n)
		}
		return kept
	}
	pt.roots = prune(pt.roots)
	// 2. Collapse chains of unlabeled containers with one child.
	var collapse func(list []*ocuNode) []*ocuNode
	collapse = func(list []*ocuNode) []*ocuNode {
		var out []*ocuNode
		for _, n := range list {
			n.children = collapse(n.children)
			if n.label == "" && ocuContainers[n.role] && len(n.children) == 1 && !strings.Contains(n.raw, "\n") {
				removed++
				out = append(out, n.children[0])
				continue
			}
			out = append(out, n)
		}
		return out
	}
	pt.roots = collapse(pt.roots)
	// 3. Cap children per parent, then the depth, until the text fits.
	out := pt.render()
	for _, limit := range []int{60, 30, 15, 8, 4} {
		if len(out) <= budget {
			break
		}
		capChildren(pt.roots, limit, &removed)
		pt.roots = capList(pt.roots, limit, &removed)
		out = pt.render()
	}
	for depth := 12; len(out) > budget && depth >= 2; depth-- {
		cutDepth(pt.roots, 0, depth, &removed)
		out = pt.render()
	}
	note := fmt.Sprintf("[tree pruned from %d to %d bytes: %d elements left out (hidden, decorative, or beyond a limit); lower max_tree_depth or read a part of the app to see more]", before, len(out), removed)
	if len(out) > budget {
		out = strings.ToValidUTF8(out[:budget], "")
		if i := strings.LastIndex(out, "\n"); i > 0 {
			out = out[:i]
		}
	}
	return out + "\n" + note
}

func countNodes(n *ocuNode) int {
	c := 1
	for _, ch := range n.children {
		c += countNodes(ch)
	}
	return c
}

// capChildren keeps the first limit children of every node, with a marker for the rest.
func capChildren(list []*ocuNode, limit int, removed *int) {
	for _, n := range list {
		capChildren(n.children, limit, removed)
		n.children = capList(n.children, limit, removed)
	}
}

// capList returns list cut to limit nodes plus a marker node for the rest.
func capList(list []*ocuNode, limit int, removed *int) []*ocuNode {
	if len(list) <= limit+1 {
		return list
	}
	rest := 0
	for _, n := range list[limit:] {
		if n.role == "" && strings.Contains(n.raw, " more") {
			continue
		}
		rest += countNodes(n)
	}
	*removed += rest
	marker := &ocuNode{raw: fmt.Sprintf("… %d more elements not shown", rest), indent: list[0].indent, parent: list[0].parent}
	return append(list[:limit:limit], marker)
}

// cutDepth removes everything below the given depth.
func cutDepth(list []*ocuNode, depth, max int, removed *int) {
	for _, n := range list {
		if depth+1 >= max && len(n.children) > 0 {
			rest := 0
			for _, c := range n.children {
				rest += countNodes(c)
			}
			*removed += rest
			n.children = []*ocuNode{{raw: fmt.Sprintf("… %d more elements not shown", rest), indent: n.indent + 1, parent: n}}
			continue
		}
		cutDepth(n.children, depth+1, max, removed)
	}
}
