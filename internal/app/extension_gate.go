package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// Extension changes are workspace-wide and persistent: a Skill description is
// loaded into every Bot's prompt and an MCP server receives requests from the
// App. A Bot may therefore only make them with a person's decision on this
// exact change, and never in a run nobody attends.
func extensionChangeGated(action string) bool {
	switch action {
	case "mcp_create", "mcp_update", "mcp_delete", "mcp_test", "skill_install", "skill_delete":
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

func (s *Server) guardExtensionChange(c Conversation, r Run, x extensionToolArgs) error {
	if !extensionChangeGated(x.Action) {
		return nil
	}
	unattended, err := mcpRunRootedInSchedule(s.store.db, r)
	if err != nil || unattended || !c.UserVisible {
		return tooloutcome.New(tooloutcome.Denied, "extension_change_needs_owner", "not_executed",
			"Changing workspace extensions needs a person's decision and no one attends this run, so nothing was changed. Do not retry or route around it; say in your reply what change is needed so the owner can make it in chat or Settings.",
			"explain_blocker").Err()
	}
	if x.Action == "skill_install" || x.Action == "skill_delete" {
		// Access-restricted skills keep their own refusal, before any card.
		if err := s.restrictedSkillGuard(r.BotID, x.Name); err != nil {
			return err
		}
	}
	if (x.Action == "mcp_create" || x.Action == "mcp_update") && !s.allowInternalMCPHosts {
		if err := refuseInternalMCPHost(x.URL); err != nil {
			return err
		}
	}
	token := extensionChangeToken(x)
	if s.claimApprovalTarget(r, token) {
		return nil
	}
	return tooloutcome.New(tooloutcome.NeedApproval, "extension_change_needs_approval", "not_executed",
		fmt.Sprintf("This extension change is workspace-wide and needs the user's approval of exactly this change. Ask with request_approval, put %q in target, describe the change and its effect plainly in action and impact, then repeat the same call unchanged after they approve. The approval allows one execution of this exact payload.", token),
		"request_approval").Err()
}

// refuseInternalMCPHost rejects loopback, private, link-local and unqualified
// hosts: a Bot-created server must not make the App send requests into the
// local network. Owners add such servers through Settings.
func refuseInternalMCPHost(raw string) error {
	denied := tooloutcome.New(tooloutcome.Denied, "mcp_internal_host", "not_executed",
		"MCP servers on loopback, private, link-local or unqualified hosts can only be added by the owner in Settings.", "explain_blocker").Err()
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return nil
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || !strings.Contains(host, ".") && net.ParseIP(host) == nil || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".localhost") {
		return denied
	}
	ips := []net.IP{net.ParseIP(host)}
	if ips[0] == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		addrs, lookupErr := net.DefaultResolver.LookupIPAddr(ctx, host)
		if lookupErr != nil {
			return nil
		}
		ips = ips[:0]
		for _, a := range addrs {
			ips = append(ips, a.IP)
		}
	}
	for _, ip := range ips {
		if ip == nil {
			continue
		}
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
			return denied
		}
	}
	return nil
}
