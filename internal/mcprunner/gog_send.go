package mcprunner

import (
	"context"
	"encoding/json"
	"errors"
	"net/mail"
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
}

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
	return nil
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
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	output, err := runGog(ctx, spec, []byte(input.Body), "--json", "--no-input", "--account", account.Email, "gmail", "send", "--to", input.To, "--subject", input.Subject, "--body-file", "-")
	if err != nil {
		return nil, errors.New("Gmail send did not return a confirmed result; check Sent mail before retrying")
	}
	if !json.Valid(output) {
		return nil, errors.New("Gmail send returned an unrecognized result; check Sent mail before retrying")
	}
	return json.RawMessage(output), nil
}
