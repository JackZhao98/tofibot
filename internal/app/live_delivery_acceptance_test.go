package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func deliveryAcceptanceServer(t *testing.T, socket string) (*Server, Config) {
	t.Helper()
	source := os.Getenv("TOFI_ACCEPTANCE_CODEX_CREDENTIAL_FILE")
	if source == "" {
		t.Skip("opt-in synthetic delivery acceptance with owner-authorized access-only snapshot")
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	var credential struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
		ExpiresAt   int64  `json:"expires_at"`
	}
	if json.Unmarshal(data, &credential) != nil || credential.AccessToken == "" || credential.ExpiresAt < time.Now().Add(10*time.Minute).UnixMilli() {
		t.Fatal("fresh access snapshot unavailable")
	}
	cfg := Config{DataDir: t.TempDir(), Environment: "acceptance", ComputerSocket: socket, DefaultModel: "codex-gpt-5.6-luna"}
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.codex.SaveAccessOnlyCredential(credential.AccessToken, credential.AccountID, credential.ExpiresAt); err != nil {
		s.Close()
		t.Fatal(err)
	}
	return s, cfg
}

func waitDeliveryRun(t *testing.T, s *Server, id string, timeout time.Duration) Run {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r, err := s.store.GetRun(id)
		if err != nil {
			t.Fatal(err)
		}
		if r.Status != "queued" && r.Status != "running" {
			if r.Status != "done" {
				t.Fatalf("run %s: %s", r.Status, r.Error)
			}
			return r
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("delivery run timed out")
	return Run{}
}

func TestLiveCodexGenerateAndEditAttachment(t *testing.T) {
	s, _ := deliveryAcceptanceServer(t, "")
	defer s.Close()
	b, err := s.store.CreateBot("Synthetic image acceptance", "", "")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, r, _, err := s.store.AddUserRun(c.ID, b.ID, "synthetic image test", "image-acceptance")
	if err != nil {
		t.Fatal(err)
	}
	s.store.SetRunStatus(r.ID, "running", "")
	tool := s.imageGenerationTools(c, r)[0]
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"Generate one minimal flat illustration: a single orange circle on an ivory background, no text.","name":"circle.png"}`))
	if err != nil {
		t.Fatal(err)
	}
	var first struct {
		ID string `json:"attachment_id"`
	}
	json.Unmarshal([]byte(out), &first)
	edit, _ := json.Marshal(map[string]string{"prompt": "Edit the supplied image: change the orange circle to blue, keep the ivory background and simple composition.", "reference_attachment_id": first.ID, "name": "edited-circle.png"})
	out, err = tool.Execute(context.Background(), edit)
	if err != nil {
		t.Fatal(err)
	}
	var second struct {
		ID string `json:"attachment_id"`
	}
	json.Unmarshal([]byte(out), &second)
	if first.ID == "" || first.ID == second.ID {
		t.Fatal("missing separate edit result")
	}
	m, ok, err := s.store.FinishRun(r.ID, c.ID, b.ID, "Generated image and edited image.")
	if err != nil || !ok || len(m.Attachments) != 2 {
		t.Fatalf("image delivery: %v %v %#v", ok, err, m.Attachments)
	}
	for _, a := range m.Attachments {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/attachments/"+a.ID, nil))
		if w.Code != 200 || !bytes.HasPrefix(w.Body.Bytes(), []byte("\x89PNG\r\n\x1a\n")) {
			t.Fatal("image download failed")
		}
		t.Logf("image_attachment name=%s bytes=%d persistent=true sender=%s", a.Name, a.Size, m.SenderBotID)
	}
}

func TestLiveScheduledReportAndImageAfterRestart(t *testing.T) {
	socket := os.Getenv("TOFI_ACCEPTANCE_DELIVERY_SOCKET")
	if socket == "" {
		t.Skip("opt-in acceptance-a guest and real model")
	}
	if socket != "/run/tofi-computer/acceptance-a/control.sock" {
		t.Fatal("only independent acceptance-a allowed")
	}
	s, cfg := deliveryAcceptanceServer(t, socket)
	defer func() {
		if s != nil {
			s.Close()
		}
	}()
	info, err := s.microVMInfo(context.Background())
	if err != nil || info.ID != "acceptance-a" || info.State != "ready" {
		t.Fatalf("acceptance VM unavailable %v %v", info, err)
	}
	s.store.putUserTimezone("America/Los_Angeles", false)
	b, err := s.store.CreateBot("Synthetic scheduled delivery", "Follow user requests using actual tools; never claim a task is scheduled or a file delivered without a successful tool call. This is an isolated synthetic acceptance.", "codex-gpt-5.6-luna")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.store.GetConversation(b.DMConversationID)
	defer func() {
		if s != nil {
			args, _ := json.Marshal(map[string]any{"command": "rm -f report.txt", "timeout_sec": 10})
			s.microVMAction(context.Background(), Run{ID: "delivery-cleanup", BotID: b.ID, ConversationID: c.ID}, "shell.exec", args)
			s.releaseComputerOwner(b.ID, "delivery-cleanup")
		}
	}()
	loc, _ := time.LoadLocation("America/Los_Angeles")
	target := time.Now().Add(75 * time.Second).Truncate(time.Second)
	brief := "Create report.txt in your VM workspace containing exactly DELIVERY_REPORT_OK and publish it to this conversation using publish_file. Also generate one small image of an orange circle on ivory using generate_image. Deliver both attachments and a short completion message. Do not create further schedules."
	prompt := fmt.Sprintf("Schedule this task once for %s in my configured local timezone: %s. Do not execute the work now; create the schedule and confirm the exact time.", target.In(loc).Format("2006-01-02 15:04:05"), brief)
	_, r, _, err := s.store.AddUserRun(c.ID, b.ID, prompt, "schedule-delivery")
	if err != nil {
		t.Fatal(err)
	}
	s.enqueue(c, r)
	waitDeliveryRun(t, s, r.ID, 50*time.Second)
	schedules, err := s.store.ListSchedules(c.ID)
	if err != nil || len(schedules) != 1 {
		t.Fatalf("expected one schedule %v %#v", err, schedules)
	}
	planned, _ := time.Parse(time.RFC3339Nano, schedules[0].NextAtUTC)
	if !planned.Equal(target) || schedules[0].Timezone != "America/Los_Angeles" {
		t.Fatalf("wrong local schedule %#v", schedules[0])
	}
	if !time.Now().Before(target) {
		t.Fatal("schedule executed before restart test")
	}
	s.Close()
	s = nil
	s, err = NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(6 * time.Minute)
	var scheduledRun string
	for time.Now().Before(deadline) {
		_ = s.store.db.QueryRow(`SELECT run_id FROM schedule_occurrences WHERE schedule_id=?`, schedules[0].ID).Scan(&scheduledRun)
		if scheduledRun != "" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if scheduledRun == "" {
		t.Fatal("no scheduled execution after restart")
	}
	done := waitDeliveryRun(t, s, scheduledRun, 5*time.Minute)
	started, _ := time.Parse(time.RFC3339Nano, done.CreatedAt)
	if started.Before(target) {
		t.Fatal("ran before scheduled time")
	}
	var count int
	s.store.db.QueryRow(`SELECT COUNT(*) FROM schedule_occurrences WHERE schedule_id=?`, schedules[0].ID).Scan(&count)
	if count != 1 {
		t.Fatal("duplicate occurrence")
	}
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/api/conversations/"+c.ID+"/messages", nil))
	var transcript struct {
		Messages []Message `json:"messages"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &transcript) != nil {
		t.Fatal("could not reload messages through chat API")
	}
	msgs := transcript.Messages
	var files []Attachment
	for _, m := range msgs {
		if m.RunID == scheduledRun && m.Role == "assistant" {
			if m.SenderBotID != b.ID {
				t.Fatal("wrong sender")
			}
			files = append(files, m.Attachments...)
		}
	}
	var pngOK, reportOK bool
	for _, a := range files {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/attachments/"+a.ID, nil))
		if w.Code != 200 {
			t.Fatalf("download %s HTTP %d", a.Name, w.Code)
		}
		if a.MIME == "image/png" {
			pngOK = bytes.HasPrefix(w.Body.Bytes(), []byte("\x89PNG"))
		}
		if strings.TrimSpace(w.Body.String()) == "DELIVERY_REPORT_OK" {
			reportOK = true
		}
	}
	if !pngOK || !reportOK {
		t.Fatalf("missing final deliverables image=%v report=%v attachments=%#v", pngOK, reportOK, files)
	}
	t.Logf("real_schedule_delivery timezone=%s target=%s started=%s restart_survived=true occurrence_count=%d attachments=%d", schedules[0].Timezone, target.Format(time.RFC3339), done.CreatedAt, count, len(files))
	// Optional synthetic-only snapshot for browser acceptance. Deliberately
	// exclude the access-only credential, vault and any non-attachment files.
	if export := os.Getenv("TOFI_ACCEPTANCE_DELIVERY_EXPORT"); export != "" {
		if !strings.HasPrefix(filepath.Clean(export), "/tmp/tofi-delivery-browser-") {
			t.Fatal("browser snapshot must be a new isolated /tmp/tofi-delivery-browser-* directory")
		}
		if err := os.Mkdir(export, 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := s.store.db.Exec(`VACUUM INTO ?`, filepath.Join(export, "tofi.db")); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(export, "attachments"), 0700); err != nil {
			t.Fatal(err)
		}
		for _, a := range files {
			_, source, err := s.store.Attachment(a.ID)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(export, "attachments", filepath.Base(source)), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
		manifest, _ := json.Marshal(map[string]any{"conversation_id": c.ID, "bot_id": b.ID, "schedule_id": schedules[0].ID, "run_id": scheduledRun, "attachments": files})
		if err := os.WriteFile(filepath.Join(export, "acceptance-result.json"), manifest, 0600); err != nil {
			t.Fatal(err)
		}
		t.Log("synthetic browser snapshot: " + export)
	}
}
