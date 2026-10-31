package guest

// Shape of the element tree the desktop engine returns (decision 2026-10-30).
// The engine prints one line per element: indentation for nesting, then the
// element index, the role and the label. Two things are done here, on the
// guest, so the model sees them whatever the engine prints:
//
//   - the state of a check box, toggle or radio button (checked, unchecked,
//     mixed) is appended to its line, from the AT-SPI states CHECKED and
//     INDETERMINATE that ocu_states_linux.go reads;
//   - a long tree is pruned (ocu_prune.go).

import (
	"regexp"
	"strings"
)

// ocuTreeLine matches an element line: indentation, index, then role and label.
var ocuTreeLine = regexp.MustCompile(`^([ \t]*)(\d+)[ \t]+(\S.*)$`)

// ocuRoles are the roles with more than one word, longest first, so a line
// "5 check box Remember me" splits into the role "check box" and the label.
var ocuRoles = []string{
	"check menu item", "radio menu item", "toggle button", "popup menu button", "menu item", "check box", "radio button",
	"scroll pane", "scroll bar", "tool bar", "status bar", "title bar", "tab list", "tab panel", "table cell", "table row",
	"column header", "row header", "list item", "tree item", "combo box", "text entry", "password text", "push button",
	"spin button", "page tab list", "page tab", "menu bar", "dialog", "drawing area", "radio",
}

// ocuCheckable are the roles that carry a checked state.
var ocuCheckable = map[string]bool{
	"check box": true, "toggle button": true, "switch": true, "radio button": true,
	"check menu item": true, "radio menu item": true, "checkbox": true, "toggle": true, "radio": true,
}

type ocuNode struct {
	raw      string
	indent   int
	index    string
	role     string
	label    string
	children []*ocuNode
	parent   *ocuNode
}

// splitRoleLabel splits the text after the index into the role and the label.
func splitRoleLabel(rest string) (role, label string) {
	lower := strings.ToLower(rest)
	for _, r := range ocuRoles {
		if lower == r || strings.HasPrefix(lower, r+" ") {
			return r, strings.TrimSpace(rest[len(r):])
		}
	}
	role, label, _ = strings.Cut(rest, " ")
	return strings.ToLower(role), strings.TrimSpace(label)
}

// ocuParsedTree holds the lines of a text split into leading text, element
// lines (as a forest) and lines after the elements that are not elements.
type ocuParsedTree struct {
	head  []string
	roots []*ocuNode
	count int
	unit  string // the indentation of one nesting level
}

func parseOCUTree(text string) *ocuParsedTree {
	pt := &ocuParsedTree{}
	var stack []*ocuNode
	for _, line := range strings.Split(text, "\n") {
		m := ocuTreeLine.FindStringSubmatch(line)
		if m == nil {
			if pt.count == 0 {
				pt.head = append(pt.head, line)
			} else if len(stack) > 0 {
				// A line that is not an element belongs to the element above it
				// (a value on several lines); keep it with that element.
				last := lastNode(stack[len(stack)-1])
				last.raw += "\n" + line
			}
			continue
		}
		n := &ocuNode{raw: line, indent: len(m[1]), index: m[2]}
		if pt.unit == "" && len(stack) > 0 && n.indent > stack[len(stack)-1].indent {
			pt.unit = m[1][stack[len(stack)-1].indent:]
		}
		n.role, n.label = splitRoleLabel(m[3])
		for len(stack) > 0 && stack[len(stack)-1].indent >= n.indent {
			stack = stack[:len(stack)-1]
		}
		if len(stack) > 0 {
			n.parent = stack[len(stack)-1]
			n.parent.children = append(n.parent.children, n)
		} else {
			pt.roots = append(pt.roots, n)
		}
		stack = append(stack, n)
		pt.count++
	}
	return pt
}

// lastNode is the last element of the subtree, in reading order.
func lastNode(n *ocuNode) *ocuNode {
	for len(n.children) > 0 {
		n = n.children[len(n.children)-1]
	}
	return n
}

// render writes the forest back out, each element indented by its depth in the
// tree as it is now (a collapsed container no longer counts as a level).
func (pt *ocuParsedTree) render() string {
	unit := pt.unit
	if unit == "" {
		unit = " "
	}
	var b strings.Builder
	for _, h := range pt.head {
		b.WriteString(h)
		b.WriteString("\n")
	}
	var walk func(n *ocuNode, depth int)
	walk = func(n *ocuNode, depth int) {
		b.WriteString(strings.Repeat(unit, depth))
		b.WriteString(strings.TrimLeft(n.raw, " \t"))
		b.WriteString("\n")
		for _, c := range n.children {
			walk(c, depth+1)
		}
	}
	for _, r := range pt.roots {
		walk(r, 0)
	}
	return strings.TrimRight(b.String(), "\n")
}

// ocuCheckState is the state of one checkable element, read from AT-SPI in
// document order.
type ocuCheckState struct {
	Role  string `json:"role"`
	Name  string `json:"name"`
	State string `json:"state"` // checked, unchecked or mixed
}

// annotateCheckStates appends the state to every checkable element line. The
// states come in document order; they are applied only when they pair one to
// one with the checkable lines of the text and the names agree, so a tree
// that does not match (another window, a changed app) is left as it is.
func annotateCheckStates(text string, states []ocuCheckState) (string, bool) {
	lines := strings.Split(text, "\n")
	var at []int
	for i, line := range lines {
		m := ocuTreeLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		role, _ := splitRoleLabel(m[3])
		if ocuCheckable[role] {
			at = append(at, i)
		}
	}
	if len(at) == 0 || len(at) != len(states) {
		return text, false
	}
	for k, i := range at {
		m := ocuTreeLine.FindStringSubmatch(lines[i])
		_, label := splitRoleLabel(m[3])
		if states[k].Name != "" && label != "" && !strings.Contains(strings.ToLower(label), strings.ToLower(states[k].Name)) &&
			!strings.Contains(strings.ToLower(states[k].Name), strings.ToLower(firstWords(label, 6))) {
			return text, false
		}
		switch states[k].State {
		case "checked", "unchecked", "mixed":
		default:
			return text, false
		}
	}
	for k, i := range at {
		lines[i] = strings.TrimRight(lines[i], " \t") + " [" + states[k].State + "]"
	}
	return strings.Join(lines, "\n"), true
}

func firstWords(s string, n int) string {
	f := strings.Fields(s)
	if len(f) > n {
		f = f[:n]
	}
	return strings.Join(f, " ")
}

// hasCheckable reports whether the text has an element line of a checkable role.
func hasCheckable(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if m := ocuTreeLine.FindStringSubmatch(line); m != nil {
			if role, _ := splitRoleLabel(m[3]); ocuCheckable[role] {
				return true
			}
		}
	}
	return false
}
