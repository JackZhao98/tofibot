package webtools

import (
	"html"
	"net/url"
	"strings"
)

// Doc is a page reduced to readable Markdown.
type Doc struct {
	Title    string
	Markdown string
	// Scripts counts script elements with a source: a page with almost no text
	// and scripts builds its content in the browser.
	Scripts int
}

type node struct {
	tag      string
	attrs    map[string]string
	text     string // text node when tag == ""
	children []*node
}

var voidTags = map[string]bool{"br": true, "hr": true, "img": true, "input": true, "meta": true, "link": true, "area": true, "base": true, "col": true, "embed": true, "source": true, "track": true, "wbr": true}
var rawTextTags = map[string]bool{"script": true, "style": true, "textarea": true, "title": true}
var dropTags = map[string]bool{"script": true, "style": true, "noscript": true, "template": true, "svg": true, "iframe": true, "canvas": true, "head": true, "object": true, "select": true, "button": true}
var chromeTags = map[string]bool{"nav": true, "footer": true, "aside": true, "header": true}

// StripTags removes tags and decodes entities from a short snippet.
func StripTags(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>' && in:
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return html.UnescapeString(b.String())
}

func parseAttrs(s string) map[string]string {
	attrs := map[string]string{}
	i := 0
	for i < len(s) {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' || s[i] == '/') {
			i++
		}
		j := i
		for j < len(s) && !strings.ContainsRune(" \t\n\r=/>", rune(s[j])) {
			j++
		}
		if j == i {
			i++
			continue
		}
		name := strings.ToLower(s[i:j])
		i = j
		for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
			i++
		}
		val := ""
		if i < len(s) && s[i] == '=' {
			i++
			for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
				i++
			}
			if i < len(s) && (s[i] == '"' || s[i] == '\'') {
				q := s[i]
				k := strings.IndexByte(s[i+1:], q)
				if k < 0 {
					val, i = s[i+1:], len(s)
				} else {
					val, i = s[i+1:i+1+k], i+k+2
				}
			} else {
				k := i
				for k < len(s) && !strings.ContainsRune(" \t\n\r>", rune(s[k])) {
					k++
				}
				val, i = s[i:k], k
			}
		}
		if _, dup := attrs[name]; !dup {
			attrs[name] = html.UnescapeString(val)
		}
	}
	return attrs
}

// parseTree is a tolerant HTML reader: unknown or unbalanced markup never fails.
func parseTree(src string) *node {
	root := &node{tag: "#root"}
	stack := []*node{root}
	top := func() *node { return stack[len(stack)-1] }
	add := func(n *node) { top().children = append(top().children, n) }
	i := 0
	for i < len(src) {
		lt := strings.IndexByte(src[i:], '<')
		if lt < 0 {
			add(&node{text: src[i:]})
			break
		}
		if lt > 0 {
			add(&node{text: src[i : i+lt]})
		}
		i += lt
		rest := src[i:]
		switch {
		case strings.HasPrefix(rest, "<!--"):
			k := strings.Index(rest, "-->")
			if k < 0 {
				return root
			}
			i += k + 3
			continue
		case strings.HasPrefix(rest, "<!") || strings.HasPrefix(rest, "<?"):
			k := strings.IndexByte(rest, '>')
			if k < 0 {
				return root
			}
			i += k + 1
			continue
		}
		gt := strings.IndexByte(rest, '>')
		if gt < 0 {
			add(&node{text: rest})
			break
		}
		inner := rest[1:gt]
		i += gt + 1
		if strings.HasPrefix(inner, "/") {
			name := strings.ToLower(strings.TrimSpace(inner[1:]))
			for k := len(stack) - 1; k > 0; k-- {
				if stack[k].tag == name {
					stack = stack[:k]
					break
				}
			}
			continue
		}
		if inner == "" || !(inner[0] >= 'A' && inner[0] <= 'Z' || inner[0] >= 'a' && inner[0] <= 'z') {
			add(&node{text: "<" + inner + ">"})
			continue
		}
		end := 0
		for end < len(inner) && !strings.ContainsRune(" \t\n\r/", rune(inner[end])) {
			end++
		}
		name := strings.ToLower(inner[:end])
		n := &node{tag: name, attrs: parseAttrs(inner[end:])}
		// A paragraph or list item ends the previous open one.
		if (name == "p" || name == "li") && top().tag == name {
			stack = stack[:len(stack)-1]
		}
		add(n)
		selfClose := strings.HasSuffix(strings.TrimSpace(inner), "/")
		if voidTags[name] || selfClose {
			continue
		}
		if rawTextTags[name] {
			closeTag := "</" + name
			k := strings.Index(strings.ToLower(src[i:]), closeTag)
			if k < 0 {
				n.children = []*node{{text: src[i:]}}
				i = len(src)
			} else {
				n.children = []*node{{text: src[i : i+k]}}
				e := strings.IndexByte(src[i+k:], '>')
				if e < 0 {
					i = len(src)
				} else {
					i += k + e + 1
				}
			}
			continue
		}
		if len(stack) < 400 {
			stack = append(stack, n)
		}
	}
	return root
}

