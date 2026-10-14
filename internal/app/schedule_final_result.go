package app

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// scheduledFinalWithResult makes a scheduled run's published answer carry the
// result it recorded with complete_scheduled_task. Models sometimes treat the
// recorded result as already delivered and answer with a reference to it
// ("roughly as above") or a correction only; the conversation would then never
// see the result. When the answer does not carry the result, the result is
// placed at the start of the answer and the model's text follows it. Progress
// notes before the answer stay where they are.
func scheduledFinalWithResult(final, recordedArgs string) string {
	var in struct {
		Content string `json:"content"`
	}
	if json.Unmarshal([]byte(recordedArgs), &in) != nil {
		return final
	}
	result := strings.TrimSpace(in.Content)
	if result == "" {
		return final
	}
	_, answer := finalSplit(final)
	answer = strings.TrimSpace(answer)
	if answerCarriesResult(answer, result) {
		return final
	}
	if answer == "" {
		if strings.TrimSpace(final) == "" {
			return result
		}
		return strings.TrimRight(final, " \n") + "\n\n" + result
	}
	at := strings.LastIndex(final, answer)
	if at < 0 {
		return result + "\n\n" + final
	}
	return final[:at] + result + "\n\n" + final[at:]
}

// answerCarriesResult accepts an answer that contains the result or restates
// it at comparable length (a rewording is fine; a pointer to it is not).
func answerCarriesResult(answer, result string) bool {
	if answer == "" {
		return false
	}
	if strings.Contains(compactSpace(answer), compactSpace(result)) {
		return true
	}
	return utf8.RuneCountInString(answer)*2 >= utf8.RuneCountInString(result)
}

func compactSpace(s string) string { return strings.Join(strings.Fields(s), " ") }
