package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// MCP changes are workspace-wide and persistent: an MCP server receives
// requests from the App. A published Skill version is workspace-wide too: every
// Bot that has the skill enabled follows the newest version. A Bot may
// therefore only make these with a person's decision on this exact change, and
// never in a run nobody attends. A Bot installing a skill, or turning a library
// skill on or off for itself, touches no other Bot and needs no approval
// (decision 0058).
func extensionChangeGated(action string) bool {
	switch action {
	case "mcp_create", "mcp_update", "mcp_delete", "mcp_test", "skill_publish_version":
		return true
	}
	return false
}

// extensionChangeToken names one change. It carries a digest of the complete
// arguments, so an approval cannot be reused for a different payload.
func extensionChangeToken(x extensionToolArgs) string {
	canonical, _ := json.Marshal(x)
	sum := sha256.Sum256(canonical)
	return fmt.Sprintf("extension %s %s #%s", x.Action, strings.TrimSpace(x.Name), hex.EncodeToString(sum[:])[:12])
}

// guardExtensionChange returns settle, which the caller invokes with the result
// of the change. The approval is spent when the change is allowed to start; a
// failure that is certain to have changed nothing gives it back, so a typo or a
// stale name does not burn the person's decision. Any other outcome keeps it spent.
func (s *Server) guardExtensionChange(c Conversation, r Run, x extensionToolArgs) (settle func(error), err error) {
	if !extensionChangeGated(x.Action) {
		return func(error) {}, nil
	}
	unattended, err := mcpRunRootedInSchedule(s.store.db, r)
	if err != nil || unattended || !c.UserVisible {
		return nil, tooloutcome.New(tooloutcome.Denied, "extension_change_needs_owner", "not_executed",
			"Changing workspace extensions needs a person's decision and no one attends this run, so nothing was changed. Do not retry or route around it; say in your reply what change is needed so the owner can make it in chat or Settings.",
			"explain_blocker").Err()
	}
	if x.Action == "skill_publish_version" {
		// A Bot that did not install the skill, or a payload that changes
		// nothing, is refused before any card is shown.
		if err := s.skillPublishGuard(r.BotID, x); err != nil {
			return nil, err
		}
	}
	if (x.Action == "mcp_create" || x.Action == "mcp_update") && !s.allowInternalMCPHosts {
		if err := refuseInternalMCPHost(x.URL); err != nil {
			return nil, err
		}
		if x.OAuth != nil && strings.TrimSpace(x.OAuth.MetadataURL) != "" {
			if err := refuseInternalMCPHost(x.OAuth.MetadataURL); err != nil {
				return nil, err
			}
		}
	}
	token := extensionChangeToken(x)
	if id, ok := s.claimApproval(r, token); ok {
		return func(execErr error) { s.settleApprovalClaim(id, execErr) }, nil
	}
	// The card for this token shows these facts, built here from the payload and
	// not from anything the model writes.
	s.rememberExtensionFacts(r, token, s.extensionChangeFacts(x, token))
	return nil, tooloutcome.New(tooloutcome.NeedApproval, "extension_change_needs_approval", "not_executed",
		fmt.Sprintf("This extension change is workspace-wide and needs the user's approval of exactly this change. Ask with request_approval, put %q in target, describe the change and its effect plainly in action and impact (the card also shows the server-verified details of the change), then repeat the same call unchanged after they approve. The approval allows one execution of this exact payload.", token),
		"request_approval").Err()
}

// rememberExtensionFacts keeps the facts of a refused change until the card
// for its token is created in the same run.
func (s *Server) rememberExtensionFacts(r Run, token string, facts *ApprovalFacts) {
	s.extensionProposals.Store(r.ID+"\x00"+approvalLabel(token), facts)
}

// extensionFactsForTarget returns the facts for an approval target that names
// an extension change, and whether the target is one at all.
func (s *Server) extensionFactsForTarget(r Run, target string) (facts *ApprovalFacts, isExtension bool) {
	label := approvalLabel(target)
	if !strings.HasPrefix(label, "extension ") {
		return nil, false
	}
	if v, ok := s.extensionProposals.Load(r.ID + "\x00" + label); ok {
		return v.(*ApprovalFacts), true
	}
	return nil, true
}