func (n *node) find(match func(*node) bool) *node {
	if n.tag != "" && match(n) {
		return n
	}
	for _, c := range n.children {
		if f := c.find(match); f != nil {
			return f
		}
	}
	return nil
}

func (n *node) plain() string {
	if n.tag == "" {
		return n.text
	}
	var b strings.Builder
	for _, c := range n.children {
		b.WriteString(c.plain())
	}
	return b.String()
}

type renderer struct {
	b      strings.Builder
	base   *url.URL
	chrome bool // drop nav, header, footer and aside
	pre    int
	quote  int
}

func (r *renderer) nl(n int) {
	s := r.b.String()
	have := 0
	for i := len(s) - 1; i >= 0 && s[i] == '\n'; i-- {
		have++
	}
	if len(s) == 0 {
		return
	}
	for ; have < n; have++ {
		r.b.WriteByte('\n')
	}
}

func (r *renderer) text(s string) {
	s = html.UnescapeString(s)
	if r.pre > 0 {
		r.b.WriteString(s)
		return
	}
	var out strings.Builder
	space := false
	for _, c := range s {
		if c == ' ' || c == '\n' || c == '\t' || c == '\r' || c == ' ' {
			space = true
			continue
		}
		if space {
			out.WriteByte(' ')
			space = false
		}
		out.WriteRune(c)
	}
	if space {
		out.WriteByte(' ')
	}
	t := out.String()
	cur := r.b.String()
	if strings.HasPrefix(t, " ") && (cur == "" || strings.HasSuffix(cur, "\n") || strings.HasSuffix(cur, " ")) {
		t = strings.TrimLeft(t, " ")
	}
	if r.quote > 0 && (cur == "" || strings.HasSuffix(cur, "\n")) && t != "" {
		r.b.WriteString("> ")
	}
	r.b.WriteString(t)
}

