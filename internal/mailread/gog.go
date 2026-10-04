// Package mailread recognizes only TOFI's pinned built-in Gmail read output.
// Identity must be supplied by the private Runner, never by model arguments.
package mailread

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const MetaKey = "tofi.dev/mail-read-context"

type Identity struct {
	Version    int    `json:"version"`
	Provider   string `json:"provider"`
	Connection string `json:"connection"`
	Mailbox    string `json:"mailbox"`
}
type Message struct {
	ID            string   `json:"id"`
	From          string   `json:"from"`
	To            string   `json:"to,omitempty"`
	Subject       string   `json:"subject"`
	ReceivedAt    string   `json:"received_at,omitempty"`
	Body          string   `json:"body"`
	BodyAvailable bool     `json:"body_available"`
	Attachments   []string `json:"attachments"`
}
type Snapshot struct {
	Identity
	Tool     string    `json:"tool"`
	Digest   string    `json:"digest"`
	Messages []Message `json:"messages"`
}

func Digest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
func Supported(tool string) bool { return tool == "gmail_search" || tool == "gmail_get_message" }

// Strip only the exact pinned wrapper with matching delimiters. The original
// wrapped result remains in tool history; this is display text, still untrusted.
var wrapped = regexp.MustCompile(`(?s)^<<<EXTERNAL_UNTRUSTED_CONTENT id="([a-zA-Z0-9]+)">>>
Source: google_api
---
(.*)
<<<END_EXTERNAL_UNTRUSTED_CONTENT id="([a-zA-Z0-9]+)">>>$`)

func displayText(text string) string {
	if parts := wrapped.FindStringSubmatch(text); parts != nil && parts[1] == parts[3] {
		return parts[2]
	}
	return text
}

// NormalizeGog is deliberately closed to other providers and arbitrary field maps.
// root is the original structuredContent from gogcli v0.40.0.
func NormalizeGog(identity Identity, tool string, root any, result string) (Snapshot, error) {
	fail := func() (Snapshot, error) {
		return Snapshot{}, errors.New("unsupported or incomplete trusted Gmail read")
	}
	if identity.Version != 1 || identity.Provider != "gmail" || identity.Connection == "" || identity.Mailbox == "" || !Supported(tool) {
		return fail()
	}
	raw, err := json.Marshal(root)
	if err != nil {
		return fail()
	}
	var envelope struct {
		Tool     string `json:"tool"`
		Service  string `json:"service"`
		Risk     string `json:"risk"`
		ExitCode *int   `json:"exit_code"`
		Stdout   struct {
			Messages []json.RawMessage `json:"messages"`
			Message  json.RawMessage   `json:"message"`
		} `json:"stdout"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Tool != tool || envelope.Service != "gmail" || envelope.Risk != "read" || envelope.ExitCode == nil || *envelope.ExitCode != 0 {
		return fail()
	}
	records := envelope.Stdout.Messages
	if tool == "gmail_get_message" {
		if len(envelope.Stdout.Message) == 0 {
			return fail()
		}
		records = []json.RawMessage{envelope.Stdout.Message}
	} else if records == nil {
		return fail()
	}
	if len(records) > 100 {
		return fail()
	}
	snapshot := Snapshot{Identity: identity, Tool: tool, Digest: Digest(result), Messages: []Message{}}
	seen := map[string]bool{}
	for _, rawRecord := range records {
		var record struct {
			ID              string            `json:"id"`
			From            string            `json:"from"`
			Subject         string            `json:"subject"`
			InternalDateISO string            `json:"internalDateIso"`
			InternalDate    int64             `json:"internalDate"`
			Headers         map[string]string `json:"headers"`
			Body            *string           `json:"body"`
			Attachments     []struct {
				Filename string `json:"filename"`
			} `json:"attachments"`
		}
		if json.Unmarshal(rawRecord, &record) != nil {
			return fail()
		}
		message := Message{ID: record.ID, From: record.From, Subject: displayText(record.Subject), ReceivedAt: record.InternalDateISO, Attachments: []string{}}
		if tool == "gmail_get_message" {
			if record.Headers == nil {
				return fail()
			} // only the default sanitized get schema
			message.From = record.Headers["from"]
			message.To = record.Headers["to"]
			message.Subject = displayText(record.Headers["subject"])
			if record.InternalDate > 0 {
				message.ReceivedAt = time.UnixMilli(record.InternalDate).UTC().Format(time.RFC3339)
			}
		}
		if message.ID == "" || message.From == "" || seen[message.ID] {
			return fail()
		}
		seen[message.ID] = true
		// Missing subject is an actual empty subject, not invented text.
		for _, value := range []string{identity.Connection, identity.Mailbox, message.ID, message.From, message.To, message.Subject, message.ReceivedAt} {
			if utf8.RuneCountInString(value) > 512 || strings.ContainsRune(value, 0) {
				return fail()
			}
		}
		if message.ReceivedAt != "" {
			if _, err := time.Parse(time.RFC3339, message.ReceivedAt); err != nil {
				return fail()
			}
		}
		if record.Body != nil {
			message.BodyAvailable = true
			message.Body = displayText(*record.Body)
			if utf8.RuneCountInString(message.Body) > 32000 {
				return fail()
			}
		}
		if len(record.Attachments) > 50 {
			return fail()
		}
		for _, attachment := range record.Attachments {
			name := attachment.Filename
			if name == "" || utf8.RuneCountInString(name) > 512 {
				return fail()
			}
			message.Attachments = append(message.Attachments, name)
		}
		snapshot.Messages = append(snapshot.Messages, message)
	}
	return snapshot, nil
}

type recorderKey struct{}
type Recorder func(context.Context, Snapshot, string) (string, error)

func WithRecorder(ctx context.Context, fn Recorder) context.Context {
	return context.WithValue(ctx, recorderKey{}, fn)
}
func Record(ctx context.Context, snapshot Snapshot, result string) (string, error) {
	if fn, _ := ctx.Value(recorderKey{}).(Recorder); fn != nil {
		return fn(ctx, snapshot, result)
	}
	return result, nil
}
