package computer

import (
	"context"
	"net/http"
	"time"
)

// Kind names for the computers this package knows. A provider reports its kind
// through Provider.Kind; the server stores a row kind (below) in `computers.kind`.
const (
	KindFirecracker = "firecracker"
	// RowKindVM is the stored kind of a computer the administrator assigns to an
	// account (today always a Firecracker VM).
	RowKindVM = "vm"
	// RowKindPersonal is a computer the person owns and connects themselves.
	RowKindPersonal = "personal"
)

// DefaultID is the route segment of the Firecracker computer. Routes under
// /api/computers/{id}/ resolve {id} through Provider.ID, and this value stays
// valid so the web app and the host tool need no change.
const DefaultID = "firecracker"

// Provider is one attached computer. A nil Provider is the valid "no computer"
// state; nothing in this package reports a kind "none".
//
// The interface holds only what every kind can do. Everything else is an
// optional interface below that the server type-asserts behind a capability,
// so a provider that lacks a feature does not list the capability and its tool
// is not registered.
type Provider interface {
	// ID is the route segment and the row id of the computer.
	ID() string
	// Kind is the implementation: "firecracker" today.
	Kind() string
	Info(ctx context.Context) (Info, error)
	Action(ctx context.Context, action Action) (ActionResult, error)
	// ActionWithin is Action with a deadline for the computer's answer.
	ActionWithin(ctx context.Context, action Action, limit time.Duration) (ActionResult, error)
	Retry(ctx context.Context) error
	// Purge wipes the computer's own state; server-owned data is untouched by construction.
	Purge(ctx context.Context) (Info, error)
}

// Healther is implemented by a provider whose guest can be probed and restarted.
type Healther interface {
	Health(ctx context.Context) (GuestHealth, error)
	Recover(ctx context.Context) error
}

// Heartbeater is implemented by a provider that is connected from the other
// side (a person's own machine): it reports when the computer last answered.
// The server marks the computer lost when the time is too old.
type Heartbeater interface {
	LastSeen() time.Time
}

// Streamer serves the live desktop video (capability desktop_stream).
type Streamer interface {
	DesktopStreamWithCursor(ctx context.Context, botID, cursor string) (*http.Response, error)
}

// Resourcer reads and changes the computer's CPU, memory and disk (capability resources).
type Resourcer interface {
	Resources(ctx context.Context) (Resources, error)
	ConfigureResources(ctx context.Context, value ResourceAllocation) (Resources, error)
	ApplyResources(ctx context.Context) (Info, error)
}

// OAuthBridge is the loopback sign-in bridge inside the guest (capability oauth_bridge).
type OAuthBridge interface {
	OAuthStart(ctx context.Context) (OAuthStartResult, error)
	OAuthArm(ctx context.Context, sessionID, state string) (OAuthArmResult, error)
	OAuthPoll(ctx context.Context, sessionID string) (OAuthPollResult, error)
	OAuthCancel(ctx context.Context, sessionID string) (OAuthCancelResult, error)
}

// RunnerHost is the legacy in-guest MCP Runner transport (capability mcp_runner).
type RunnerHost interface {
	RunnerTransport() http.RoundTripper
}

// LegacyBlobs reads and deletes the guest blob objects that rows written before
// the server file store still point at. There is no Put: new bytes never go to a computer first.
type LegacyBlobs interface {
	GetBlob(ctx context.Context, id string) ([]byte, error)
	DeleteBlob(ctx context.Context, id string) error
}

// Socketed is implemented by a provider that is reached over one fixed control socket.
type Socketed interface {
	Socket() string
}

// ID reports the route segment of this computer.
func (c *Client) ID() string {
	if c.id != "" {
		return c.id
	}
	return DefaultID
}

// Kind reports the implementation.
func (c *Client) Kind() string { return KindFirecracker }

var (
	_ Provider    = (*Client)(nil)
	_ Healther    = (*Client)(nil)
	_ Streamer    = (*Client)(nil)
	_ Resourcer   = (*Client)(nil)
	_ OAuthBridge = (*Client)(nil)
	_ RunnerHost  = (*Client)(nil)
	_ LegacyBlobs = (*Client)(nil)
	_ Socketed    = (*Client)(nil)
)
