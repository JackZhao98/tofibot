package mcprunner

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// GogFileError is a typed refusal of a file operation. Code is stable (the app and the model branch on it);
// Message names what to do and never carries Google's own text.
type GogFileError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Scope names the Google permission that is missing, when Code is scope_missing.
	Scope string `json:"scope,omitempty"`
	// Reauth is true when the person has to authorise Google again.
	Reauth bool `json:"reauth,omitempty"`
}

func (e *GogFileError) Error() string { return e.Message }

func fileErr(code, message string) *GogFileError { return &GogFileError{Code: code, Message: message} }

// maxGogFileBytes bounds one downloaded file: the same limit as an email's attachments.
const maxGogFileBytes = 20 << 20

const gogFileTimeout = 3 * time.Minute

var gogIDPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]{0,255}$`)

// GogAttachmentRequest names one attachment of one message. Name is optional and only labels the file.
type GogAttachmentRequest struct {
	MessageID    string `json:"message_id"`
	AttachmentID string `json:"attachment_id"`
	Name         string `json:"name,omitempty"`
}

// GogFile is a downloaded file, bounded by maxGogFileBytes.
type GogFile struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	DataBase64 string `json:"data_base64"`
}

// GogDriveRequest names one Drive file. Format is a gog export format for Google Docs, Sheets and Slides.
type GogDriveRequest struct {
	FileID string `json:"file_id"`
	Format string `json:"format,omitempty"`
}

var gogExportFormats = map[string]bool{"pdf": true, "csv": true, "xlsx": true, "pptx": true, "txt": true, "docx": true, "md": true, "html": true, "png": true}

// gogRequireService returns the connected account when it holds the service.
func (r *Runner) gogRequireService(id, service string) (Spec, gogAccount, error) {
	spec, err := r.gogSpec(id)
	if err != nil {
		return Spec{}, gogAccount{}, err
	}
	account, err := r.GogStatus(id)
	if err != nil || account.Email == "" {
		return Spec{}, gogAccount{}, fileErr("not_connected", "No Google account is connected. Ask the person to connect Google in Settings > Connections (/settings/connections/local_gog).")
	}
	if r.GogNeedsReauth(id) {
		e := fileErr("reauth_required", GogReauthMessage)
		e.Reauth = true
		return Spec{}, gogAccount{}, e
	}
	for _, have := range authorisedGogServices(account.Services) {
		if have == service {
			return spec, account, nil
		}
	}
	title := strings.ToUpper(service[:1]) + service[1:]
	e := fileErr("scope_missing", title+" is not authorised on the connected Google account. Ask the person to add "+title+" in Settings > Connections (/settings/connections/local_gog); Google asks them to approve it and keeps what is already granted.")
	e.Scope = service
	return Spec{}, gogAccount{}, e
}

// gogHTTP404 matches a 404 that gog reports as a status, not a file name that happens to contain the digits.
var gogHTTP404 = regexp.MustCompile(`(?:status|error|code|http)[ :=]*"?404\b|\b404 (?:not found|error)`)

// classifyGogFileFailure turns a failed gog command into a typed refusal. Only Google's structured errors and a
// few stable status markers decide the code; the text is never passed on.
func (r *Runner) classifyGogFileFailure(id, service string, err error) error {
	if disabled := classifyGogFailure(err, ""); disabled != nil {
		return disabled
	}
	var failed *gogCommandError
	if !errors.As(err, &failed) {
		return fileErr("failed", "Google did not return the file. Try again later.")
	}
	lower := strings.ToLower(failed.output)
	switch {
	case IsGogGrantFailure(failed.output):
		r.GogMarkReauth(id)
		e := fileErr("reauth_required", GogReauthMessage)
		e.Reauth = true
		return e
	case strings.Contains(lower, "insufficient") && (strings.Contains(lower, "scope") || strings.Contains(lower, "permission")):
		e := fileErr("scope_missing", "The Google permission granted does not cover this. Ask the person to reconnect Google in Settings > Connections (/settings/connections/local_gog) and approve the requested access.")
		e.Scope = service
		return e
	case strings.Contains(lower, "not found") || gogHTTP404.MatchString(lower) || strings.Contains(lower, "notfound"):
		return fileErr("not_found", "Google has no such item, or the account cannot see it. Check the id with a search first.")
	case strings.Contains(lower, "--format") || strings.Contains(lower, "not supported") || strings.Contains(lower, "cannot export") || strings.Contains(lower, "exportformat"):
		return fileErr("unsupported_format", "This item cannot be exported in that format. Leave format empty to use the default for its type.")
	case strings.Contains(lower, "too large") || strings.Contains(lower, "exportsizelimit"):
		return fileErr("too_large", "The item is too large for Google to export. Ask the person to share a smaller part.")
	}
	return fileErr("failed", "Google did not return the file. Try again later.")
}

