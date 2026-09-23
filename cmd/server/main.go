package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/okottawar/jobqueue/internal/api"
	"github.com/okottawar/jobqueue/internal/config"
	"github.com/okottawar/jobqueue/internal/db"
	"github.com/okottawar/jobqueue/internal/job"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	if err := run(); err != nil {
		slog.Error("fatal error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	migrationSQL, err := os.ReadFile(migrationPath())
	if err != nil {
		return err
	}
	if err := db.Migrate(ctx, pool, string(migrationSQL)); err != nil {
		return err
	}
	slog.Info("database migrated")

	store := db.NewStore(pool)

	if n, err := store.ResetStuckProcessing(ctx); err != nil {
		slog.Error("failed to reset stuck jobs", "error", err)
	} else if n > 0 {
		slog.Info("reset stuck processing jobs from previous run", "count", n)
	}

	registry := job.NewRegistry()

	workerPool := job.NewPool(job.PoolConfig{
		WorkerCount:  cfg.WorkerCount,
		QueueSize:    cfg.QueueSize,
		PollInterval: cfg.PollInterval,
		BaseBackoff:  cfg.BaseBackoff,
		MaxBackoff:   cfg.MaxBackoff,
	}, store, registry)

	server := api.NewServer(store, registry, workerPool)

	mux := http.NewServeMux()
	server.Routes(mux, staticDir())
	handler := api.CORSMiddleware(api.LoggingMiddleware(api.WriteAuthMiddleware(cfg.AuthUsername, cfg.AuthPassword, mux)))

	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Run worker pool until ctx is cancelled (signal received).
	poolDone := make(chan struct{})
	go func() {
		workerPool.Run(ctx)
		close(poolDone)
	}()

	// Run HTTP server, shutting down gracefully when ctx is cancelled.
	serverErrCh := make(chan error, 1)
	go func() {
		slog.Info("starting server", "port", cfg.Port, "workers", cfg.WorkerCount)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrCh <- err
		}
		close(serverErrCh)
	}()

	select {
	case <-ctx.Done():
		slog.Info("shutdown signal received")
	case err := <-serverErrCh:
		if err != nil {
			return err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("error during HTTP shutdown", "error", err)
	}

	// Wait for the worker pool to finish in-flight jobs, bounded by the same
	// grace period.
	select {
	case <-poolDone:
	case <-shutdownCtx.Done():
		slog.Warn("worker pool did not stop within shutdown grace period")
	}

	slog.Info("shutdown complete")
	return nil
}

func migrationPath() string {
	if p := os.Getenv("MIGRATIONS_FILE"); p != "" {
		return p
	}
	return "migrations/001_init.sql"
}

func staticDir() string {
	if p := os.Getenv("STATIC_DIR"); p != "" {
		return p
	}
	return "web/static"
}
