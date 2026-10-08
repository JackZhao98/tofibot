package app

import (
	"context"
	"net/http"
	"strings"
	"time"
)

const (
	codexCheckOK           = "ok"
	codexCheckRejected     = "rejected"
	codexCheckUnverified   = "unverified"
	codexCheckNotConnected = "not_connected"
	codexVerifyFreshFor    = time.Minute
)

// probeCodexCredential asks the provider whether the credential is accepted. It
// returns the HTTP status, or an error when the provider could not be reached.
var probeCodexCredential = func(ctx context.Context, credential string) (int, error) {
	parts := strings.SplitN(credential, "\x00", 2)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://chatgpt.com/backend-api/codex/models?client_version=0.155.0", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+parts[0])
	req.Header.Set("originator", "tofi")
	req.Header.Set("User-Agent", "tofi/1.0")
	if len(parts) == 2 && parts[1] != "" {
		req.Header.Set("ChatGPT-Account-Id", parts[1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

// verifyCodexSignIn checks the stored sign-in against the provider so settings
// never report "connected" for a credential the provider no longer accepts.
// A rejection gets one refresh; only an unrecoverable one marks a reconnect.
func (s *Server) verifyCodexSignIn(ctx context.Context) string {
	if s.codex == nil || !s.codex.Status().Connected {
		return codexCheckNotConnected
	}
	s.codexVerifyMu.Lock()
	defer s.codexVerifyMu.Unlock()
	if s.codexVerifyCheck != "" && time.Since(s.codexVerifyAt) < codexVerifyFreshFor {
		return s.codexVerifyCheck
	}
	probe := func() (int, error) {
		credential, err := s.codex.Credential(ctx)
		if err != nil {
			if code, _ := modelAccountFailure(strings.ToLower(err.Error()), ""); code == "model_auth_invalid" {
				return http.StatusUnauthorized, nil
			}
			return 0, err
		}
		return probeCodexCredential(ctx, credential)
	}
	status, err := probe()
	if err == nil && (status == http.StatusUnauthorized || status == http.StatusForbidden) {
		rejected, recoverErr := s.codex.RecoverRejected(ctx)
		if recoverErr != nil {
			return codexCheckUnverified
		}
		if rejected {
			_, _ = s.store.WorkspaceEvent(workspaceScopeConfig)
		}
		if !s.codex.Status().Connected {
			s.codexVerifyCheck = ""
			return codexCheckRejected
		}
		status, err = probe()
	}
	check := codexCheckUnverified
	if err == nil && status >= 200 && status < 300 {
		check = codexCheckOK
	}
	s.codexVerifyCheck, s.codexVerifyAt = check, time.Now()
	return check
}

func (s *Server) forgetCodexVerification() {
	s.codexVerifyMu.Lock()
	s.codexVerifyCheck, s.codexVerifyAt = "", time.Time{}
	s.codexVerifyMu.Unlock()
}
