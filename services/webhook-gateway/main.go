// webhook-gateway terminates GitHub App webhooks: verify HMAC, then publish the
// raw payload to Kafka and 202 immediately. No business logic on the hot path.
package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dev-intel/platform/libs/go/config"
	"github.com/dev-intel/platform/libs/go/events"
	"github.com/dev-intel/platform/libs/go/kafka"
	"github.com/dev-intel/platform/libs/go/observability"
)

func main() {
	log := observability.Logger("webhook-gateway")

	brokers := config.List("KAFKA_BROKERS", "localhost:9092")
	secret := config.String("GITHUB_WEBHOOK_SECRET", "dev-secret")
	addr := config.String("LISTEN_ADDR", ":8080")

	// SIGTERM/SIGINT cancels ctx → triggers graceful drain below.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	writer := kafka.NewWriter(brokers, events.TopicRawGitHub)
	defer writer.Close()

	srv := &server{log: log, secret: []byte(secret), writer: writer}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/webhooks/github", srv.handleGitHub)

	log.Info("listening", "addr", addr, "brokers", brokers)
	httpSrv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	// Serve in the background so main can wait on the signal context.
	errCh := make(chan error, 1)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		log.Error("server exited", "err", err)
		os.Exit(1)
	case <-ctx.Done():
		log.Info("shutdown signal received; draining")
		// Stop accepting new connections and let in-flight requests finish.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			log.Error("graceful shutdown failed", "err", err)
			os.Exit(1)
		}
		log.Info("drained; exiting")
	}
}

type server struct {
	log    *slog.Logger
	secret []byte
	writer *kafka.Writer
}

func (s *server) handleGitHub(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20)) // 5 MiB cap
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	if !validSignature(s.secret, r.Header.Get("X-Hub-Signature-256"), body) {
		s.log.Warn("invalid signature", "delivery", r.Header.Get("X-GitHub-Delivery"))
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	delivery := r.Header.Get("X-GitHub-Delivery")
	eventType := r.Header.Get("X-GitHub-Event")
	traceID := observability.NewTraceID()

	headers := map[string]string{
		events.HeaderTraceID:     traceID,
		events.HeaderGitHubEvent: eventType,
		events.HeaderGitHubDeliv: delivery,
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Key by delivery id → idempotent, ordered per delivery.
	if err := s.writer.Write(ctx, delivery, body, headers); err != nil {
		s.log.Error("kafka write failed", "err", err, "delivery", delivery, "trace_id", traceID)
		http.Error(w, "enqueue failed", http.StatusServiceUnavailable) // 503 → GitHub retries
		return
	}

	s.log.Info("accepted", "event", eventType, "delivery", delivery, "trace_id", traceID)
	w.WriteHeader(http.StatusAccepted)
}

// validSignature verifies the GitHub HMAC-SHA256 signature header
// ("sha256=...") in constant time.
func validSignature(secret []byte, header string, body []byte) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	want, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}
