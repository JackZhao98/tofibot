package app

// desktop.trace: the model looks at the sequence of its own screen work. The
// action joins captures this run already made (stored by finishCapture) into
// one labelled grid. It takes no new picture and does not touch the Bot
// computer, so it needs no lease. It counts toward the views of the run.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const (
	traceLastDefault = 6
	traceLastMin     = 2

	// traceActionSentence is the model-facing sentence of the tool description.
	traceActionSentence = "desktop.trace joins recent captures of this run into one labelled grid to check the sequence of states; it is too small for reading text. Use it before you report that screen work is done."
)

var traceActionParams = map[string]any{
	"last":         map[string]any{"type": "integer", "minimum": traceLastMin, "maximum": montageModelTilesMax, "description": "desktop.trace: the number of most recent captures, default 6"},
	"capture_seqs": map[string]any{"type": "array", "maxItems": montageModelTilesMax, "items": map[string]any{"type": "integer", "minimum": 1}, "description": "desktop.trace: capture_seq numbers (each capture result carries one) to join, instead of last"},
	"columns":      map[string]any{"type": "integer", "minimum": 2, "maximum": 3, "description": "desktop.trace: grid columns, default 3"},
}

type traceArgs struct {
	Last        int   `json:"last,omitempty"`
	CaptureSeqs []int `json:"capture_seqs,omitempty"`
	Columns     int   `json:"columns,omitempty"`
}

// selectTraceRows picks the captures a desktop.trace call asks for.
func selectTraceRows(rows []RunCapture, in traceArgs) ([]RunCapture, error) {
	if len(in.CaptureSeqs) > 0 {
		if len(in.CaptureSeqs) > montageModelTilesMax {
			return nil, tooloutcome.InvalidArguments(fmt.Sprintf("capture_seqs holds at most %d numbers", montageModelTilesMax))
		}
		bySeq := map[int]RunCapture{}
		for _, row := range rows {
			bySeq[row.Seq] = row
		}
		seen := map[int]bool{}
		var picked []RunCapture
		for _, seq := range in.CaptureSeqs {
			row, ok := bySeq[seq]
			if !ok {
				return nil, tooloutcome.InvalidArguments(fmt.Sprintf("capture_seq %d is not a capture of this run; the run has captures 1 to %d", seq, rows[len(rows)-1].Seq))
			}
			if !seen[seq] {
				seen[seq] = true
				picked = append(picked, row)
			}
		}
		sort.Slice(picked, func(i, j int) bool { return picked[i].Seq < picked[j].Seq })
		return picked, nil
	}
	last := in.Last
	if last == 0 {
		last = traceLastDefault
	}
	if last < traceLastMin || last > montageModelTilesMax {
		return nil, tooloutcome.InvalidArguments(fmt.Sprintf("last must be from %d to %d", traceLastMin, montageModelTilesMax))
	}
	if last > len(rows) {
		last = len(rows)
	}
	return rows[len(rows)-last:], nil
}

// traceForModel answers desktop.trace with a grid the vision path lifts into
// the model's image input.
func (s *Server) traceForModel(ctx context.Context, r Run, raw json.RawMessage) (string, error) {
	var in traceArgs
	if len(raw) > 0 && json.Unmarshal(raw, &in) != nil {
		return "", tooloutcome.InvalidArguments("desktop.trace takes last, capture_seqs and columns")
	}
	if in.Columns != 0 && (in.Columns < 2 || in.Columns > 3) {
		return "", tooloutcome.InvalidArguments("columns must be 2 or 3")
	}
	if !s.autoBool(autoWorkTrace) {
		return "", tooloutcome.New(tooloutcome.Permanent, "work_trace_off", "not_executed", "trace is off: this workspace keeps no captures, so there is nothing to join. Use desktop.capture to look at the screen.", "replan").Err()
	}
	rows, err := s.store.runCaptures(r.ID)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", tooloutcome.New(tooloutcome.Validation, "no_captures", "not_executed", "This run has no capture yet. Use desktop.capture first.", "repair_arguments").Err()
	}
	picked, err := selectTraceRows(rows, in)
	if err != nil {
		return "", err
	}
	res, err := s.buildRunMontage(ctx, rows, picked, montageOptions{Columns: in.Columns})
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(map[string]any{
		"image_url": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(res.JPEG),
		"width":     res.Width, "height": res.Height,
		"tiles": res.Tiles, "captures_in_run": len(rows),
	})
	return string(out), err
}
