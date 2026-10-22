package app

import (
	"sync"

	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/saystream"
)

// sayStreamer routes the fragments of say calls to the run's draft. Calls are
// tracked by their index within one model response; the first fragment of a
// call carries its ID and name.
type sayStreamer struct {
	mu    sync.Mutex
	calls map[int]*sayStreamCall
	state *sayRunState
	// toolWork: this model response already holds a call that may arm the
	// completion review, so a say(answer) in it may be refused once.
	toolWork bool
}

type sayStreamCall struct {
	id      string
	dec     saystream.Decoder
	started bool
	// held: the answer may still be sent back for review, so its text is not
	// streamed. If no review follows, the answer is shown whole instead.
	held bool
}

func newSayStreamer(state *sayRunState) *sayStreamer {
	return &sayStreamer{calls: map[int]*sayStreamCall{}, state: state}
}

// Delta consumes one provider tool-call fragment. The draft is touched only
// after the streamer's own lock is released: starting a say call can reset a
// draft kept for review, which resets this streamer too.
func (st *sayStreamer) Delta(d runtime.ToolCallDelta) {
	callID, text, begin := func() (string, string, bool) {
		st.mu.Lock()
		defer st.mu.Unlock()
		if d.ID != "" || d.Name != "" {
			if d.Name == "say" {
				st.calls[d.Index] = &sayStreamCall{id: d.ID}
			} else {
				delete(st.calls, d.Index)
				if completionReviewTool(d.Name) {
					st.toolWork = true
				}
			}
		}
		call := st.calls[d.Index]
		if call == nil || d.Arguments == "" || call.id == "" {
			return "", "", false
		}
		text := call.dec.Feed(d.Arguments)
		begin := false
		if !call.started {
			if _, known := call.dec.Purpose(); !known {
				return "", "", false
			}
			call.started, begin = true, true
			purpose, _ := call.dec.Purpose()
			call.held = st.toolWork || st.state.reviewOwed() || (purpose == sayAnswer && (st.state.gradeIsOwed() || st.state.holdAnswers))
		}
		if call.held {
			return "", "", false
		}
		return call.id, text, begin
	}()
	if callID == "" {
		return
	}
	if begin {
		st.state.begin(callID)
	}
	st.state.write(callID, text)
}

// Reset drops decoder state when the model call is retried.
func (st *sayStreamer) Reset() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.calls = map[int]*sayStreamCall{}
	st.toolWork = false
}
