package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultDictationModel   = "gpt-4o-mini-transcribe"
	defaultTranscriptionURL = "https://api.openai.com/v1/audio/transcriptions"
	maxDictationBytes       = 25 << 20
)

type dictationModelOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Cost        string `json:"cost"`
}

var dictationModels = []dictationModelOption{
	{ID: "gpt-4o-mini-transcribe", Name: "GPT-4o mini Transcribe", Description: "更便宜，适合日常 Dictate。", Cost: "$0.003 / 分钟"},
	{ID: "gpt-4o-transcribe", Name: "GPT-4o Transcribe", Description: "识别质量更高，成本更高。", Cost: "$0.006 / 分钟"},
}

func migrateDictationSettings(db *sql.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS dictation_settings(
id INTEGER PRIMARY KEY CHECK(id=1),
model TEXT NOT NULL,
updated_at TEXT NOT NULL
)`)
	return err
}

func validDictationModel(model string) bool {
	for _, option := range dictationModels {
		if option.ID == model {
			return true
		}
	}
	return false
}

func (s *Store) dictationModel() (string, error) {
	var model string
	err := s.db.QueryRow(`SELECT model FROM dictation_settings WHERE id=1`).Scan(&model)
	if err == sql.ErrNoRows {
		return defaultDictationModel, nil
	}
	if err != nil {
		return "", err
	}
	if !validDictationModel(model) {
		return defaultDictationModel, nil
	}
	return model, nil
}

func (s *Store) putDictationModel(model string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO dictation_settings(id,model,updated_at) VALUES(1,?,?)
ON CONFLICT(id) DO UPDATE SET model=excluded.model,updated_at=excluded.updated_at`, model, now()); err != nil {
		return err
	}
	if err = insertWorkspaceEventTx(tx, workspaceScopeConfig, now()); err != nil {
		return err
	}
	return tx.Commit()
}

// Codex credentials may only be sent to the verified OpenAI endpoint. Custom
// gateways must use an explicitly configured transcription API key.
func (s *Server) dictationAuthSource() string {
	if strings.TrimSpace(s.transcriptionAPIKey) != "" {
		return "api_key"
	}
	if s.transcriptionURL == defaultTranscriptionURL && s.codex != nil && s.codex.Status().Connected {
		return "codex"
	}
	return ""
}

func (s *Server) dictationCredential(ctx context.Context) (string, error) {
	if strings.TrimSpace(s.transcriptionAPIKey) != "" {
		return strings.TrimSpace(s.transcriptionAPIKey), nil
	}
	if s.transcriptionURL != defaultTranscriptionURL || s.codex == nil {
		return "", errors.New("dictation authentication unavailable")
	}
	credential, err := s.codex.Credential(ctx)
	if err != nil {
		return "", errors.New("dictation authentication unavailable")
	}
	token, _, _ := strings.Cut(credential, "\x00")
	if strings.TrimSpace(token) == "" {
		return "", errors.New("dictation authentication unavailable")
	}
	return token, nil
}

func (s *Server) dictationSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		model, err := s.store.dictationModel()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "dictation_settings_unavailable", "dictation settings unavailable")
			return
		}
		authSource := s.dictationAuthSource()
		writeJSON(w, http.StatusOK, map[string]any{
			"model":       model,
			"configured":  authSource != "",
			"auth_source": authSource,
			"models":      dictationModels,
		})
	case http.MethodPut:
		var body struct {
			Model string `json:"model"`
		}
		if err := decode(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", "invalid dictation settings")
			return
		}
		body.Model = strings.TrimSpace(body.Model)
		if !validDictationModel(body.Model) {
			writeErr(w, http.StatusBadRequest, "invalid_dictation_model", "unsupported dictation model")
			return
		}
		if err := s.store.putDictationModel(body.Model); err != nil {
			writeErr(w, http.StatusInternalServerError, "dictation_settings_unavailable", "dictation settings unavailable")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"model": body.Model})
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or PUT")
	}
}

func (s *Server) dictate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return
	}
	if s.dictationAuthSource() == "" {
		writeErr(w, http.StatusServiceUnavailable, "dictation_unconfigured", "请连接 Codex，或配置服务端转写 API key。")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxDictationBytes+1<<20)
	if err := r.ParseMultipartForm(maxDictationBytes + 1<<20); err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "audio_too_large", "audio file is too large")
		return
	}
	defer r.MultipartForm.RemoveAll()
	model := strings.TrimSpace(r.FormValue("model"))
	if !validDictationModel(model) {
		model, _ = s.store.dictationModel()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "audio_required", "audio file is required")
		return
	}
	defer file.Close()
	audio, err := io.ReadAll(io.LimitReader(file, maxDictationBytes+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "audio_read_failed", "audio file could not be read")
		return
	}
	if len(audio) > maxDictationBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "audio_too_large", "audio file is too large")
		return
	}
	apiKey, err := s.dictationCredential(r.Context())
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "dictation_auth_unavailable", "转写认证暂不可用，请重试或重新连接 Codex。")
		return
	}
	text, err := requestTranscription(r.Context(), s.transcriptionURL, apiKey, model, audio, header)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "dictation_provider_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": text, "model": model})
}

func requestTranscription(ctx context.Context, endpoint, apiKey, model string, audio []byte, header *multipart.FileHeader) (string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	filename := filepath.Base(header.Filename)
	if filename == "." || filename == "" {
		filename = "dictation.webm"
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("create transcription upload: %w", err)
	}
	if _, err = part.Write(audio); err != nil {
		return "", fmt.Errorf("write transcription upload: %w", err)
	}
	if err = writer.WriteField("model", model); err != nil {
		return "", fmt.Errorf("write transcription model: %w", err)
	}
	if err = writer.WriteField("response_format", "json"); err != nil {
		return "", fmt.Errorf("write transcription format: %w", err)
	}
	if err = writer.Close(); err != nil {
		return "", fmt.Errorf("close transcription upload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return "", fmt.Errorf("create transcription request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	client := &http.Client{
		Timeout:       2 * time.Minute,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("transcription request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("transcription service returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&result); err != nil {
		return "", fmt.Errorf("decode transcription response: %w", err)
	}
	return strings.TrimSpace(result.Text), nil
}
