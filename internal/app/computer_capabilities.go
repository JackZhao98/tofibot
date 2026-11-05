package app

// Computer backends. The server runs the same with or without a computer:
// "none" has no Worker client, no computer_* tools and no computer wording in
// prompts; "kvm" is the Firecracker Worker. docs/design/computer-optional.md.
const (
	ComputerBackendNone = "none"
	ComputerBackendKVM  = "kvm"
)

// Computer capability names the UI and prompts can ask for. A backend lists
// the ones it serves; none lists nothing.
const (
	CapShell         = "shell"
	CapFiles         = "files"
	CapBrowser       = "browser"
	CapDesktop       = "desktop"
	CapDesktopStream = "desktop_stream"
	CapTerminal      = "terminal"
	CapHibernate     = "hibernate"
	CapMCPRunner     = "mcp_runner"
	CapOAuthBridge   = "oauth_bridge"
	CapSecretSink    = "secret_sink"
	CapResources     = "resources"
)

var kvmCapabilities = []string{CapShell, CapFiles, CapBrowser, CapDesktop, CapDesktopStream, CapTerminal, CapHibernate, CapMCPRunner, CapOAuthBridge, CapSecretSink, CapResources}

// ComputerCapabilities is what a client may assume about this workspace's
// computer. Available is "a Bot computer is attached" (its live state, ready or
// asleep, still comes from the computer info endpoint).
type ComputerCapabilities struct {
	// Backend and Available describe this account's computer: "none" while none is
	// attached, even when the host could attach one.
	Backend   string `json:"backend"`
	Available bool   `json:"available"`
	// Attached repeats Available under the word the Admin console uses.
	Attached bool `json:"attached"`
	// HostBackend says whether this server can run a computer at all ("kvm" or
	// "none"), whether or not this account's computer is attached. Attach and
	// detach surfaces follow it; computer tools and settings follow Backend.
	HostBackend  string   `json:"host_backend"`
	Capabilities []string `json:"capabilities"`
	// PersonalComputers is true while the pairing routes are on (unless TOFI_PERSONAL_COMPUTERS=0).
	PersonalComputers bool `json:"personal_computers,omitempty"`
}

func (s *Server) computerCapabilities() ComputerCapabilities {
	host := ComputerBackendNone
	if s.computerFactory != nil {
		host = ComputerBackendKVM
	}
	cur := s.computer()
	if cur == nil {
		return ComputerCapabilities{Backend: ComputerBackendNone, HostBackend: host, Capabilities: []string{}, PersonalComputers: s.personalComputers}
	}
	if cur.Kind() == envKindE2B {
		// HostBackend stays about this server: an E2B computer is not a host backend.
		return ComputerCapabilities{Backend: ComputerBackendE2B, Available: true, Attached: true, HostBackend: host, Capabilities: append([]string(nil), e2bCapabilities...), PersonalComputers: s.personalComputers}
	}
	return ComputerCapabilities{Backend: ComputerBackendKVM, Available: true, Attached: true, HostBackend: ComputerBackendKVM, Capabilities: append([]string(nil), kvmCapabilities...), PersonalComputers: s.personalComputers}
}

// botComputerRowLabel is the stored name and capability list of the Bot computer row.
func botComputerRowLabel(p interface{ Kind() string }) (string, []string) {
	if p.Kind() == envKindE2B {
		return "E2B cloud computer", e2bCapabilities
	}
	return "Bot workspace", kvmCapabilities
}

// hasComputerCapability is the one question prompts and tools ask instead of
// testing s.computer() directly, so a later backend only adds a case here.
func (s *Server) hasComputerCapability(name string) bool {
	for _, c := range s.computerCapabilities().Capabilities {
		if c == name {
			return true
		}
	}
	return false
}
