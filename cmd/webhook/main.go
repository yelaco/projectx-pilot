// webhook is an opt-in ingress-only service. No ProjectX credentials are read.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"projectxbot/internal/storage"
	"projectxbot/internal/webhook"
)

func main() {
	if err := run(); err != nil {
		slog.Error("webhook stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	enabled := flag.Bool("enabled", false, "explicitly enable inbox-only webhook ingress")
	listen := flag.String("listen", "127.0.0.1:8090", "local receiver address (tunnel this port only)")
	state := flag.String("state", "var", "same private state directory used by bot")
	age := flag.Duration("max-age", 5*time.Minute, "maximum incoming signal age (1s..15m)")
	managed := flag.String("managed-config", "", "admin-managed ingress settings file")
	flag.Parse()
	if !*enabled {
		return errors.New("webhook is disabled; use -enabled to opt into inbox-only ingress")
	}
	host, port, err := net.SplitHostPort(*listen)
	if err != nil || host != "127.0.0.1" || port == "" {
		return errors.New("receiver must listen on 127.0.0.1")
	}
	if *managed == "" && len(os.Getenv("WEBHOOK_DELIVERY_TOKEN")) < 32 {
		return errors.New("WEBHOOK_DELIVERY_TOKEN must be a separate random token of at least 32 characters")
	}
	if err = os.MkdirAll(*state, 0700); err != nil {
		return err
	}
	if err = os.Chmod(*state, 0700); err != nil {
		return err
	}
	store, err := storage.Open(filepath.Join(*state, "remote-alerts.db"))
	if err != nil {
		return err
	}
	defer store.Close()
	var handler http.Handler
	if *managed != "" {
		handler, err = webhook.Managed(store, *managed)
	} else {
		handler, err = webhook.New(store, webhook.Options{Enabled: true, Token: os.Getenv("WEBHOOK_DELIVERY_TOKEN"), MaxAge: *age})
	}
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2500 * time.Millisecond, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 8192}
	errs := make(chan error, 1)
	go func() { errs <- srv.Serve(listener) }()
	slog.Info("inbox-only webhook listening", "listen", *listen, "execution_enabled", false)
	select {
	case <-ctx.Done():
	case err = <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	}
	stop, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	if shutdownErr := srv.Shutdown(stop); shutdownErr != nil {
		_ = srv.Close()
		if err == nil {
			err = shutdownErr
		}
	}
	return err
}
