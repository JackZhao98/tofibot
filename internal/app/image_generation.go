package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
)

func (s *Server) imageGenerationTools(c Conversation, r Run) []Tool {
	if s.codex == nil {
		return nil
	}
	return []Tool{s.imageGenerationTool(c, r, func(ctx context.Context, input provider.ImageRequest) ([]byte, error) {
		credential, err := s.codex.CredentialReadOnly(ctx)
		if err != nil {
			return nil, err
		}
		return provider.GenerateCodexImage(ctx, credential, input)
	})}
}

func (s *Server) imageGenerationTool(c Conversation, r Run, generate func(context.Context, provider.ImageRequest) ([]byte, error)) Tool {
	var mu sync.Mutex
	return Tool{Name: "generate_image", Description: "Generate an image, or edit a referenced image attachment from this conversation, using the connected Codex image service. The resulting PNG is automatically attached to your final reply; do not publish it again. Use only for requested image work. May take several minutes and consumes the connected account's image allowance. Identical requests in the same run reuse the result. Never claim success if the tool returns an error.", Parameters: objectSchema(map[string]any{
		"prompt":                  map[string]any{"type": "string", "description": "A complete image brief, or precise editing instructions."},
		"reference_attachment_id": map[string]any{"type": "string", "description": "Optional image attachment ID from this conversation to edit. Omit to create a new image."},
		"name":                    map[string]any{"type": "string", "description": "Optional output filename; .png is appended if absent."},
	}, []string{"prompt"}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var in struct {
			Prompt    string `json:"prompt"`
			Reference string `json:"reference_attachment_id"`
			Name      string `json:"name"`
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF || strings.TrimSpace(in.Prompt) == "" || len(in.Prompt) > 12000 {
			return "", errors.New("generate_image requires a nonempty prompt up to 12000 bytes")
		}
		name := filepath.Base(strings.TrimSpace(in.Name))
		if name == "." || name == "" {
			name = "generated-image.png"
		}
		if !strings.HasSuffix(strings.ToLower(name), ".png") {
			name += ".png"
		}
		if len(name) > 180 {
			return "", errors.New("image filename is too long")
		}
		input := provider.ImageRequest{Prompt: in.Prompt}
		if in.Reference != "" {
			var err error
			input.Reference, err = s.store.attachmentImage(in.Reference, c.ID)
			if err != nil {
				return "", err
			}
		}
		sig, _ := json.Marshal([]string{in.Prompt, in.Reference, name})
		digest := sha256.Sum256(sig)
		key := "image:" + hex.EncodeToString(digest[:])
		var id string
		err := s.store.db.QueryRow(`SELECT attachment_id FROM run_attachments WHERE run_id=? AND request_key=? LIMIT 1`, r.ID, key).Scan(&id)
		if err == nil {
			a, _, e := s.store.Attachment(id)
			if e != nil {
				return "", e
			}
			return imageAttachmentResult(a), nil
		}
		if err != sql.ErrNoRows {
			return "", err
		}
		var stagedCount int
		if err = s.store.db.QueryRow(`SELECT COUNT(*) FROM run_attachments WHERE run_id=?`, r.ID).Scan(&stagedCount); err != nil {
			return "", err
		}
		if stagedCount >= 8 {
			return "", errors.New("at most 8 published files are allowed per run")
		}
		current, err := s.store.GetRun(r.ID)
		if err != nil || current.Status != "running" || current.ConversationID != c.ID || current.BotID != r.BotID {
			return "", errors.New("image generation requires an active run")
		}
		ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		data, err := generate(ctx, input)
		if err != nil {
			return "", err
		}
		if err = ctx.Err(); err != nil {
			return "", err
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || format != "png" || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 16_777_216 || int64(len(data)) > maxAttachmentSize {
			return "", errors.New("image provider returned an invalid or oversized PNG")
		}
		if _, _, err = image.Decode(bytes.NewReader(data)); err != nil {
			return "", errors.New("image provider returned a damaged PNG")
		}
		a, err := s.store.AddAttachment(c.ID, name, "image/png", bytes.NewReader(data))
		if err != nil {
			return "", err
		}
		staged, err := s.store.stageRunAttachment(r, c, a, key)
		if err != nil {
			_ = s.store.deleteUnboundAttachment(a.ID)
			return "", err
		}
		return imageAttachmentResult(staged), nil
	}}
}

func imageAttachmentResult(a Attachment) string {
	data, _ := json.Marshal(map[string]any{"attachment_id": a.ID, "name": a.Name, "mime": a.MIME, "size": a.Size, "url": "/api/attachments/" + a.ID, "delivery": "attached to final reply"})
	return string(data)
}
