package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func imageStream(data []byte) string {
	item, _ := json.Marshal(map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "image_generation_call", "status": "completed", "result": base64.StdEncoding.EncodeToString(data)}})
	return "data: " + string(item) + "\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\ndata: [DONE]\n\n"
}

func TestGenerateImageCodexTransportAndReference(t *testing.T) {
	for _, reference := range []string{"", "data:image/png;base64,fixture"} {
		t.Run(fmt.Sprint(reference != ""), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("ChatGPT-Account-Id") != "account" {
					t.Error("incorrect auth headers")
				}
				var p map[string]any
				if json.NewDecoder(r.Body).Decode(&p) != nil {
					t.Fatal("invalid payload")
				}
				if p["store"] != false || p["stream"] != true || p["model"] != "gpt-5.6-luna" {
					t.Errorf("wrong transport payload: %v", p)
				}
				tool := p["tools"].([]any)[0].(map[string]any)
				want := "generate"
				if reference != "" {
					want = "edit"
				}
				if tool["type"] != "image_generation" || tool["action"] != want {
					t.Error("wrong hosted tool")
				}
				content := p["input"].([]any)[0].(map[string]any)["content"].([]any)
				if reference != "" && (len(content) != 2 || content[1].(map[string]any)["image_url"] != reference) {
					t.Error("missing reference")
				}
				fmt.Fprint(w, imageStream([]byte("image data")))
			}))
			defer server.Close()
			data, err := generateCodexImage(context.Background(), server.Client(), server.URL, "token\x00account", ImageRequest{Prompt: "Draw a circle", Reference: reference})
			if err != nil || string(data) != "image data" {
				t.Fatalf("result %q %v", data, err)
			}
		})
	}
}

func TestImageGenerationRejectsPartialAndFailedStreams(t *testing.T) {
	complete := imageStream([]byte("image data"))
	for name, body := range map[string]string{
		"truncated": strings.Split(complete, "data: {\"type\":\"response.completed\"")[0],
		"failed":    complete + "data: {\"type\":\"response.failed\"}\n", // DONE ends stream; tested separately below
		"no image":  "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n",
		"malformed": "data: broken\n",
	} {
		if name == "failed" {
			body = strings.ReplaceAll(complete, "data: [DONE]\n\n", "") + "data: {\"type\":\"response.failed\"}\n"
		}
		t.Run(name, func(t *testing.T) {
			if _, err := readGeneratedImage(strings.NewReader(body)); err == nil {
				t.Fatal("accepted incomplete image")
			}
		})
	}
}

func TestImageGenerationDoesNotExposeProviderBodyOrRetry(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(429)
		fmt.Fprint(w, "secret upstream body")
	}))
	defer server.Close()
	_, err := generateCodexImage(context.Background(), server.Client(), server.URL, "token", ImageRequest{Prompt: "test"})
	if err == nil || strings.Contains(err.Error(), "secret") || calls != 1 {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = generateCodexImage(ctx, server.Client(), server.URL, "token", ImageRequest{Prompt: "test"})
	if err != context.Canceled || calls != 1 {
		t.Fatalf("cancel=%v calls=%d", err, calls)
	}
}