func (r *renderer) resolve(href string) string {
	href = strings.TrimSpace(href)
	if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(strings.ToLower(href), "javascript:") || strings.HasPrefix(strings.ToLower(href), "mailto:") {
		return ""
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if r.base != nil {
		u = r.base.ResolveReference(u)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.String()
}

func (r *renderer) children(n *node) {
	for _, c := range n.children {
		r.walk(c)
	}
}

func (r *renderer) walk(n *node) {
	if n.tag == "" {
		r.text(n.text)
		return
	}
	if dropTags[n.tag] || (r.chrome && chromeTags[n.tag]) || hasAttr(n, "hidden") || n.attrs["aria-hidden"] == "true" {
		return
	}
	switch n.tag {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		r.nl(2)
		r.b.WriteString(strings.Repeat("#", int(n.tag[1]-'0')) + " ")
		r.children(n)
		r.nl(2)
	case "p", "div", "section", "article", "main", "ul", "ol", "table", "figure", "details", "dl", "address", "fieldset":
		r.nl(2)
		r.children(n)
		r.nl(2)
	case "blockquote":
		r.nl(2)
		r.quote++
		r.children(n)
		r.quote--
		r.nl(2)
	case "br":
		r.b.WriteByte('\n')
	case "hr":
		r.nl(2)
		r.b.WriteString("---")
		r.nl(2)
	case "li", "dt", "dd":
		r.nl(1)
		r.b.WriteString("- ")
		r.children(n)
		r.nl(1)
	case "tr":
		r.nl(1)
		r.children(n)
		r.nl(1)
	case "td", "th":
		r.children(n)
		r.b.WriteString(" | ")
	case "pre":
		r.nl(2)
		r.b.WriteString("```\n")
		r.pre++
		r.children(n)
		r.pre--
		r.nl(1)
		r.b.WriteString("```")
		r.nl(2)
	case "code":
		if r.pre > 0 {
			r.children(n)
			return
		}
		r.b.WriteString("`")
		r.children(n)
		r.b.WriteString("`")
	case "strong", "b":
		r.b.WriteString("**")
		r.children(n)
		r.b.WriteString("**")
	case "em", "i":
		r.b.WriteString("*")
		r.children(n)
		r.b.WriteString("*")
	case "a":
		label := strings.Join(strings.Fields(n.plain()), " ")
		href := r.resolve(n.attrs["href"])
		if href == "" || len(label) < 1 {
			r.children(n)
			return
		}
		r.b.WriteString("[" + strings.ReplaceAll(label, "]", ")") + "](" + href + ")")
	case "img":
		// An image is not text; its alt text is kept when the page has it.
		if alt := strings.TrimSpace(n.attrs["alt"]); alt != "" {
			r.b.WriteString(alt)
		}
	default:
		r.children(n)
	}
}

func hasAttr(n *node, k string) bool { _, ok := n.attrs[k]; return ok }

func tidy(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	inFence := false
	for _, l := range lines {
		if strings.HasPrefix(l, "```") {
			inFence = !inFence
		}
		if !inFence {
			l = strings.TrimRight(l, " \t")
			l = strings.TrimSuffix(l, " |")
		}
		if strings.TrimSpace(l) == "" && !inFence {
			blank++
			if blank > 1 {
				continue
			}
			out = append(out, "")
			continue
		}
		blank = 0
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// ParseHTML reduces a page to Markdown with the same rule the computer's
// browser.read uses: the text of main, article or [role=main], else the whole
// body. Scripts, styles and hidden elements never count; navigation, headers,
// footers and asides are dropped only when the whole body is read.
func ParseHTML(src string, base *url.URL) Doc {
	root := parseTree(src)
	doc := Doc{}
	if t := root.find(func(n *node) bool { return n.tag == "title" }); t != nil {
		doc.Title = strings.Join(strings.Fields(html.UnescapeString(t.plain())), " ")
		if r := []rune(doc.Title); len(r) > 300 {
			doc.Title = string(r[:300])
		}
	}
	var scripts func(*node)
	scripts = func(n *node) {
		if n.tag == "script" && n.attrs["src"] != "" {
			doc.Scripts++
		}
		for _, c := range n.children {
			scripts(c)
		}
	}
	scripts(root)
	render := func(n *node, chrome bool) string {
		r := &renderer{base: base, chrome: chrome}
		r.walk(n)
		return tidy(r.b.String())
	}
	if main := root.find(func(n *node) bool {
		return n.tag == "main" || n.tag == "article" || n.attrs["role"] == "main"
	}); main != nil {
		if s := render(main, false); len([]rune(s)) >= 400 {
			doc.Markdown = s
			return doc
		}
	}
	body := root.find(func(n *node) bool { return n.tag == "body" })
	if body == nil {
		body = root
	}
	doc.Markdown = render(body, true)
	if len([]rune(doc.Markdown)) < 80 {
		if all := render(body, false); len([]rune(all)) > len([]rune(doc.Markdown)) {
			doc.Markdown = all
		}
	}
	return doc
}
