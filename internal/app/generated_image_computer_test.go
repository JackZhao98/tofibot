package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/provider"
)

func genImageComputerFixture(t *testing.T) (*Server, string, Conversation, Run, Bot, *[]string) {
	t.Helper()
	s, root, statuses := newGuestInputServer(t)
	bot, _ := s.store.CreateBot("illustrator", "", "model")
	conv, _ := s.store.GetConversation(bot.DMConversationID)
	_, run, _ := sendWithFiles(t, s, conv.ID, bot.ID, "draw", "gi-1", nil, nil)
	if ok, err := s.store.SetRunStatus(run.ID, "running", ""); err != nil || !ok {
		t.Fatalf("running=%v %v", ok, err)
	}
	return s, root, conv, run, bot, statuses
}

func genComputerPath(t *testing.T, out string) string {
	t.Helper()
	var res struct {
		Path string `json:"computer_path"`
	}
	if json.Unmarshal([]byte(out), &res) != nil || res.Path == "" {
		t.Fatalf("result lacks computer_path: %s", out)
	}
	return res.Path
}

func TestGeneratedImageReachesTheComputerOnNextSyncWithoutBootingIt(t *testing.T) {
	s, _, conv, run, _, statuses := genImageComputerFixture(t)
	data := generatedPNG(t)
	tool := s.imageGenerationTool(conv, run, func(context.Context, provider.ImageRequest) ([]byte, error) { return data, nil })
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"panels","name":"comic"}`))
	if err != nil {
		t.Fatal(err)
	}
	p := genComputerPath(t, out)
	if p != guestUploadsRoot+"/"+conv.ID+"/generated/comic.png" {
		t.Fatalf("path=%q", p)
	}
	if _, err := os.Stat(p); err == nil || len(*statuses) != 0 {
		t.Fatalf("generate_image touched the computer: %v transfers=%v", err, *statuses)
	}
	files, _ := toolNamed(s.microVMTools(run), "computer_files")
	if _, err := files.Execute(context.Background(), json.RawMessage(`{"action":"files.list","path":"."}`)); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(p); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("not placed on next sync: %v", err)
	}
}

func TestGeneratedImagePlacedAtOnceWhenComputerAlreadyInUse(t *testing.T) {
	s, _, conv, run, _, _ := genImageComputerFixture(t)
	data := generatedPNG(t)
	files, _ := toolNamed(s.microVMTools(run), "computer_files")
	if _, err := files.Execute(context.Background(), json.RawMessage(`{"action":"files.list","path":"."}`)); err != nil {
		t.Fatal(err)
	}
	tool := s.imageGenerationTool(conv, run, func(context.Context, provider.ImageRequest) ([]byte, error) { return data, nil })
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"panels","name":"comic"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(genComputerPath(t, out)); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("not placed at once: %v", err)
	}
}

func TestGeneratedImageCopyFailureNeverFailsTheTool(t *testing.T) {
	s, _, conv, run, _, _ := genImageComputerFixture(t)
	files, _ := toolNamed(s.microVMTools(run), "computer_files")
	if _, err := files.Execute(context.Background(), json.RawMessage(`{"action":"files.list","path":"."}`)); err != nil {
		t.Fatal(err)
	}
	broken, err := computer.New(computer.Config{Socket: "/tmp/synthetic-missing.sock", Client: &http.Client{Transport: reviewTransport(func(*http.Request) (*http.Response, error) {
		return nil, os.ErrClosed
	})}})
	if err != nil {
		t.Fatal(err)
	}
	s.microVM = broken
	tool := s.imageGenerationTool(conv, run, func(context.Context, provider.ImageRequest) ([]byte, error) { return generatedPNG(t), nil })
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"panels","name":"comic"}`))
	if err != nil {
		t.Fatalf("copy failure failed the tool: %v", err)
	}
	if !strings.Contains(out, "attachment_id") {
		t.Fatalf("out=%s", out)
	}
}

