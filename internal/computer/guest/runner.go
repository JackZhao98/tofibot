package guest

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/mcprunner"
)

func (s *Service) handleRunner(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/runner")
	if r.URL.RawQuery != "" || r.URL.RawPath != "" || strings.Contains(path, "..") || (!strings.HasPrefix(path, "/v1/plugins") && !strings.HasPrefix(path, "/mcp/")) {
		writeError(w, http.StatusNotFound, "Runner endpoint not found")
		return
	}
	s.runnerMu.Lock()
	if s.runnerClosed {
		s.runnerMu.Unlock()
		writeError(w, 503, "Runner stopped")
		return
	}
	if s.runner == nil {
		dir := filepath.Join(s.root, "shared", ".tofi", "runner")
		specs, err := mcprunner.LoadRecords(filepath.Join(dir, "manifest.json"))
		var runner *mcprunner.Runner
		if err == nil {
			runner, err = mcprunner.New(specs, 10*time.Minute)
		}
		if err == nil {
			err = runner.SetStateDir(dir)
		}
		secret := make([]byte, 32)
		if err == nil {
			_, err = rand.Read(secret)
		}
		if err != nil {
			if runner != nil {
				runner.Close()
			}
			s.runnerMu.Unlock()
			writeError(w, 503, "account Runner unavailable")
			return
		}
		s.runner = runner
		s.runnerToken = hex.EncodeToString(secret)
		s.runnerHandler = runner.Handler(s.runnerToken)
	}
	handler, token := s.runnerHandler, s.runnerToken
	s.runnerMu.Unlock()
	req := r.Clone(r.Context())
	req.URL.Path = path
	req.Header.Set("Authorization", "Bearer "+token)
	req.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	handler.ServeHTTP(w, req)
}

func (s *Service) closeRunner() {
	s.runnerMu.Lock()
	s.runnerClosed = true
	runner := s.runner
	s.runnerMu.Unlock()
	if runner != nil {
		runner.Close()
	}
}
