package app

// save_to on every capture action. One function (saveCapture) writes every
// saved capture to the Bot computer: a desktop.capture or browser.snapshot of
// the Bot computer, a full-page capture, and a capture of a person's Mac. The
// bytes reach the guest through the uploads folder (the only place its file
// import accepts) and one shell.exec moves them to the requested path.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/png"
	"path"
	"strings"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const (
	saveToRoot = "/workspace/"
	// fullPageMaxHeight caps a full-page capture, in CSS pixels.
	fullPageMaxHeight = 16384
)

const saveToDescription = "Absolute path under /workspace ending in .png. The full-size picture of this capture, before any detail scaling, is written there (an existing file at the path is replaced) and the result carries saved_to, saved_width and saved_height. A capture of a person's computer is written to the Bot computer. Refused with save_to_withheld while a Secret Input window is open or when the page is private."

const fullPageDescription = "browser.snapshot on the Bot computer, with save_to only: the saved picture is the whole page, up to 16384 CSS pixels high. The result carries page_height and truncated: true when the page was cut. The picture you see stays the visible area, so coordinates in the saved file differ from coordinates in that picture."

var saveToParam = map[string]any{"type": "string", "description": saveToDescription}
var fullPageParam = map[string]any{"type": "boolean", "description": fullPageDescription}

// captureSave is the save request of one capture call.
type captureSave struct {
	Path     string
	FullPage bool
}

func saveRefusal(code, message, next string) error {
	return tooloutcome.New(tooloutcome.Validation, code, "not_executed", message, next).Err()
}

// captureSaveOf reads and checks save_to and full_page. Nothing has run yet.
func captureSaveOf(raw json.RawMessage, action string) (captureSave, error) {
	var in struct {
		SaveTo   string `json:"save_to"`
		FullPage bool   `json:"full_page"`
	}
	_ = json.Unmarshal(raw, &in)
	save := captureSave{Path: in.SaveTo, FullPage: in.FullPage}
	if save.FullPage && save.Path == "" {
		return save, saveRefusal("full_page_needs_save_to", "full_page works only together with save_to: the whole page is written to that file.", "repair_arguments")
	}
	if save.FullPage && action != "browser.snapshot" {
		return save, saveRefusal("save_to_invalid", "full_page is for browser.snapshot.", "repair_arguments")
	}
	if save.Path == "" {
		return save, nil
	}
	if action != "desktop.capture" && action != "browser.snapshot" {
		return save, saveRefusal("save_to_invalid", "save_to belongs to desktop.capture and browser.snapshot.", "repair_arguments")
	}
	if err := checkSaveToPath(save.Path); err != nil {
		return save, err
	}
	return save, nil
}

func checkSaveToPath(p string) error {
	bad := func(why string) error {
		return saveRefusal("save_to_invalid", "save_to "+why+": it must be an absolute path under /workspace that ends in .png.", "repair_arguments")
	}
	if len(p) > 512 || strings.ContainsRune(p, 0) || !strings.HasPrefix(p, "/") {
		return bad("is not an acceptable path")
	}
	if path.Clean(p) != p || strings.Contains(p, "/../") {
		return bad("is not a normalized path")
	}
	if !strings.HasPrefix(p, saveToRoot) || len(p) == len(saveToRoot) {
		return bad("is outside /workspace")
	}
	if !strings.HasSuffix(strings.ToLower(p), ".png") || strings.HasSuffix(p, "/.png") {
		return bad("does not name a .png file")
	}
	return nil
}

// saveWithheld names the work-trace rule that keeps this capture from being
// saved, or "". pageURL is the page the picture shows.
func (s *Server) saveWithheld(pageURL string, resolved bool) bool {
	if s.store == nil {
		return false
	}
	return s.traceRedaction(pageURL, resolved) != ""
}

func saveWithheldError() error {
	return saveRefusal("save_to_withheld", "This capture is not saved: a Secret Input window is open or the page is private. Nothing was written.", "explain_blocker")
}

// captureSaveBytes returns the PNG bytes and size of a capture result's picture.
func captureSaveBytes(picture string) ([]byte, int, int, bool) {
	_, uri, ok := captureResultFields(picture)
	if !ok {
		return nil, 0, 0, false
	}
	mime, encoded, found := strings.Cut(strings.TrimPrefix(uri, "data:"), ";base64,")
	if !found {
		return nil, 0, 0, false
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) == 0 {
		return nil, 0, 0, false
	}
	if mime == "image/png" {
		if cfg, err := png.DecodeConfig(bytes.NewReader(data)); err == nil {
			return data, cfg.Width, cfg.Height, true
		}
	}
	img, ok := decodeCaptureImage(uri)
	if !ok {
		return nil, 0, 0, false
	}
	var buf bytes.Buffer
	if png.Encode(&buf, img) != nil {
		return nil, 0, 0, false
	}
	b := img.Bounds()
	return buf.Bytes(), b.Dx(), b.Dy(), true
}

func guestQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// saveTempPath is a fresh file under the uploads folder.
func saveTempPath() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return guestUploadsRoot + "/.capture-tmp/cap-" + hex.EncodeToString(b) + ".png"
}

// saveMoveScript moves tmp to dst. The parent folder's symlinks are resolved
// before the /workspace check; the move is a rename on one filesystem, else a
// copy beside the target and a rename; an existing file at dst is replaced.
const saveMoveScript = `dst=%s; tmp=%s
fail() { rm -f -- "$tmp"; echo "$1" >&2; exit 3; }
name=$(basename -- "$dst")
check() { case "$1/" in /workspace/.tofi-env/*|/workspace/browser/*|*/.ssh/*|*/.gnupg/*|*/.aws/*) fail save_to_forbidden_folder;; /workspace/*) ;; *) fail save_to_outside_workspace;; esac; }
rp=$(realpath -m -- "$(dirname -- "$dst")") || fail save_to_unresolvable
check "$rp"
mkdir -p -- "$rp" || fail save_to_unwritable
rp=$(realpath -- "$rp") || fail save_to_unresolvable
check "$rp"
target="$rp/$name"
[ -d "$target" ] && fail save_to_is_folder
if [ "$(stat -c %%d -- "$tmp")" = "$(stat -c %%d -- "$rp")" ]; then
  mv -f -- "$tmp" "$target" || fail save_to_unwritable
else
  part=$(mktemp -p "$rp" .tofi-save-XXXXXX) || fail save_to_unwritable
  cp -- "$tmp" "$part" && mv -f -- "$part" "$target" || { rm -f -- "$part"; fail save_to_unwritable; }
  rm -f -- "$tmp"
fi
echo saved
`

// finishSave moves the guest temp file to the requested path.
func (s *Server) finishSave(ctx context.Context, r Run, dst, tmp string) error {
	command := fmt.Sprintf(saveMoveScript, guestQuote(dst), guestQuote(tmp))
	args, _ := json.Marshal(map[string]any{"command": command, "timeout_sec": 30})
	out, err := s.microVMAction(ctx, r, "shell.exec", args)
	if err != nil {
		return err
	}
	var shell struct {
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode int    `json:"exit_code"`
	}
	if json.Unmarshal([]byte(out), &shell) != nil {
		return fmt.Errorf("saving the capture failed: the Bot computer's answer could not be read")
	}
	if shell.ExitCode == 0 && strings.Contains(shell.Stdout, "saved") {
		return nil
	}
	reason := strings.TrimSpace(shell.Stderr)
	if shell.ExitCode == 3 && strings.HasPrefix(reason, "save_to_") {
		return saveRefusal("save_to_invalid", "save_to cannot be written ("+reason+"): use a file path under /workspace outside the browser profile and key folders.", "repair_arguments")
	}
	return fmt.Errorf("saving the capture failed: %s", reason)
}

// dropGuestTemp removes a temp file whose capture is not saved.
func (s *Server) dropGuestTemp(ctx context.Context, r Run, tmp string) {
	args, _ := json.Marshal(map[string]any{"command": "rm -f -- " + guestQuote(tmp), "timeout_sec": 10})
	_, _ = s.microVMAction(ctx, r, "shell.exec", args)
}

// saveCapture is the one function that writes a saved capture. data is the
// picture; tmp, when data is nil, is a file the guest already holds (a full-page
// capture). The caller has decided the capture is not withheld.
func (s *Server) saveCapture(ctx context.Context, r Run, dst string, data []byte, tmp string) error {
	if data != nil {
		tmp = saveTempPath()
		if err := s.pushGuestBytes(ctx, r, tmp, data); err != nil {
			return fmt.Errorf("saving the capture failed: %w", err)
		}
	}
	return s.finishSave(ctx, r, dst, tmp)
}

// withSavedFields adds fields to a result object.
func withSavedFields(result string, add map[string]any) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(result), &fields) != nil {
		return result
	}
	for k, v := range add {
		raw, _ := json.Marshal(v)
		fields[k] = raw
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return result
	}
	return string(out)
}

// saveBotCapture saves the picture of a Bot computer capture. picture is the
// guest's result before detail scaling; out is what the model reads.
func (s *Server) saveBotCapture(ctx context.Context, r Run, action string, save captureSave, picture, out string) (string, error) {
	withheld := s.store != nil && s.store.secretWindowOpen()
	if !withheld {
		var kept struct {
			ID string `json:"capture_id"`
		}
		_ = json.Unmarshal([]byte(out), &kept)
		if k, ok := s.keptCaptureOf(r.ID, kept.ID); ok {
			withheld = k.Withheld
		} else if action == "browser.snapshot" {
			url := pageURLFromResult(picture)
			withheld = s.saveWithheld(url, url != "")
		}
	}
	if withheld {
		return "", saveWithheldError()
	}
	if save.FullPage {
		return s.saveFullPage(ctx, r, save, out)
	}
	data, w, h, ok := captureSaveBytes(picture)
	if !ok {
		return "", fmt.Errorf("the capture carries no picture to save")
	}
	if err := s.saveCapture(ctx, r, save.Path, data, ""); err != nil {
		return "", err
	}
	return withSavedFields(out, map[string]any{"saved_to": save.Path, "saved_width": w, "saved_height": h}), nil
}