// gogFetchFile runs one gog command that writes a single file, then reads it back through a Root anchored at the
// account's TMPDIR so a link the account plants cannot redirect the read. build receives the output path.
func (r *Runner) gogFetchFile(ctx context.Context, id, service string, build func(account gogAccount, out string) []string, fallbackName string) (GogFile, error) {
	spec, account, err := r.gogRequireService(id, service)
	if err != nil {
		return GogFile{}, err
	}
	p, err := r.get(id)
	if err != nil {
		return GogFile{}, err
	}
	// The same lock as the other account operations: gog keeps its token file under it.
	p.gogMu.Lock()
	defer p.gogMu.Unlock()
	tmp, err := openTmpRoot(spec)
	if err != nil {
		return GogFile{}, fileErr("failed", "The runner could not prepare a download folder.")
	}
	defer tmp.Close()
	suffix := make([]byte, 8)
	if _, err = rand.Read(suffix); err != nil {
		return GogFile{}, err
	}
	dir := "tofi-file-" + hex.EncodeToString(suffix)
	if err = tmp.Mkdir(dir, 0700); err != nil {
		return GogFile{}, fileErr("failed", "The runner could not prepare a download folder.")
	}
	defer func() { _ = tmp.RemoveAll(dir) }()
	if err = chownIn(spec.Cred, tmp, dir); err != nil {
		return GogFile{}, fileErr("failed", "The runner could not prepare a download folder.")
	}
	out := filepath.Join(spec.tmpDir(), dir, attachmentFileName(fallbackName))
	ctx, cancel := context.WithTimeout(ctx, gogFileTimeout)
	defer cancel()
	output, err := runGog(ctx, spec, nil, build(account, out)...)
	if err != nil {
		return GogFile{}, r.classifyGogFileFailure(id, service, err)
	}
	rel := filepath.Join(dir, filepath.Base(out))
	// Drive appends the export's extension to the name it was given; its JSON says what it wrote.
	var written struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(output, &written) == nil && written.Path != "" {
		if base := filepath.Base(written.Path); filepath.Dir(written.Path) == filepath.Dir(out) && base != "." && base != string(filepath.Separator) {
			rel = filepath.Join(dir, base)
		}
	}
	f, err := tmp.Open(rel)
	if err != nil {
		return GogFile{}, fileErr("failed", "Google returned no file.")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return GogFile{}, fileErr("failed", "Google returned no file.")
	}
	if info.Size() > maxGogFileBytes {
		return GogFile{}, fileErr("too_large", "The file is larger than the 20 MB limit for a single download.")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxGogFileBytes+1))
	if err != nil {
		return GogFile{}, fileErr("failed", "Google returned no file.")
	}
	if len(data) > maxGogFileBytes {
		return GogFile{}, fileErr("too_large", "The file is larger than the 20 MB limit for a single download.")
	}
	return GogFile{Name: filepath.Base(rel), Size: int64(len(data)), DataBase64: base64.StdEncoding.EncodeToString(data)}, nil
}

// GogAttachment downloads one Gmail attachment through `gog gmail attachment`.
func (r *Runner) GogAttachment(ctx context.Context, id string, in GogAttachmentRequest) (GogFile, error) {
	if !gogIDPattern.MatchString(in.MessageID) || !gogIDPattern.MatchString(in.AttachmentID) {
		return GogFile{}, fileErr("invalid_arguments", "message_id and attachment_id must be the ids a Gmail message read returned.")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "attachment.bin"
	}
	return r.gogFetchFile(ctx, id, "gmail", func(account gogAccount, out string) []string {
		return []string{"--json", "--no-input", "--readonly", "--account", account.Email, "gmail", "attachment", "--out", out, "--", in.MessageID, in.AttachmentID}
	}, name)
}

// GogDriveExport downloads a Drive file, or exports a Google Doc, Sheet or Slides file, through `gog drive download`.
func (r *Runner) GogDriveExport(ctx context.Context, id string, in GogDriveRequest) (GogFile, error) {
	if !gogIDPattern.MatchString(in.FileID) {
		return GogFile{}, fileErr("invalid_arguments", "file_id must be a Drive file id from a Drive search.")
	}
	format := strings.ToLower(strings.TrimSpace(in.Format))
	if format != "" && !gogExportFormats[format] {
		return GogFile{}, fileErr("unsupported_format", "format is one of pdf, csv, xlsx, pptx, txt, docx, md, html, png; leave it empty for the default of the file type.")
	}
	return r.gogFetchFile(ctx, id, "drive", func(account gogAccount, out string) []string {
		args := []string{"--json", "--no-input", "--readonly", "--account", account.Email, "drive", "download", "--out", out}
		if format != "" {
			args = append(args, "--format", format)
		}
		return append(args, "--", in.FileID)
	}, "drive-file")
}
