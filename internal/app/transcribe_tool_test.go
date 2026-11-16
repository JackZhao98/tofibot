package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func transcribeFixture(t *testing.T) (*Server, Conversation, Run, *int) {
	t.Helper()
	s, _, _ := newGuestInputServer(t)
	hits := new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		if r.Header.Get("Authorization") != "Bearer synthetic-key" {
			t.Errorf("authorization=%q", r.Header.Get("Authorization"))
		}
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			t.Errorf("multipart: %v", err)
			return
		}
		f, h, err := r.FormFile("file")
		if err != nil {
			t.Errorf("file: %v", err)
			return
		}
		b, _ := io.ReadAll(f)
		if h.Filename != "talk.mp3" || string(b) != "synthetic audio" || r.FormValue("model") == "" || r.FormValue("language") != "en" {
			t.Errorf("name=%q body=%q model=%q language=%q", h.Filename, b, r.FormValue("model"), r.FormValue("language"))
		}
		_, _ = w.Write([]byte(`{"text":"hello"}`))
	}))
	t.Cleanup(srv.Close)
	s.transcriptionAPIKey = "synthetic-key"
	s.transcriptionURL = srv.URL
	bot, _ := s.store.CreateBot("synthetic", "", "model")
	conv, _ := s.store.GetConversation(bot.DMConversationID)
	_, run, _ := sendWithFiles(t, s, conv.ID, bot.ID, "transcribe", "tr-1", nil, nil)
	return s, conv, run, hits
}

func TestTranscribeAudioRegisteredOnlyWithCredentialSource(t *testing.T) {
	s, conv, run, _ := transcribeFixture(t)
	if len(s.transcribeAudioTools(conv, run)) != 1 {
		t.Fatal("tool missing with a credential")
	}
	s.transcriptionAPIKey = ""
	s.transcriptionURL = "http://127.0.0.1:1"
	if len(s.transcribeAudioTools(conv, run)) != 0 {
		t.Fatal("tool registered without a credential source")
	}
}

func TestTranscribeAudioAttachment(t *testing.T) {
	s, conv, run, hits := transcribeFixture(t)
	tool := s.transcribeAudioTools(conv, run)[0]
	a, err := s.store.AddAttachment(conv.ID, "talk.mp3", "audio/mpeg", strings.NewReader("synthetic audio"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"attachment_id":"`+a.ID+`","language":"en"}`))
	if err != nil {
		t.Fatal(err)
	}
	var res map[string]string
	if json.Unmarshal([]byte(out), &res) != nil || res["text"] != "hello" || res["source"] != "talk.mp3" || res["model"] == "" {
		t.Fatalf("result=%s", out)
	}
	if *hits != 1 {
		t.Fatalf("hits=%d", *hits)
	}
}

func TestTranscribeAudioRefusals(t *testing.T) {
	s, conv, run, hits := transcribeFixture(t)
	tool := s.transcribeAudioTools(conv, run)[0]
	other, _ := s.store.CreateBot("other", "", "model")
	otherConv, _ := s.store.GetConversation(other.DMConversationID)
	foreign, err := s.store.AddAttachment(otherConv.ID, "talk.mp3", "audio/mpeg", strings.NewReader("synthetic audio"))
	if err != nil {
		t.Fatal(err)
	}
	text, err := s.store.AddAttachment(conv.ID, "notes.txt", "text/plain", strings.NewReader("synthetic"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, args, want string }{
		{"both", `{"attachment_id":"x","path":"/workspace/a.mp3"}`, "not both"},
		{"neither", `{}`, "required"},
		{"foreign", `{"attachment_id":"` + foreign.ID + `"}`, "another conversation"},
		{"type", `{"attachment_id":"` + text.ID + `"}`, "mp3, mp4, mpeg, mpga, m4a, wav and webm"},
		{"outside", `{"path":"/etc/a.mp3"}`, "under /workspace"},
		{"language", `{"attachment_id":"` + text.ID + `","language":"english"}`, "ISO 639-1"},
	}
	for _, c := range cases {
		_, err := tool.Execute(context.Background(), json.RawMessage(c.args))
		o, typed := tooloutcome.FromError(err)
		if !typed || o.Status != tooloutcome.Validation || !strings.Contains(o.Message, c.want) {
			t.Errorf("%s: err=%v outcome=%+v", c.name, err, o)
		}
	}
	if *hits != 0 {
		t.Fatalf("a refused call reached the service %d times", *hits)
	}
}

func TestTranscribeAudioServiceFailureHasStableCodeWithoutCredential(t *testing.T) {
	s, conv, run, _ := transcribeFixture(t)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer bad.Close()
	s.transcriptionURL = bad.URL
	tool := s.transcribeAudioTools(conv, run)[0]
	a, _ := s.store.AddAttachment(conv.ID, "talk.mp3", "audio/mpeg", strings.NewReader("synthetic audio"))
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"attachment_id":"`+a.ID+`"}`))
	o, typed := tooloutcome.FromError(err)
	if !typed || o.Code != "transcription_failed" || strings.Contains(o.Message, "synthetic-key") {
		t.Fatalf("err=%v outcome=%+v", err, o)
	}
}

// The store caps an upload below the service limit, so a file over the limit
// can only arrive from the Bot computer; the check is the same for both.
func TestTranscribeAudioOversizedFileIsRefusedWithAWayOut(t *testing.T) {
	err := checkTranscribable("long.wav", maxDictationBytes+1)
	o, typed := tooloutcome.FromError(err)
	if !typed || o.Status != tooloutcome.Validation || !strings.Contains(o.Message, "25 MiB") || !strings.Contains(o.Message, "ffmpeg") {
		t.Fatalf("err=%v outcome=%+v", err, o)
	}
	if err := checkTranscribable("talk.WAV", maxDictationBytes); err != nil {
		t.Fatalf("a file at the limit must pass: %v", err)
	}
}
