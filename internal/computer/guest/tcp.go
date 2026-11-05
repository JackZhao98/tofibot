package guest

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// ListenSpec is the validated form of the --listen and --token-file flags.
type ListenSpec struct {
	// Addr is empty for the default AF_VSOCK listener.
	Addr      string
	TokenFile string
}

// TCP reports whether the guest serves HTTP on TCP (bearer-token protected).
func (l ListenSpec) TCP() bool { return l.Addr != "" }

// ParseListen validates the listener flags. "vsock" (or empty) keeps the VM
// path and ignores nothing: a token file with vsock is refused so a stray flag
// cannot suggest protection that is not there.
func ParseListen(listen, tokenFile string) (ListenSpec, error) {
	listen = strings.TrimSpace(listen)
	tokenFile = strings.TrimSpace(tokenFile)
	if listen == "" || listen == "vsock" {
		if tokenFile != "" {
			return ListenSpec{}, errors.New("--token-file is only valid with --listen tcp://HOST:PORT")
		}
		return ListenSpec{}, nil
	}
	rest, ok := strings.CutPrefix(listen, "tcp://")
	if !ok {
		return ListenSpec{}, fmt.Errorf("--listen must be \"vsock\" or \"tcp://HOST:PORT\" (got %q)", listen)
	}
	if _, port, err := net.SplitHostPort(rest); err != nil || port == "" {
		return ListenSpec{}, fmt.Errorf("--listen %q needs HOST:PORT", listen)
	}
	if tokenFile == "" {
		return ListenSpec{}, errors.New("--token-file is required with --listen tcp://")
	}
	return ListenSpec{Addr: rest, TokenFile: tokenFile}, nil
}

// TokenAuth guards every route except GET /health with a bearer token read
// from tokenFile on each request, so the server can write or rewrite the file
// after the process has started.
func TokenAuth(next http.Handler, tokenFile string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		raw, err := os.ReadFile(tokenFile)
		token := strings.TrimSpace(string(raw))
		if err != nil || token == "" {
			writeAuthJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "guest token not provisioned", "code": "token_missing"})
			return
		}
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), []byte(token)) != 1 {
			writeAuthJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeAuthJSON(w http.ResponseWriter, status int, body map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// ServeTCP serves the Service behind TokenAuth on addr until ctx ends. There is
// no write timeout: /v1/desktop/stream and long shell actions hold the
// response open.
func (s *Service) ServeTCP(ctx context.Context, addr, tokenFile string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.serveTCPOn(ctx, ln, tokenFile)
}

func (s *Service) serveTCPOn(ctx context.Context, ln net.Listener, tokenFile string) error {
	srv := &http.Server{
		Handler:           TokenAuth(s.Handler(), tokenFile),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
		_ = s.Close(context.Background())
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return ctx.Err()
}
