package mcprunner

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeGogDownload stands in for `gog gmail attachment` and `gog drive download`: it writes the file the real
// command would write to --out and prints the JSON the real command prints.
func fakeGogDownload(args string) {
	fields := strings.Fields(args)
	out := ""
	for i, f := range fields {
		if f == "--out" && i+1 < len(fields) {
			out = fields[i+1]
		}
	}
	switch {
	case strings.Contains(args, " gone"):
		fmt.Fprint(os.Stderr, "googleapi: Error 404: Requested entity was not found.")
		os.Exit(1)
	case strings.Contains(args, " denied"):
		fmt.Fprint(os.Stderr, "googleapi: Error 403: Request had insufficient authentication scopes.")
		os.Exit(1)
	case strings.Contains(args, " grant"):
		fmt.Fprint(os.Stderr, "oauth2: cannot fetch token: invalid_grant")
		os.Exit(1)
	case strings.Contains(args, " nofmt") && strings.Contains(args, "--format"):
		fmt.Fprint(os.Stderr, `--format "png" not supported for non-Google Workspace files`)
		os.Exit(1)
	}
	if out == "" {
		os.Exit(7)
	}
	data := []byte("file-bytes")
	if strings.Contains(args, " huge") {
		data = make([]byte, maxGogFileBytes+1)
	}
	if strings.Contains(args, "drive download") {
		ext := ".txt"
		if strings.Contains(args, "--format pdf") {
			ext = ".pdf"
		}
		out = strings.TrimSuffix(out, filepath.Ext(out)) + ext
	}
	if err := os.WriteFile(out, data, 0600); err != nil {
		os.Exit(8)
	}
	result, _ := json.Marshal(map[string]any{"path": out, "bytes": len(data), "size": len(data)})
	_, _ = os.Stdout.Write(result)
}

func newFileFakeGogRunner(t *testing.T, services ...string) *Runner {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-gog")
	script := fmt.Sprintf("#!/bin/sh\nexec '%s' -test.run=TestFakeGogProcess -- \"$@\"\n", strings.ReplaceAll(os.Args[0], "'", "'\\''"))
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "calls.log")
	r, err := New([]Spec{{ID: "gog", Kind: "builtin_gog", Command: bin, WorkDir: dir, Env: map[string]string{"TOFI_FAKE_GOG": "1", "TOFI_FAKE_GOG_LOG": log}}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	if len(services) > 0 {
		spec, _ := r.gogSpec("gog")
		data, _ := json.Marshal(gogAccount{Email: "person@gmail.com", Scope: "readonly", Services: services, ConnectedAt: time.Now()})
		if err := spec.writeInPlugin("gog-account.json", data); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func fileErrorCode(err error) string {
	var typed *GogFileError
	if errors.As(err, &typed) {
		return typed.Code
	}
	return ""
}

func TestGogAttachmentDownload(t *testing.T) {
	r := newFileFakeGogRunner(t, "gmail")
	ctx := context.Background()
	file, err := r.GogAttachment(ctx, "gog", GogAttachmentRequest{MessageID: "m1", AttachmentID: "a1", Name: "../report.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := base64.StdEncoding.DecodeString(file.DataBase64)
	if string(data) != "file-bytes" || file.Size != 10 || !strings.HasSuffix(file.Name, "report.pdf") || strings.Contains(file.Name, "/") {
		t.Fatalf("file=%+v data=%q", file, data)
	}
	cases := map[string]string{"gone": "not_found", "denied": "scope_missing", "huge": "too_large"}
	for attachment, want := range cases {
		if _, err := r.GogAttachment(ctx, "gog", GogAttachmentRequest{MessageID: "m1", AttachmentID: attachment}); fileErrorCode(err) != want {
			t.Errorf("%s: code=%q err=%v want %s", attachment, fileErrorCode(err), err, want)
		}
	}
	for _, bad := range []GogAttachmentRequest{{MessageID: "-x", AttachmentID: "a"}, {MessageID: "m", AttachmentID: "a b"}, {MessageID: "", AttachmentID: "a"}} {
		if _, err := r.GogAttachment(ctx, "gog", bad); fileErrorCode(err) != "invalid_arguments" {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	// A rejected grant flags the account, so the next call fails before gog runs.
	if _, err := r.GogAttachment(ctx, "gog", GogAttachmentRequest{MessageID: "m1", AttachmentID: "grant"}); fileErrorCode(err) != "reauth_required" {
		t.Errorf("grant failure: %v", err)
	}
	if _, err := r.GogAttachment(ctx, "gog", GogAttachmentRequest{MessageID: "m1", AttachmentID: "a1"}); fileErrorCode(err) != "reauth_required" {
		t.Errorf("flagged account: %v", err)
	}
}

func TestGogFileNeedsConnectionAndService(t *testing.T) {
	r := newFileFakeGogRunner(t)
	if _, err := r.GogAttachment(context.Background(), "gog", GogAttachmentRequest{MessageID: "m", AttachmentID: "a"}); fileErrorCode(err) != "not_connected" {
		t.Fatalf("not connected: %v", err)
	}
	r = newFileFakeGogRunner(t, "gmail")
	_, err := r.GogDriveExport(context.Background(), "gog", GogDriveRequest{FileID: "f1"})
	var typed *GogFileError
	if !errors.As(err, &typed) || typed.Code != "scope_missing" || typed.Scope != "drive" || !strings.Contains(typed.Message, "Settings") {
		t.Fatalf("drive not authorised: %v", err)
	}
}

func TestGogDriveExport(t *testing.T) {
	r := newFileFakeGogRunner(t, "gmail", "drive")
	ctx := context.Background()
	file, err := r.GogDriveExport(ctx, "gog", GogDriveRequest{FileID: "f1", Format: "pdf"})
	if err != nil || !strings.HasSuffix(file.Name, ".pdf") || file.Size != 10 {
		t.Fatalf("pdf export=%+v err=%v", file, err)
	}
	file, err = r.GogDriveExport(ctx, "gog", GogDriveRequest{FileID: "f1"})
	if err != nil || !strings.HasSuffix(file.Name, ".txt") {
		t.Fatalf("default export=%+v err=%v", file, err)
	}
	if _, err := r.GogDriveExport(ctx, "gog", GogDriveRequest{FileID: "f1", Format: "exe"}); fileErrorCode(err) != "unsupported_format" {
		t.Errorf("bad format: %v", err)
	}
	if _, err := r.GogDriveExport(ctx, "gog", GogDriveRequest{FileID: "nofmt", Format: "png"}); fileErrorCode(err) != "unsupported_format" {
		t.Errorf("format refused by gog: %v", err)
	}
	if _, err := r.GogDriveExport(ctx, "gog", GogDriveRequest{FileID: "huge"}); fileErrorCode(err) != "too_large" {
		t.Errorf("oversize: %v", err)
	}
}