// extensionChangeFacts describes the change from its payload, independent of
// the model's wording. Secrets are never included: headers appear by name only
// and URL query values are masked.
func (s *Server) extensionChangeFacts(x extensionToolArgs, token string) *ApprovalFacts {
	f := &ApprovalFacts{Kind: x.Action, Name: strings.TrimSpace(x.Name), ChangeID: token[strings.LastIndex(token, "#"):]}
	switch x.Action {
	case "mcp_create", "mcp_update":
		f.Source = approvalSourceURL(x.URL)
		f.Transport = "streamable_http"
		if x.Transport != nil {
			f.Transport = *x.Transport
		}
		if x.ToolAllowlist != nil {
			f.AllowTools = append([]string(nil), *x.ToolAllowlist...)
		}
		if x.ToolDenylist != nil {
			f.DenyTools = append([]string(nil), *x.ToolDenylist...)
		}
		for k := range x.Headers {
			f.HeaderNames = append(f.HeaderNames, k)
		}
		sort.Strings(f.HeaderNames)
		if x.OAuth != nil {
			f.OAuthScopes = append([]string(nil), x.OAuth.Scopes...)
			f.OAuthMetadata = approvalSourceURL(x.OAuth.MetadataURL)
		}
		if s.extensions != nil {
			if servers, err := s.extensions.ListMCP(); err == nil {
				for _, v := range servers {
					if v.Name == f.Name {
						f.PreviousSource = approvalSourceURL(v.URL)
						if x.Transport == nil {
							f.Transport = v.Transport
						}
						if x.ToolAllowlist == nil {
							f.AllowTools = v.ToolAllowlist
						}
						if x.ToolDenylist == nil {
							f.DenyTools = v.ToolDenylist
						}
					}
				}
			}
		}
		if f.Transport == "" {
			f.Transport = "streamable_http"
		}
	case "mcp_delete", "mcp_test":
		if s.extensions != nil {
			if servers, err := s.extensions.ListMCP(); err == nil {
				for _, v := range servers {
					if v.Name == f.Name {
						f.Source, f.Transport = approvalSourceURL(v.URL), v.Transport
					}
				}
			}
		}
	case "skill_install", "skill_publish_version":
		files := stringFiles(x.Files)
		sum := extensions.SummarizeSkillFiles(files)
		f.Skill = &ApprovalSkillFacts{Description: sum.Description, Excerpt: sum.Excerpt, AllowedTools: sum.AllowedTools, Files: sum.Files, HasScripts: sum.HasScripts}
		if x.Action == "skill_publish_version" {
			if preview, err := s.previewSkillPublish(f.Name, files); err == nil {
				f.Skill.NewVersion = preview.Version
				f.Skill.Added, f.Skill.Removed, f.Skill.Changed = preview.Added, preview.Removed, preview.Changed
				f.Skill.EnabledBots = preview.EnabledBots
			}
		}
	}
	return f
}

// approvalSourceURL shows where a server lives: no credentials, query values
// masked, fragment dropped.
func approvalSourceURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "[unparseable URL]"
	}
	u.User, u.Fragment, u.RawFragment = nil, "", ""
	if q := u.Query(); len(q) > 0 {
		keys := make([]string, 0, len(q))
		for k := range q {
			keys = append(keys, url.QueryEscape(k)+"=***")
		}
		sort.Strings(keys)
		u.RawQuery = strings.Join(keys, "&")
	}
	return u.String()
}

// mcpHostLookup resolves a host name; tests replace it.
var mcpHostLookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	return addrs, err
}

// refuseInternalMCPHost rejects a URL whose host is, or resolves to, a
// loopback, private, link-local, CGNAT, metadata or otherwise internal
// address, and fails closed when that cannot be established (parse error, DNS
// failure or timeout, odd numeric forms). A Bot-created server must not make
// the App send requests into the local network. The same ranges are enforced
// again at every connection (extensions.GuardedTransport), so a name that
// later resolves elsewhere is still refused. Owners add such servers in Settings.
func refuseInternalMCPHost(raw string) error {
	denied := func(code, msg string) error {
		return tooloutcome.New(tooloutcome.Denied, code, "not_executed", msg, "explain_blocker").Err()
	}
	internal := denied("mcp_internal_host", "MCP servers on loopback, private, link-local, CGNAT or unqualified hosts can only be added by the owner in Settings.")
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return denied("mcp_host_unverified", "The MCP server address could not be understood, so it was not checked and nothing was changed.")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return denied("mcp_host_unverified", "MCP servers must use an http or https address.")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if strings.Contains(host, "%") {
		return internal
	}
	if a, err := netip.ParseAddr(host); err == nil {
		if extensions.BlockedAddr(a) {
			return internal
		}
		return nil
	}
	labels := strings.Split(host, ".")
	// A last label made only of digits (or 0x hex) is an IPv4 spelling such as
	// 2130706433, 0177.0.0.1 or 127.1, which resolvers expand to a real address.
	if last := labels[len(labels)-1]; last != "" && strings.Trim(last, "0123456789") == "" || strings.HasPrefix(last, "0x") {
		return internal
	}
	if host == "localhost" || !strings.Contains(host, ".") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".lan") || strings.HasSuffix(host, ".home.arpa") || strings.HasSuffix(host, ".localdomain") {
		return internal
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addrs, err := mcpHostLookup(ctx, host)
	if err != nil || len(addrs) == 0 {
		return denied("mcp_host_unverified", "The MCP server host could not be resolved, so it was not verified as a public address and nothing was changed.")
	}
	for _, a := range addrs {
		if extensions.BlockedAddr(a) {
			return internal
		}
	}
	return nil
}
