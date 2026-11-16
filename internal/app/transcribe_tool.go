package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// transcribeFormats are the file types the transcription service accepts.
var transcribeFormats = map[string]bool{".mp3": true, ".mp4": true, ".mpeg": true, ".mpga": true, ".m4a": true, ".wav": true, ".webm": true}

const transcribeFormatList = "mp3, mp4, mpeg, mpga, m4a, wav and webm"

// transcribeAudioTools registers transcribe_audio only for a run whose account
// has a transcription credential source (the one dictation uses).
func (s *Server) transcribeAudioTools(c Conversation, r Run) []Tool {
	if s.dictationAuthSource() == "" {
		return nil
	}
	return []Tool{{Name: "transcribe_audio", Description: "Turns speech in an audio or video file into text with the account's transcription service. The file is an attachment of this conversation or a path under /workspace on the Bot computer; the service accepts " + transcribeFormatList + " files up to 25 MiB.", Timeout: 150 * time.Second, Parameters: objectSchema(map[string]any{
		"attachment_id": map[string]any{"type": "string", "description": "ID of a file of this conversation"},
		"path":          map[string]any{"type": "string", "description": "Absolute path of a file on the Bot computer, under /workspace"},
		"language":      map[string]any{"type": "string", "description": "Optional ISO 639-1 code of the spoken language"},
	}, nil), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
		var in struct {
			AttachmentID string `json:"attachment_id"`
			Path         string `json:"path"`
			Language     string `json:"language"`
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if err := d.Decode(&in); err != nil {
			return "", tooloutcome.InvalidArguments("transcribe_audio takes attachment_id or path, and optionally language")
		}
		in.AttachmentID, in.Path, in.Language = strings.TrimSpace(in.AttachmentID), strings.TrimSpace(in.Path), strings.ToLower(strings.TrimSpace(in.Language))
		switch {
		case in.AttachmentID != "" && in.Path != "":
			return "", tooloutcome.InvalidArguments("Give attachment_id or path, not both.")
		case in.AttachmentID == "" && in.Path == "":
			return "", tooloutcome.InvalidArguments("One of attachment_id or path is required.")
		}
		if in.Language != "" && (len(in.Language) != 2 || strings.Trim(in.Language, "abcdefghijklmnopqrstuvwxyz") != "") {
			return "", tooloutcome.InvalidArguments("language must be an ISO 639-1 code such as en.")
		}
		var audio []byte
		var name, source string
		if in.AttachmentID != "" {
			a, disk, err := s.store.Attachment(in.AttachmentID)
			if err != nil {
				return "", tooloutcome.New(tooloutcome.Permanent, tooloutcome.FileNotFound, "not_executed", "No attachment with that attachment_id is readable in this conversation.", "repair_arguments").Err()
			}
			if ok, e := s.store.attachmentAvailableInConversation(in.AttachmentID, c.ID); e != nil || !ok {
				return "", tooloutcome.InvalidArguments("That attachment belongs to another conversation.")
			}
			name, source = a.Name, a.Name
			if err := checkTranscribable(name, a.Size); err != nil {
				return "", err
			}
			f, err := s.store.openAttachment(ctx, disk)
			if err != nil {
				return "", transcribeReadFailure()
			}
			defer f.Close()
			audio, err = io.ReadAll(io.LimitReader(f, maxDictationBytes+1))
			if err != nil {
				return "", transcribeReadFailure()
			}
			if len(audio) > maxDictationBytes {
				return "", checkTranscribable(name, int64(len(audio)))
			}
		} else {
			clean := path.Clean(in.Path)
			if !strings.HasPrefix(in.Path, "/") || clean != "/workspace" && !strings.HasPrefix(clean, "/workspace/") {
				return "", tooloutcome.InvalidArguments("path must be an absolute path under /workspace on the Bot computer.")
			}
			name, source = path.Base(clean), clean
			if err := checkTranscribable(name, 0); err != nil {
				return "", err
			}
			if s.computer() == nil {
				return "", tooloutcome.New(tooloutcome.Permanent, "computer_unavailable", "not_executed", "This account has no Bot computer; use attachment_id.", "explain_blocker").Err()
			}
			b, _, err := s.readGuestFile(ctx, r, clean, maxDictationBytes)
			if errors.Is(err, errGuestFileTooLarge) {
				return "", checkTranscribable(name, maxDictationBytes+1)
			}
			if err != nil {
				return "", err
			}
			audio = b
		}
		credential, err := s.dictationCredential(ctx)
		if err != nil {
			return "", tooloutcome.New(tooloutcome.Permanent, "transcription_unavailable", "not_executed", "The transcription service has no usable credential. Tell the person to check the dictation transcription setting in Settings.", "explain_blocker").Err()
		}
		model, _ := s.store.dictationModel()
		endpoint := s.transcriptionURL
		if endpoint == "" {
			endpoint = defaultTranscriptionURL
		}
		text, err := requestTranscriptionIn(ctx, endpoint, credential, model, in.Language, audio, &multipart.FileHeader{Filename: filepath.Base(name)})
		if err != nil {
			msg := strings.ReplaceAll(err.Error(), credential, "[redacted]")
			return "", tooloutcome.New(tooloutcome.Transient, "transcription_failed", "not_executed", "The transcription service failed: "+msg, "retry_later").Err()
		}
		out, _ := json.Marshal(map[string]string{"text": text, "model": model, "source": source})
		return string(out), nil
	}}}
}

func transcribeReadFailure() error {
	return tooloutcome.New(tooloutcome.Transient, "transcription_failed", "not_executed", "The file could not be read for transcription.", "retry_later").Err()
}

// checkTranscribable refuses a file the service cannot take: an unsupported
// type, or a size over the dictation limit (size 0 means not known yet).
func checkTranscribable(name string, size int64) error {
	ext := strings.ToLower(filepath.Ext(name))
	if !transcribeFormats[ext] {
		return tooloutcome.InvalidArguments(fmt.Sprintf("%q is not a type the transcription service accepts. It accepts %s files; extract or convert the audio on the Bot computer with ffmpeg first.", name, transcribeFormatList))
	}
	if size > maxDictationBytes {
		return tooloutcome.InvalidArguments(fmt.Sprintf("%q is larger than the limit of %d MiB for transcription. Cut the audio into shorter parts or re-encode it at a lower bitrate on the Bot computer (ffmpeg is installed), then transcribe the result.", name, maxDictationBytes>>20))
	}
	return nil
}
