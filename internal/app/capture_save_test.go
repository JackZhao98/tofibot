package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/computer"
)

type savedOut struct {
	captureOut
	SavedTo     string `json:"saved_to"`
	SavedWidth  int    `json:"saved_width"`
	SavedHeight int    `json:"saved_height"`
	FullPage    bool   `json:"full_page"`
	PageHeight  int    `json:"page_height"`
	Truncated   bool   `json:"truncated"`
}

func (c *captureComputer) named(name string) []computer.Action {
	c.cmu.Lock()
	defer c.cmu.Unlock()
	var out []computer.Action
	for _, a := range c.actions {
		if a.Name == name {
			out = append(out, a)
		}
	}
	return out
}

// importedBytes joins the chunks of every files.import_chunk the computer got.
func importedBytes(t *testing.T, c *captureComputer) ([]byte, string) {
	t.Helper()
	var buf []byte
	var dst string
	for _, a := range c.named("files.import_chunk") {
		var in struct {
			Path string `json:"path"`
			Data string `json:"data_base64"`
		}
		if err := json.Unmarshal(a.Args, &in); err != nil {
			t.Fatal(err)
		}
		raw, _ := base64.StdEncoding.DecodeString(in.Data)
		buf, dst = append(buf, raw...), in.Path
	}
	return buf, dst
}

