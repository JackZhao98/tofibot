package app

import (
	"database/sql"
	"strings"
	"unicode"
)

// A final answer that restates a visible message the same run already posted
// ("both schedules are set" twice) reads as a stutter. Such an answer is not
// published; the run still finishes.
// Calibrated on real replies: a restatement scored 0.57 with length ratio 0.89;
// an answer that expanded its earlier line scored 0.49 with ratio 0.32.
const (
	repeatedReplySimilarity  = 0.5
	repeatedReplyLengthRatio = 0.6
)

// repeatedSegmentTx returns the run's visible segment that the final answer
// restates, if any. That segment then stands as the run's result message.
func repeatedSegmentTx(tx *sql.Tx, runID, content string) (Message, bool, error) {
	rows, err := tx.Query(`SELECT id,content FROM messages WHERE run_id=? AND role='assistant' AND kind='segment' ORDER BY seq DESC`, runID)
	if err != nil {
		return Message{}, false, err
	}
	var match string
	for rows.Next() {
		var id, prior string
		if err := rows.Scan(&id, &prior); err != nil {
			rows.Close()
			return Message{}, false, err
		}
		if match == "" && repliesNearlyEqual(prior, content) {
			match = id
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil || match == "" {
		return Message{}, false, err
	}
	m, err := scanMsg(tx.QueryRow(`SELECT id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,notice_data,created_at FROM messages WHERE id=?`, match))
	return m, err == nil, err
}

// repliesNearlyEqual compares two replies by character bigrams (Dice
// coefficient) after dropping spaces and punctuation, so a reworded restatement
// of the same reply counts while an answer that adds substance (much longer) does not.
func repliesNearlyEqual(a, b string) bool {
	x, y := replyBigrams(a), replyBigrams(b)
	if len(x) == 0 || len(y) == 0 {
		return false
	}
	shared, nx, ny := 0, 0, 0
	for gram, n := range x {
		if m := y[gram]; m > 0 {
			shared += min(n, m)
		}
		nx += n
	}
	for _, n := range y {
		ny += n
	}
	if float64(min(nx, ny))/float64(max(nx, ny)) < repeatedReplyLengthRatio {
		return false
	}
	return float64(2*shared)/float64(nx+ny) >= repeatedReplySimilarity
}

func replyBigrams(s string) map[string]int {
	var runes []rune
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			runes = append(runes, r)
		}
	}
	out := map[string]int{}
	for i := 0; i+1 < len(runes); i++ {
		out[string(runes[i:i+2])]++
	}
	return out
}
