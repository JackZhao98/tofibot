package provider

import (
	"errors"
	"strings"
	"testing"
)

func TestCompletionsParseStreamTruncated(t *testing.T) {
	o := &openaiChatCompletions{}
	cases := map[string]string{
		"partial content":   "data: {\"choices\":[{\"delta\":{\"content\":\"half an ans\"}}]}\n\n",
		"partial tool call": "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"x\",\"arguments\":\"{\\\"a\\\":\"}}]}}]}\n\n",
	}
	for name, stream := range cases {
		if _, err := o.parseStream(strings.NewReader(stream), nil); !errors.Is(err, ErrStreamIncomplete) {
			t.Fatalf("%s: err = %v, want ErrStreamIncomplete", name, err)
		}
	}
}

func TestCompletionsParseStreamTerminators(t *testing.T) {
	o := &openaiChatCompletions{}
	for name, stream := range map[string]string{
		"done":          "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n",
		"finish reason": "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n",
	} {
		resp, err := o.parseStream(strings.NewReader(stream), nil)
		if err != nil || resp.Content != "ok" {
			t.Fatalf("%s: resp=%v err=%v", name, resp, err)
		}
	}
}
