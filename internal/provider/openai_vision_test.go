package provider

import "testing"

func TestOpenAIResponsesPreservesUserImagesAsVisualInput(t *testing.T) {
	payload := (&openaiResponses{}).buildPayload(&ChatRequest{Model: "gpt-4o", Messages: []Message{{
		Role:      "user",
		Content:   "Describe the uploaded image",
		ImageURLs: []string{"data:image/png;base64,iVBORw0KGgo="},
	}}}, false)
	input, ok := payload["input"].([]interface{})
	if !ok {
		t.Fatalf("expected Responses API input array, got %#v", payload["input"])
	}
	if len(input) != 1 {
		t.Fatalf("expected one user input, got %d", len(input))
	}
	message, ok := input[0].(map[string]interface{})
	if !ok || message["role"] != "user" {
		t.Fatalf("expected user message, got %#v", input[0])
	}
	content, ok := message["content"].([]map[string]interface{})
	if !ok || len(content) != 2 {
		t.Fatalf("expected text and image blocks, got %#v", message["content"])
	}
	if content[0]["type"] != "input_text" || content[0]["text"] != "Describe the uploaded image" {
		t.Fatalf("unexpected text block: %#v", content[0])
	}
	if content[1]["type"] != "input_image" || content[1]["image_url"] != "data:image/png;base64,iVBORw0KGgo=" || content[1]["detail"] != "auto" {
		t.Fatalf("unexpected image block: %#v", content[1])
	}
}

func TestOpenAIResponsesUsesRequestedReasoningEffort(t *testing.T) {
	payload := (&openaiResponses{}).buildPayload(&ChatRequest{Model: "gpt-5.6-luna", ReasoningEffort: "high"}, false)
	reasoning, ok := payload["reasoning"].(map[string]interface{})
	if !ok || reasoning["effort"] != "high" {
		t.Fatalf("reasoning payload=%#v", payload["reasoning"])
	}
	without := (&openaiResponses{}).buildPayload(&ChatRequest{Model: "gpt-5.6-luna", ReasoningEffort: "none"}, false)
	if _, ok := without["reasoning"]; ok {
		t.Fatalf("none should omit reasoning: %#v", without)
	}
}

func TestOpenAIResponsesAttachesToolImagesAsVisualInput(t *testing.T) {
	input := (&openaiResponses{}).convertMessages([]Message{{Role: "tool", Content: "--- Images ---\n- https://example.com/chart.png", ImageURLs: []string{"https://example.com/chart.png"}, ToolCallID: "call_123"}}, false)
	if len(input) != 2 {
		t.Fatalf("expected tool output and visual message, got %d inputs", len(input))
	}
	visual, ok := input[1].(map[string]interface{})
	if !ok || visual["role"] != "user" {
		t.Fatalf("expected visual user message, got %#v", input[1])
	}
	content := visual["content"].([]map[string]interface{})
	if content[1]["type"] != "input_image" || content[1]["image_url"] != "https://example.com/chart.png" {
		t.Fatalf("expected input_image block, got %#v", content[1])
	}
}
