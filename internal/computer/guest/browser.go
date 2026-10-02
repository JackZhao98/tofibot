package guest

// browserCommand is shared by the Linux implementation and the non-Linux
// stub so the action dispatcher can expose the same target contract.
type browserCommand struct {
	Action   string
	URL      string
	TargetID string
}