func shellCommands(c *captureComputer) []string {
	var out []string
	for _, a := range c.named("shell.exec") {
		var in struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(a.Args, &in)
		out = append(out, in.Command)
	}
	return out
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil || toolCode(err) != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestDesktopCaptureSaveToWritesTheFullSizePicture(t *testing.T) {
	s, c, run := captureServer(t)
	out := runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture","detail":"overview","save_to":"/workspace/bots/b1/shots/screen.png"}`)
	var res savedOut
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.SavedTo != "/workspace/bots/b1/shots/screen.png" || res.SavedWidth != 1280 || res.SavedHeight != 800 {
		t.Fatalf("%+v", res)
	}
	// The model still sees the small picture.
	if res.Width != 640 || res.Detail != "overview" {
		t.Fatalf("model picture %+v", res.captureOut)
	}
	data, tmp := importedBytes(t, c)
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width != 1280 || cfg.Height != 800 {
		t.Fatalf("imported %v %v", cfg, err)
	}
	if !strings.HasPrefix(tmp, guestUploadsRoot+"/") {
		t.Fatalf("temp %s is outside the uploads folder", tmp)
	}
	cmds := shellCommands(c)
	if len(cmds) != 1 || !strings.Contains(cmds[0], "'/workspace/bots/b1/shots/screen.png'") || !strings.Contains(cmds[0], "'"+tmp+"'") || !strings.Contains(cmds[0], "realpath") || !strings.Contains(cmds[0], "mkdir -p") {
		t.Fatalf("move command %v", cmds)
	}
	// The guest is asked for the same capture; save_to never reaches it.
	for _, a := range c.named("desktop.capture") {
		if string(a.Args) != "{}" {
			t.Fatalf("guest args %s", a.Args)
		}
	}
}

func TestBrowserSnapshotSaveToWritesAndKeepsTheResultFields(t *testing.T) {
	s, c, run := captureServer(t)
	c.snapshotURL = "https://shop.example/cart"
	out := runTool(t, s, run, "computer_browser", `{"action":"browser.snapshot","save_to":"/workspace/home/uploads/page.png"}`)
	var res savedOut
	_ = json.Unmarshal([]byte(out), &res)
	if res.SavedTo != "/workspace/home/uploads/page.png" || res.URL != "https://shop.example/cart" || res.SavedWidth != 1280 {
		t.Fatalf("%+v", res)
	}
	if data, _ := importedBytes(t, c); len(data) == 0 {
		t.Fatal("nothing was imported")
	}
}

func TestSaveToRefusals(t *testing.T) {
	s, c, run := captureServer(t)
	for _, path := range []string{"relative/a.png", "/etc/a.png", "/workspace/a.jpg", "/workspace/../etc/a.png", "/workspace/", "/workspacex/a.png", "/workspace/a/./b.png"} {
		_, err := execTool(t, s, run, "computer_desktop", `{"action":"desktop.capture","save_to":"`+path+`"}`)
		wantCode(t, err, "save_to_invalid")
	}
	_, err := execTool(t, s, run, "computer_desktop", `{"action":"desktop.click","x":1,"y":1,"save_to":"/workspace/a.png"}`)
	wantCode(t, err, "save_to_invalid")
	_, err = execTool(t, s, run, "computer_browser", `{"action":"browser.snapshot","full_page":true}`)
	wantCode(t, err, "full_page_needs_save_to")
	if len(c.named("desktop.capture"))+len(c.named("browser.snapshot")) != 0 || len(c.named("files.import_chunk")) != 0 {
		t.Fatal("a refused call reached the computer")
	}
}

func TestSaveToWithheldWhenASecretWindowIsOpen(t *testing.T) {
	s, c, run := captureServer(t)
	s.store.openSecretWindow(run.ID)
	for _, args := range []string{`{"action":"desktop.capture","save_to":"/workspace/a.png"}`, `{"action":"browser.snapshot","save_to":"/workspace/a.png"}`, `{"action":"browser.snapshot","save_to":"/workspace/a.png","full_page":true}`} {
		tool := "computer_desktop"
		if strings.Contains(args, "browser") {
			tool = "computer_browser"
		}
		c.shell = func(cmd string) string {
			if strings.Contains(cmd, "captureBeyondViewport") {
				return `{"saved_width":1280,"saved_height":3000,"page_width":1280,"page_height":3000,"truncated":false,"url":"https://news.example/"}`
			}
			return ""
		}
		_, err := execTool(t, s, run, tool, args)
		wantCode(t, err, "save_to_withheld")
	}
	if len(c.named("files.import_chunk")) != 0 {
		t.Fatal("a withheld capture was written")
	}
	for _, cmd := range shellCommands(c) {
		if strings.Contains(cmd, "mv -f") {
			t.Fatal("a withheld capture was moved into place")
		}
	}
}

func TestSaveToWithheldForAPrivateOrigin(t *testing.T) {
	s, c, run := captureServer(t)
	setAuto(t, s, autoTracePrivateOrigins, "bank.example")
	c.snapshotURL = "https://www.bank.example/login"
	_, err := execTool(t, s, run, "computer_browser", `{"action":"browser.snapshot","save_to":"/workspace/a.png"}`)
	wantCode(t, err, "save_to_withheld")
	_, err = execTool(t, s, run, "computer_desktop", `{"action":"desktop.capture","save_to":"/workspace/a.png"}`)
	wantCode(t, err, "save_to_withheld")
	c.snapshotURL = "https://news.example/"
	runTool(t, s, run, "computer_browser", `{"action":"browser.snapshot","save_to":"/workspace/a.png"}`)
}

func TestFullPageRunsADevToolsCaptureOfTheWholePage(t *testing.T) {
	s, c, run := captureServer(t)
	c.snapshotURL = "https://news.example/long"
	c.shell = func(cmd string) string {
		if strings.Contains(cmd, "captureBeyondViewport") {
			return `{"saved_width":1280,"saved_height":16384,"page_width":1280,"page_height":30000,"truncated":true,"url":"https://news.example/long"}`
		}
		return ""
	}
	out := runTool(t, s, run, "computer_browser", `{"action":"browser.snapshot","save_to":"/workspace/shots/long.png","full_page":true}`)
	var res savedOut
	_ = json.Unmarshal([]byte(out), &res)
	if !res.FullPage || !res.Truncated || res.PageHeight != 30000 || res.SavedHeight != 16384 || res.SavedTo != "/workspace/shots/long.png" {
		t.Fatalf("%+v", res)
	}
	var script string
	moves := 0
	for _, cmd := range shellCommands(c) {
		if strings.Contains(cmd, "captureBeyondViewport") {
			script = cmd
		}
		if strings.Contains(cmd, "mv -f") {
			moves++
		}
	}
	opts, _ := base64.StdEncoding.DecodeString(strings.Fields(strings.SplitN(script, "python3 - ", 2)[1])[0])
	if !strings.Contains(string(opts), `"max_height":16384`) {
		t.Fatalf("height cap missing: %s", opts)
	}
	for _, want := range []string{"Page.captureScreenshot", "Page.getLayoutMetrics", "cssContentSize"} {
		if !strings.Contains(script, want) {
			t.Fatalf("full-page script lacks %s", want)
		}
	}
	// The picture is written by the guest, not sent through the import.
	if len(c.named("files.import_chunk")) != 0 || moves != 1 {
		t.Fatalf("imports %d moves %d", len(c.named("files.import_chunk")), moves)
	}
}

func TestFullPageOfAPrivatePageIsNotSaved(t *testing.T) {
	s, c, run := captureServer(t)
	setAuto(t, s, autoTracePrivateOrigins, "bank.example")
	c.snapshotURL = "https://news.example/"
	c.shell = func(cmd string) string {
		if strings.Contains(cmd, "captureBeyondViewport") {
			return `{"saved_width":1280,"saved_height":900,"page_width":1280,"page_height":900,"truncated":false,"url":"https://bank.example/home"}`
		}
		return ""
	}
	_, err := execTool(t, s, run, "computer_browser", `{"action":"browser.snapshot","save_to":"/workspace/a.png","full_page":true}`)
	wantCode(t, err, "save_to_withheld")
	for _, cmd := range shellCommands(c) {
		if strings.Contains(cmd, "mv -f") {
			t.Fatal("moved")
		}
	}
}

func TestMacCaptureSaveToNeedsABotComputer(t *testing.T) {
	s := computerTestServer(t)
	mac := enrollMac(t, s, "Studio Mac", "desktop.capture")
	run := activeComputerRun(t, s)
	var tool Tool
	for _, tl := range s.microVMTools(run) {
		if tl.Name == "computer_desktop" {
			tool = tl
		}
	}
	if tool.Name == "" {
		t.Fatal("no desktop tool for a Mac without a Bot computer")
	}
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"desktop.capture","computer_id":"`+mac.id+`","save_to":"/workspace/a.png"}`))
	wantCode(t, err, "save_to_needs_bot_computer")
}

func TestMacCaptureSaveToWritesToTheBotComputer(t *testing.T) {
	s, c, mac, run, _ := computerDesktopServer(t, "allow")
	tool := desktopTool(t, s, run)
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"desktop.capture","computer_id":"`+mac.id+`","detail":"overview","save_to":"/workspace/bots/b1/mac.png"}`))
		done <- result{out, err}
	}()
	job := mac.poll(5)
	if job == nil {
		t.Fatal("no job reached the Mac")
	}
	if strings.Contains(string(job.Args), "save_to") {
		t.Fatalf("save_to reached the Mac: %s", job.Args)
	}
	mac.answer(job, `{"image_url":"`+base64PNGURL(t, 1000, 600)+`"}`)
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	var res savedOut
	_ = json.Unmarshal([]byte(r.out), &res)
	if res.SavedTo != "/workspace/bots/b1/mac.png" || res.SavedWidth != 1000 || res.SavedHeight != 600 {
		t.Fatalf("%+v", res)
	}
	if data, _ := importedBytes(t, c); len(data) == 0 {
		t.Fatal("nothing was written to the Bot computer")
	}
}

func base64PNGURL(t *testing.T, w, h int) string {
	t.Helper()
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(patternPNG(w, h, 40))
}