func TestEditedAndCachedGeneratedImagesLandOnTheComputer(t *testing.T) {
	s, _, conv, run, _, statuses := genImageComputerFixture(t)
	a, b := generatedPNG(t), append(generatedPNG(t)[:len(generatedPNG(t)):len(generatedPNG(t))], 0)
	n := 0
	tool := s.imageGenerationTool(conv, run, func(_ context.Context, in provider.ImageRequest) ([]byte, error) {
		n++
		if len(in.References) > 0 {
			return b, nil
		}
		return a, nil
	})
	first, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"base","name":"base"}`))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		ID string `json:"attachment_id"`
	}
	_ = json.Unmarshal([]byte(first), &f)
	edited, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"edit","reference_attachment_id":"`+f.ID+`","name":"base"}`))
	if err != nil {
		t.Fatal(err)
	}
	cached, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"edit","reference_attachment_id":"`+f.ID+`","name":"base"}`))
	if err != nil || n != 2 {
		t.Fatalf("cache n=%d err=%v", n, err)
	}
	p1, p2, p3 := genComputerPath(t, first), genComputerPath(t, edited), genComputerPath(t, cached)
	if p2 != p3 || p1 == p2 {
		t.Fatalf("paths %q %q %q", p1, p2, p3)
	}
	files, _ := toolNamed(s.microVMTools(run), "computer_files")
	if _, err := files.Execute(context.Background(), json.RawMessage(`{"action":"files.list","path":"."}`)); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(p1); err != nil || !bytes.Equal(got, a) {
		t.Fatalf("source: %v", err)
	}
	if got, err := os.ReadFile(p2); err != nil || !bytes.Equal(got, b) {
		t.Fatalf("edit: %v", err)
	}
	written := 0
	for _, st := range *statuses {
		if st == "written" {
			written++
		}
	}
	if written != 2 {
		t.Fatalf("statuses=%v", *statuses)
	}
}

func TestPublishFileReplacesGeneratedImageInFinalReply(t *testing.T) {
	s, root, conv, run, bot, _ := genImageComputerFixture(t)
	tool := s.imageGenerationTool(conv, run, func(context.Context, provider.ImageRequest) ([]byte, error) { return generatedPNG(t), nil })
	base, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"panels","name":"comic"}`))
	if err != nil {
		t.Fatal(err)
	}
	keep, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"other","name":"other"}`))
	if err != nil {
		t.Fatal(err)
	}
	var b, k struct {
		ID string `json:"attachment_id"`
	}
	_ = json.Unmarshal([]byte(base), &b)
	_ = json.Unmarshal([]byte(keep), &k)
	dir := filepath.Join(root, "bots", bot.ID)
	_ = os.MkdirAll(dir, 0o770)
	if err := os.WriteFile(filepath.Join(dir, "final.png"), generatedPNG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	pub := s.publishAttachmentTools(conv, run)[0]
	if _, err := pub.Execute(context.Background(), json.RawMessage(`{"path":"final.png","replaces_attachment_id":"nope"}`)); err == nil {
		t.Fatal("unknown replaces_attachment_id accepted")
	}
	if names := s.store.runAttachmentNames(run.ID); len(names) != 2 {
		t.Fatalf("failed call changed the reply: %v", names)
	}
	out, err := pub.Execute(context.Background(), json.RawMessage(`{"path":"final.png","name":"final.png","replaces_attachment_id":"`+b.ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "final.png") || strings.Contains(out, "comic.png") {
		t.Fatalf("result=%s", out)
	}
	names := s.store.runAttachmentNames(run.ID)
	if len(names) != 2 || names[0] != "other.png" || names[1] != "final.png" {
		t.Fatalf("final reply=%v", names)
	}
}

func TestPersonUploadWithGeneratedImageNameIsNotShadowed(t *testing.T) {
	s, _, conv, run, bot, _ := genImageComputerFixture(t)
	data := generatedPNG(t)
	tool := s.imageGenerationTool(conv, run, func(context.Context, provider.ImageRequest) ([]byte, error) { return data, nil })
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"panels","name":"comic"}`))
	if err != nil {
		t.Fatal(err)
	}
	gp := genComputerPath(t, out)
	files, _ := toolNamed(s.microVMTools(run), "computer_files")
	if _, err := files.Execute(context.Background(), json.RawMessage(`{"action":"files.list","path":"."}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.store.FinishRun(run.ID, conv.ID, bot.ID, "done"); err != nil {
		t.Fatal(err)
	}
	mine := []byte("the person's own file")
	_, run2, _ := sendWithFiles(t, s, conv.ID, bot.ID, "here", "gi-2", map[string][]byte{"comic.png": mine}, []string{"comic.png"})
	if ok, err := s.store.SetRunStatus(run2.ID, "running", ""); err != nil || !ok {
		t.Fatalf("running=%v %v", ok, err)
	}
	files2, _ := toolNamed(s.microVMTools(run2), "computer_files")
	if _, err := files2.Execute(context.Background(), json.RawMessage(`{"action":"files.list","path":"."}`)); err != nil {
		t.Fatal(err)
	}
	plain := guestUploadsRoot + "/" + conv.ID + "/comic.png"
	if got, err := os.ReadFile(plain); err != nil || !bytes.Equal(got, mine) {
		t.Fatalf("person's file shadowed: %q %v", got, err)
	}
	if got, err := os.ReadFile(gp); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("generated image moved: %v", err)
	}
}

// A file on the computer serves as a reference by its absolute path.
func TestImageGenerationTakesAComputerFileAsReference(t *testing.T) {
	s, _, conv, run, _, _ := genImageComputerFixture(t)
	data := generatedPNG(t)
	var got provider.ImageRequest
	tool := s.imageGenerationTool(conv, run, func(_ context.Context, in provider.ImageRequest) ([]byte, error) { got = in; return data, nil })
	base, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"panels","name":"comic"}`))
	if err != nil {
		t.Fatal(err)
	}
	files, _ := toolNamed(s.microVMTools(run), "computer_files")
	if _, err := files.Execute(context.Background(), json.RawMessage(`{"action":"files.list","path":"."}`)); err != nil {
		t.Fatal(err)
	}
	path := genComputerPath(t, base)
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"same style, new scene","references":["`+path+`"]}`)); err != nil {
		t.Fatal(err)
	}
	if got.Edit || len(got.References) != 1 || got.References[0] != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(data) {
		t.Fatalf("request edit=%v refs=%d", got.Edit, len(got.References))
	}
}
