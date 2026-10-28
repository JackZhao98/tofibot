package mcprunner

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// GogSendRequest is an exact, owner-approved draft. It is never exposed as an
// MCP tool; the app calls this private endpoint after a matching approval.
type GogSendRequest struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`

	Attachments []GogAttachment `json:"attachments,omitempty"`
}

// GogAttachment is one file of an approved draft. gog builds the MIME message
// (multipart/mixed, encoded filename, content type) from the file it is given.
type GogAttachment struct {
	Name       string `json:"name"`
	MIME       string `json:"mime"`
	DataBase64 string `json:"data_base64"`
}

const (
	maxGogAttachments  = 10
	maxGogAttachBytes  = 20 << 20
	gogSendBodyLimit   = 32 << 20
	gogSendTimeoutBase = 60 * time.Second
)

func (input *GogSendRequest) validate() error {
	input.To = strings.TrimSpace(input.To)
	input.Subject = strings.TrimSpace(input.Subject)
	if input.To == "" || len(input.To) > 2048 || input.Subject == "" || utf8.RuneCountInString(input.Subject) > 512 || strings.ContainsAny(input.Subject, "\r\n") || strings.TrimSpace(input.Body) == "" || len(input.Body) > 128<<10 || !utf8.ValidString(input.Body) {
		return errors.New("valid recipients, subject and body are required")
	}
	addresses := strings.Split(input.To, ",")
	if len(addresses) > 20 {
		return errors.New("too many recipients")
	}
	for i, address := range addresses {
		address = strings.TrimSpace(address)
		parsed, err := mail.ParseAddress(address)
		if err != nil || parsed.Address != address || strings.ContainsAny(address, "\r\n") {
			return errors.New("invalid recipient address")
		}
		addresses[i] = address
	}
	input.To = strings.Join(addresses, ",")
	if len(input.Attachments) > maxGogAttachments {
		return errors.New("too many attachments")
	}
	return nil
}

// attachmentFileName keeps the person's file name, minus anything that could
// leave its directory or be read as an option.
func attachmentFileName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '/' || r == '\\' || r == 0 {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if len(name) > 200 {
		cut := 200
		for !utf8.RuneStart(name[cut]) {
			cut--
		}
		name = name[:cut]
	}
	if name == "" || name == "." || name == ".." || strings.HasPrefix(name, "-") {
		name = "attachment" + name
	}
	return name
}

// writeGogAttachments puts each file in its own folder under a private
// temporary directory, so the real name survives and duplicates cannot collide.
// The account owns its TMPDIR, so every step goes through a Root anchored there:
// a link the account plants cannot redirect a root-side write.
func writeGogAttachments(spec Spec, attachments []GogAttachment) (dir string, paths []string, err error) {
	if len(attachments) == 0 {
		return "", nil, nil
	}
	base := spec.tmpDir()
	tmp, err := openTmpRoot(spec)
	if err != nil {
		return "", nil, err
	}
	defer tmp.Close()
	suffix := make([]byte, 8)
	if _, err = rand.Read(suffix); err != nil {
		return "", nil, err
	}
	name := "tofi-mail-" + hex.EncodeToString(suffix)
	if err = tmp.Mkdir(name, 0700); err != nil {
		return "", nil, err
	}
	fail := func(e error) (string, []string, error) {
		_ = tmp.RemoveAll(name)
		return "", nil, e
	}
	// The gogcli child runs as the account's uid and must be able to read them.
	if err = chownIn(spec.Cred, tmp, name); err != nil {
		return fail(err)
	}
	var total int64
	for i, a := range attachments {
		data, e := base64.StdEncoding.DecodeString(a.DataBase64)
		total += int64(len(data))
		if e != nil || total > maxGogAttachBytes {
			return fail(errors.New("invalid attachment"))
		}
		sub := filepath.Join(name, strconv.Itoa(i))
		rel := filepath.Join(sub, attachmentFileName(a.Name))
		if e = tmp.Mkdir(sub, 0700); e == nil {
			e = chownIn(spec.Cred, tmp, sub)
		}
		if e == nil {
			if e = tmp.WriteFile(rel, data, 0600); e == nil {
				e = chownIn(spec.Cred, tmp, rel)
			}
		}
		if e != nil {
			return fail(e)
		}
		paths = append(paths, filepath.Join(base, rel))
	}
	return filepath.Join(base, name), paths, nil
}

// removeGogAttachments deletes a folder writeGogAttachments made, without
// following anything the account put in it.
func removeGogAttachments(spec Spec, dir string) {
	base := spec.tmpDir()
	tmp, err := openTmpRoot(spec)
	if err != nil {
		return
	}
	defer tmp.Close()
	if rel, err := filepath.Rel(base, dir); err == nil && !strings.HasPrefix(rel, "..") && rel != "." {
		_ = tmp.RemoveAll(rel)
	}
}

func (r *Runner) GogSend(ctx context.Context, id string, input GogSendRequest) (json.RawMessage, error) {
	if err := input.validate(); err != nil {
		return nil, err
	}
	p, err := r.get(id)
	if err != nil {
		return nil, err
	}
	p.gogMu.Lock()
	defer p.gogMu.Unlock()
	spec, err := r.gogSpec(id)
	if err != nil {
		return nil, err
	}
	account, err := r.GogStatus(id)
	if err != nil || account.Email == "" || account.Scope != "read-send" {
		return nil, errors.New("Gmail send authorization is required")
	}
	dir, files, err := writeGogAttachments(spec, input.Attachments)
	if err != nil {
		return nil, err
	}
	if dir != "" {
		defer removeGogAttachments(spec, dir)
	}
	timeout := gogSendTimeoutBase
	if len(files) > 0 {
		timeout += 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := []string{"--json", "--no-input", "--account", account.Email, "gmail", "send", "--to", input.To, "--subject", input.Subject, "--body-file", "-"}
	for _, f := range files {
		args = append(args, "--attach", f)
	}
	output, err := runGog(ctx, spec, []byte(input.Body), args...)
	if err != nil {
		return nil, errors.New("Gmail send did not return a confirmed result; check Sent mail before retrying")
	}
	if !json.Valid(output) {
		return nil, errors.New("Gmail send returned an unrecognized result; check Sent mail before retrying")
	}
	return json.RawMessage(output), nil
}

// openTmpRoot opens the account's TMPDIR, which must be a real directory. Only
// the single-tenant Runner's shared /tmp may be a link (it is not an account's).
func openTmpRoot(spec Spec) (*os.Root, error) {
	if spec.Tmp == "" {
		return os.OpenRoot(spec.tmpDir())
	}
	return openDirRoot(spec.Tmp)
}
