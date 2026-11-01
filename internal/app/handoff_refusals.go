package app

import "github.com/JackZhao98/tofibot/internal/tooloutcome"

// A run starts at most one handoff. The refusals below state the next step so
// the Bot ends its turn instead of retrying.

// errRunHandoffUsed refuses a second handoff or send_group_message in a run.
var errRunHandoffUsed = tooloutcome.New(tooloutcome.Permanent, "handoff_already_used", "not_executed",
	"This run already started its one handoff, and a run starts at most one. The work is dispatched: end your turn now and the colleague's result resumes you. To bring in more members, wait for that result and make the next request in the resumed turn.", "end_turn").Err()

// errAnswerAfterHandoff refuses a say answer after the Bot delegated and posted.
func errAnswerAfterHandoff() error {
	return tooloutcome.New(tooloutcome.Transient, "answer_after_handoff", "not_executed",
		"Your part is already posted and the rest is delegated. Nothing more is said in this turn: end it now, and the colleague's result resumes you to integrate it.", "end_turn").Err()
}
