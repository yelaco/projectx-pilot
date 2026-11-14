package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"projectxbot/internal/admin"
	"projectxbot/internal/app"
	"projectxbot/internal/config"
	"projectxbot/internal/dashboard"
	"projectxbot/internal/observability"
	"projectxbot/internal/storage"
	"projectxbot/internal/tunnel"
)

func main() {
	if err := run(); err != nil {
		slog.Error("bot stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	path := flag.String("config", "config/example.yaml", "YAML configuration path")
	stateDir := flag.String("state", "var", "private local administration state directory")
	flag.Parse()
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	var level slog.Level
	_ = level.UnmarshalText([]byte(c.LogLevel))
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)
	manager, token, err := admin.Open(c, *path, *stateDir)
	if err != nil {
		return err
	}
	defer manager.Close()
	if err := manager.AcquireExecutionLease(); err != nil {
		return err
	}
	audit, err := storage.Open(filepath.Join(*stateDir, "activity.db"))
	if err != nil {
		return err
	}
	defer audit.Close()
	log = slog.New(observability.Handler{Console: log.Handler(), Store: audit})
	slog.SetDefault(log)

	client, err := manager.Reader()
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	tunnelController := tunnel.New(manager.RoutingStore(), audit)
	manager.AttachTunnelWake(tunnelController.Wake)
	manager.AttachTunnelStatus(tunnelController.Status)
	tunnelDone := make(chan struct{})
	go func() { defer close(tunnelDone); tunnelController.Run(ctx) }()
	defer func() { cancel(); <-tunnelDone }()

	newsDone := make(chan struct{})
	go func() { defer close(newsDone); manager.RunNews(ctx) }()
	defer func() { cancel(); <-newsDone }()

	executionDone := make(chan struct{})
	go func() { defer close(executionDone); manager.RunExecution(ctx) }()
	defer func() { cancel(); <-executionDone }()
	automationDone := make(chan struct{})
	go func() { defer close(automationDone); manager.RunAutomation(ctx) }()
	defer func() { cancel(); <-automationDone }()
	service := app.New(client, c.AccountID)
	manager.Attach(service)
	srv := &http.Server{Addr: c.DashboardListen, Handler: dashboard.AdminHandler(service, token, c.DashboardListen, manager), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 45 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	done := make(chan struct{})
	go func() { defer close(done); service.Run(ctx, c.RefreshInterval, log) }()
	failures := make(chan error, 1)
	go func() { failures <- srv.ListenAndServe() }()
	log.Info("local trading manager started", "listen", c.DashboardListen, "execution_enabled", false, "password_file", *stateDir+"/admin-password")
	select {
	case <-ctx.Done():
	case err = <-failures:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	}
	cancel()
	stopCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	shutdownErr := srv.Shutdown(stopCtx)
	<-done
	if err != nil {
		return err
	}
	return shutdownErr
}
