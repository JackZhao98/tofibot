package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/JackZhao98/tofibot/internal/app"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

func main() {
	tlsConfig, err := serverTLSConfig(os.Getenv("TOFI_TLS_CERT_FILE"), os.Getenv("TOFI_TLS_KEY_FILE"))
	if err != nil {
		log.Printf("TLS configuration unavailable: %v", err)
		return
	}
	var engine runtime.Engine
	provider, model, key := os.Getenv("TOFI_MODEL_PROVIDER"), os.Getenv("TOFI_MODEL"), os.Getenv("TOFI_MODEL_API_KEY")
	if provider != "" && !strings.EqualFold(provider, "openai_codex") {
		var err error
		engine, err = runtime.New(runtime.Config{Provider: provider, Model: model, APIKey: key, BaseURL: os.Getenv("TOFI_MODEL_BASE_URL")})
		if err != nil {
			log.Printf("model unavailable: %v", err)
			engine = nil
		}
	}
	config := app.Config{Engine: engine, DataDir: os.Getenv("TOFI_DATA_DIR"), UIDir: os.Getenv("TOFI_UI_DIR"), Listen: os.Getenv("TOFI_LISTEN"), OwnerAuth: os.Getenv("TOFI_OWNER_AUTH") == "1", OwnerAllowLoopbackHTTP: os.Getenv("TOFI_OWNER_ALLOW_LOOPBACK_HTTP") == "1", OwnerAllowLANHTTP: os.Getenv("TOFI_OWNER_ALLOW_LAN_HTTP") == "1", DefaultModel: model, Provider: provider, TranscriptionAPIKey: os.Getenv("TOFI_TRANSCRIPTION_API_KEY"), TranscriptionURL: os.Getenv("TOFI_TRANSCRIPTION_URL"), ComputerSocket: os.Getenv("TOFI_COMPUTER_SOCKET")}
	s, err := newConfiguredServer(config, os.Getenv)
	if err != nil {
		log.Printf("server unavailable: %v", err)
		return
	}
	defer s.Close()
	log.Printf("tofi listening on %s", s.Listen())

	httpServer := &http.Server{Addr: s.Listen(), Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, TLSConfig: tlsConfig}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveErr := make(chan error, 1)
	go func() {
		if tlsConfig != nil {
			serveErr <- httpServer.ListenAndServeTLS("", "")
		} else {
			serveErr <- httpServer.ListenAndServe()
		}
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := httpServer.Shutdown(shutdownCtx)
		cancel()
		if err != nil {
			log.Printf("HTTP graceful shutdown timed out: %v", err)
			// Shutdown leaves active connections (including SSE) open when its
			// deadline expires; force-close them before closing the app store.
			if closeErr := httpServer.Close(); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
				log.Printf("HTTP forced shutdown: %v", closeErr)
			}
		}
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("HTTP server stopped: %v", err)
		}
	}
}

// TLS is configured only by the service operator, never by request headers.
func serverTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	if certFile == "" && keyFile == "" {
		return nil, nil
	}
	if certFile == "" || keyFile == "" {
		return nil, errors.New("TOFI_TLS_CERT_FILE and TOFI_TLS_KEY_FILE must be configured together")
	}
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load configured certificate and key: %w", err)
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}, nil
}
