package mcprunner

import (
	"context"
	"encoding/base64"
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
func writeGogAttachments(attachments []GogAttachment) (dir string, paths []string, err error) {
	if len(attachments) == 0 {
		return "", nil, nil
	}
	dir, err = os.MkdirTemp("", "tofi-mail-")
	if err != nil {
		return "", nil, err
	}
	var total int64
	for i, a := range attachments {
		data, e := base64.StdEncoding.DecodeString(a.DataBase64)
		total += int64(len(data))
		if e != nil || total > maxGogAttachBytes {
			_ = os.RemoveAll(dir)
			return "", nil, errors.New("invalid attachment")
		}
		sub := filepath.Join(dir, strconv.Itoa(i))
		path := filepath.Join(sub, attachmentFileName(a.Name))
		if e = os.Mkdir(sub, 0700); e == nil {
			e = os.WriteFile(path, data, 0600)
		}
		if e != nil {
			_ = os.RemoveAll(dir)
			return "", nil, e
		}
		paths = append(paths, path)
	}
	return dir, paths, nil
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
	dir, files, err := writeGogAttachments(input.Attachments)
	if err != nil {
		return nil, err
	}
	if dir != "" {
		defer os.RemoveAll(dir)
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
