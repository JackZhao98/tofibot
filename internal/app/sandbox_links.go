package app

import (
	"encoding/json"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Some models write links the way a hosted chat sandbox does:
// [name](sandbox:/mnt/data/name.xlsx). Nothing here serves that scheme, so a
// browser treats it as dead. Message.MarshalJSON, the one place every client's
// copy of a message passes through (list responses and stream events), rewrites
// the two forms that name a file this server holds to its plain
// /api/attachments/<id> address. The stored text is never changed, so the
// model's own wording stays auditable; only what is served is normalized.
//
//	sandbox:/api/attachments/<id>   the scheme is dropped
//	sandbox:/<any absolute path>    when the file name matches exactly one file
//	                                attached to that same message
//
// Every other scheme and every sandbox: link that names nothing we hold is
// left exactly as written.
var sandboxLink = regexp.MustCompile(`(?i)(^|[\s(\[<"'*「（])sandbox:(/[^\s)>\]"'<]*)`)

func normalizeSandboxLinks(content string, attached []Attachment) string {
	if !strings.Contains(strings.ToLower(content), "sandbox:") {
		return content
	}
	return sandboxLink.ReplaceAllStringFunc(content, func(match string) string {
		parts := sandboxLink.FindStringSubmatch(match)
		lead, target := parts[1], parts[2]
		if strings.HasPrefix(target, "/api/attachments/") {
			return lead + target
		}
		clean := target
		if i := strings.IndexAny(clean, "?#"); i >= 0 {
			clean = clean[:i]
		}
		if decoded, err := url.PathUnescape(clean); err == nil {
			clean = decoded
		}
		name := path.Base(clean)
		var found string
		for _, a := range attached {
			if a.Name != name {
				continue
			}
			if found != "" && found != a.ID {
				return match // two files with this name: do not guess
			}
			found = a.ID
		}
		if found == "" {
			return match
		}
		return lead + "/api/attachments/" + url.PathEscape(found)
	})
}

// MarshalJSON serves an assistant message with its sandbox: links normalized.
func (m Message) MarshalJSON() ([]byte, error) {
	type plain Message
	if m.Role == "assistant" {
		m.Content = normalizeSandboxLinks(m.Content, m.Attachments)
	}
	return json.Marshal(plain(m))
}
