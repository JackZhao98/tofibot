package webtools

import (
	"context"
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
	// Shell marks a page whose markup shows it is built by scripts: an empty
	// app mount point or a noscript notice asking for JavaScript.
	Shell bool
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
func parseTree(ctx context.Context, src string) *node {
	root := &node{tag: "#root"}
	nodes, tokens := 0, 0
	stack := []*node{root}
	top := func() *node { return stack[len(stack)-1] }
	add := func(n *node) { top().children = append(top().children, n); nodes++ }
	i := 0
	for i < len(src) {
		// A page that would build more nodes than a real one ever has, or a
		// cancelled request, ends the read with what was built so far.
		if tokens++; nodes > maxNodes || (tokens&1023 == 0 && ctx.Err() != nil) {
			return root
		}
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
			k := indexFold(src, i, "</"+name)
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

// indexFold returns the offset from start of the first ASCII case-insensitive
// occurrence of the lower-case needle in s[start:], or -1. It copies nothing.
func indexFold(s string, start int, needle string) int {
	for i := start; ; i++ {
		j := strings.IndexByte(s[i:], '<')
		if j < 0 {
			return -1
		}
		i += j
		if len(s)-i < len(needle) {
			return -1
		}
		ok := true
		for k := 0; k < len(needle); k++ {
			c := s[i+k]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != needle[k] {
				ok = false
				break
			}
		}
		if ok {
			return i - start
		}
	}
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

// plain is the text under n, cut at limit bytes so nested anchors stay cheap.
func (n *node) plain(limit int) string {
	var b strings.Builder
	var walk func(*node)
	walk = func(x *node) {
		if b.Len() >= limit {
			return
		}
		if x.tag == "" {
			b.WriteString(x.text)
			return
		}
		for _, c := range x.children {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// Ceilings for one ParseHTML call, shared by every render it makes.
const (
	maxNodes     = 500000
	maxVisits    = 1500000
	maxRenderLen = 4 << 20
)

type renderer struct {
	ctx    context.Context
	work   *int
	trail  int // newlines at the end of b
	b      strings.Builder
	base   *url.URL
	chrome bool // drop nav, header, footer and aside
	pre    int
	quote  int
}

// w writes s and keeps the count of trailing newlines, so nl never rescans.
func (r *renderer) w(s string) {
	if s == "" {
		return
	}
	r.b.WriteString(s)
	t := 0
	for i := len(s) - 1; i >= 0 && s[i] == '\n'; i-- {
		t++
	}
	if t == len(s) {
		r.trail += t
	} else {
		r.trail = t
	}
}

func (r *renderer) nl(n int) {
	if r.b.Len() == 0 {
		return
	}
	for ; r.trail < n; r.trail++ {
		r.b.WriteByte('\n')
	}
}

// done reports that the budget, the output ceiling or the request is spent.
func (r *renderer) done() bool {
	*r.work++
	if *r.work > maxVisits || r.b.Len() > maxRenderLen {
		return true
	}
	return *r.work&1023 == 0 && r.ctx.Err() != nil
}

func (r *renderer) text(s string) {
	s = html.UnescapeString(s)
	if r.pre > 0 {
		r.w(s)
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
		r.w("> ")
	}
	r.w(t)
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
	if r.done() {
		return
	}
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
		r.w(strings.Repeat("#", int(n.tag[1]-'0')) + " ")
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
		r.w("\n")
	case "hr":
		r.nl(2)
		r.w("---")
		r.nl(2)
	case "li", "dt", "dd":
		r.nl(1)
		r.w("- ")
		r.children(n)
		r.nl(1)
	case "tr":
		r.nl(1)
		r.children(n)
		r.nl(1)
	case "td", "th":
		r.children(n)
		r.w(" | ")
	case "pre":
		r.nl(2)
		r.w("```\n")
		r.pre++
		r.children(n)
		r.pre--
		r.nl(1)
		r.w("```")
		r.nl(2)
	case "code":
		if r.pre > 0 {
			r.children(n)
			return
		}
		r.w("`")
		r.children(n)
		r.w("`")
	case "strong", "b":
		r.w("**")
		r.children(n)
		r.w("**")
	case "em", "i":
		r.w("*")
		r.children(n)
		r.w("*")
	case "a":
		label := strings.Join(strings.Fields(n.plain(2000)), " ")
		href := r.resolve(n.attrs["href"])
		if href == "" || len(label) < 1 {
			r.children(n)
			return
		}
		r.w("[" + strings.ReplaceAll(label, "]", ")") + "](" + href + ")")
	case "img":
		// An image is not text; its alt text is kept when the page has it.
		if alt := strings.TrimSpace(n.attrs["alt"]); alt != "" {
			r.w(alt)
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
func ParseHTML(ctx context.Context, src string, base *url.URL) Doc {
	root := parseTree(ctx, src)
	work := 0
	doc := Doc{}
	if t := root.find(func(n *node) bool { return n.tag == "title" }); t != nil {
		doc.Title = strings.Join(strings.Fields(html.UnescapeString(t.plain(2000))), " ")
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
	mounts := map[string]bool{"root": true, "app": true, "__next": true, "__nuxt": true, "svelte": true, "___gatsby": true}
	doc.Shell = doc.Scripts > 0 && root.find(func(n *node) bool {
		if n.tag == "noscript" {
			return strings.Contains(strings.ToLower(n.plain(2000)), "javascript")
		}
		return n.tag == "div" && mounts[n.attrs["id"]] && len([]rune(strings.TrimSpace(n.plain(200)))) < 40
	}) != nil
	render := func(n *node, chrome bool) string {
		r := &renderer{ctx: ctx, work: &work, base: base, chrome: chrome}
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
