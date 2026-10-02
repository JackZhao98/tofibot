package tooloutcome

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Identity describes the effective backend operation, independent of the model's
// wrapper spelling. Resolvers must use the same argument interpretation as Execute.
type Identity struct {
	Scope              string `json:"scope"`
	Operation          string `json:"operation"`
	ArgumentsHash      string `json:"arguments_hash"`
	Target             string `json:"target,omitempty"`
	Risk               string `json:"risk,omitempty"`
	Object             string `json:"object,omitempty"`
	ResolutionRequired bool   `json:"resolution_required,omitempty"`
}

const (
	Observation    = "observation"
	TargetMutation = "target_mutation"
	OpaqueEffect   = "opaque_effect"
)

func OperationIdentity(scope, operation string, args json.RawMessage) Identity {
	d := json.NewDecoder(bytes.NewReader(args))
	d.UseNumber()
	var v any
	if d.Decode(&v) == nil {
		if canonical, err := json.Marshal(v); err == nil {
			args = canonical
		}
	}
	h := sha256.Sum256(args)
	return Identity{Scope: scope, Operation: operation, ArgumentsHash: hex.EncodeToString(h[:]), Risk: OpaqueEffect}
}

func DefaultIdentity(name string, args json.RawMessage) Identity {
	if name == "call_mcp_tool" {
		var in struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(args, &in) == nil && in.Name != "" {
			return OperationIdentity(name, in.Name, in.Arguments)
		}
	}
	i := OperationIdentity("tool", name, args)
	switch name {
	case "get_context_usage", "search_history", "inspect_recent_runs", "list_computers", "list_bots", "list_skills", "list_mcp_servers", "search_mcp_tools", "workflow_guide", "read_workflow_guide", "computer_help", "workspace_capabilities", "read_skill", "read_skill_file":
		i.Risk = Observation
	}
	return i
}

// Bounded retains a valid, independently bounded control envelope even when the
// human/model-facing result body is too long for activity storage.
func Bounded(o *Outcome) *Outcome {
	if o == nil {
		return nil
	}
	copy := *o
	limit := func(s string, n int) string {
		r := []rune(s)
		if len(r) > n {
			return string(r[:n]) + "…"
		}
		return s
	}
	copy.Message = limit(copy.Message, 2048)
	copy.Status = limit(copy.Status, 128)
	copy.Code = limit(copy.Code, 128)
	copy.Certainty = limit(copy.Certainty, 128)
	copy.NextAction = limit(copy.NextAction, 128)
	return &copy
}
