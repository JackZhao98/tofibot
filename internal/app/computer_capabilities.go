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
// computer. Available is "a computer is attached" (its live state, ready or
// asleep, still comes from the computer info endpoint).
type ComputerCapabilities struct {
	Backend      string   `json:"backend"`
	Available    bool     `json:"available"`
	Capabilities []string `json:"capabilities"`
	// PersonalComputers is true only while the pairing routes are on (TOFI_PERSONAL_COMPUTERS=1).
	PersonalComputers bool `json:"personal_computers,omitempty"`
}

func (s *Server) computerCapabilities() ComputerCapabilities {
	if s.computer() == nil {
		return ComputerCapabilities{Backend: ComputerBackendNone, Capabilities: []string{}, PersonalComputers: s.personalComputers}
	}
	return ComputerCapabilities{Backend: ComputerBackendKVM, Available: true, Capabilities: append([]string(nil), kvmCapabilities...), PersonalComputers: s.personalComputers}
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