// saveMacCapture saves the picture of a person's Mac on the Bot computer. The
// owner rule (guardShare) is asked on every save.
func (s *Server) saveMacCapture(ctx context.Context, r Run, macName string, save captureSave, full, out string) (res string, err error) {
	data, w, h, ok := captureSaveBytes(full)
	if !ok {
		return "", fmt.Errorf("the capture carries no picture to save")
	}
	settle, err := s.guardShare(ctx, r, macName)
	if err != nil {
		return "", err
	}
	defer func() { settle(err) }()
	if err = s.saveCapture(ctx, r, save.Path, data, ""); err != nil {
		return "", err
	}
	return withSavedFields(out, map[string]any{"saved_to": save.Path, "saved_width": w, "saved_height": h}), nil
}

// saveNeedsBotComputer is the refusal for a save request with no Bot computer.
func saveNeedsBotComputer() error {
	return saveRefusal("save_to_needs_bot_computer", "save_to writes a file on the Bot computer, and this account has none. Nothing was captured.", "explain_blocker")
}

const fullPageHelper = `
import math, os, struct
tmp = opts["tmp"]
metrics = send(target, "Page.getLayoutMetrics", {})
size = metrics.get("cssContentSize") or metrics.get("contentSize") or {}
page_w, page_h = int(math.ceil(size.get("width", 0))), int(math.ceil(size.get("height", 0)))
if page_w < 1 or page_h < 1:
    print(json.dumps({"error": "full_page_no_size"})); sys.exit(0)
cut_h = min(page_h, opts["max_height"])
shot = send(target, "Page.captureScreenshot", {"format": "png", "fromSurface": True, "captureBeyondViewport": True, "clip": {"x": 0, "y": 0, "width": page_w, "height": cut_h, "scale": 1}})
if not shot.get("data"):
    print(json.dumps({"error": "full_page_capture_failed"})); sys.exit(0)
png = base64.b64decode(shot["data"])
os.makedirs(os.path.dirname(tmp), exist_ok=True)
with open(tmp, "wb") as f:
    f.write(png)
w, h = struct.unpack(">II", png[16:24])
print(json.dumps({"saved_width": w, "saved_height": h, "page_width": page_w, "page_height": page_h, "truncated": page_h > cut_h, "url": page_target.get("url", ""), "title": page_target.get("title", "")}, ensure_ascii=False))
`

// fullPageCommand is the shell.exec payload of one full-page capture.
func fullPageCommand(tmp string) (json.RawMessage, error) {
	opts, err := json.Marshal(map[string]any{"tmp": tmp, "max_height": fullPageMaxHeight})
	if err != nil {
		return nil, err
	}
	command := "python3 - " + base64.StdEncoding.EncodeToString(opts) + " <<'TOFI_BROWSER_FULLPAGE'\n" + browserScriptHead + fullPageHelper + "\nTOFI_BROWSER_FULLPAGE"
	return json.Marshal(map[string]any{"command": command, "timeout_sec": 60})
}

// saveFullPage replaces the file of a snapshot's save_to with the whole page.
// The page that was captured (its DevTools target) decides whether it is withheld.
func (s *Server) saveFullPage(ctx context.Context, r Run, save captureSave, out string) (string, error) {
	tmp := saveTempPath()
	args, err := fullPageCommand(tmp)
	if err != nil {
		return "", err
	}
	res, err := s.microVMAction(ctx, r, "shell.exec", args)
	if err != nil {
		return "", err
	}
	page, err := browserReadResult(res)
	if err != nil {
		return "", err
	}
	var got struct {
		Error       string `json:"error"`
		SavedWidth  int    `json:"saved_width"`
		SavedHeight int    `json:"saved_height"`
		PageWidth   int    `json:"page_width"`
		PageHeight  int    `json:"page_height"`
		Truncated   bool   `json:"truncated"`
		URL         string `json:"url"`
	}
	if json.Unmarshal([]byte(page), &got) != nil {
		return "", fmt.Errorf("full_page capture returned an unreadable answer")
	}
	if got.Error != "" {
		s.dropGuestTemp(ctx, r, tmp)
		return "", fmt.Errorf("full_page capture failed: %s", got.Error)
	}
	if (s.store != nil && s.store.secretWindowOpen()) || s.saveWithheld(got.URL, got.URL != "") {
		s.dropGuestTemp(ctx, r, tmp)
		return "", saveWithheldError()
	}
	if err := s.finishSave(ctx, r, save.Path, tmp); err != nil {
		return "", err
	}
	return withSavedFields(out, map[string]any{"saved_to": save.Path, "saved_width": got.SavedWidth, "saved_height": got.SavedHeight, "full_page": true, "page_width": got.PageWidth, "page_height": got.PageHeight, "truncated": got.Truncated}), nil
}
