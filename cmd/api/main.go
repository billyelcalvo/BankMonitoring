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

	"bankmonitoring/internal/config"
	"bankmonitoring/internal/httpapi"
	"bankmonitoring/internal/repository"
	"bankmonitoring/internal/seed"
	"bankmonitoring/internal/service/account"
	"bankmonitoring/internal/service/auth"
	"bankmonitoring/internal/service/transfer"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	if err := run(cfg, logger); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(cfg config.Config, logger *slog.Logger) error {
	tokens, err := auth.NewTokenService(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.RunMigrations {
		version, err := repository.Migrate(cfg.DatabaseURL)
		if err != nil {
			return err
		}
		logger.Info("database schema up to date", "version", version)
	}

	pool, err := repository.NewPool(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return err
	}
	defer pool.Close()

	if cfg.SeedDemoData {
		if err := seed.Demo(ctx, pool, cfg.DemoPassword); err != nil {
			return err
		}
		logger.Info("demo data ready")
	}

	transfers := repository.NewTransferRepository(pool)
	handler := httpapi.NewHandler(httpapi.Dependencies{
		Tokens:         tokens,
		Login:          auth.NewLoginService(repository.NewUserRepository(pool), tokens),
		Transfers:      transfer.NewService(repository.NewStore(pool), transfers),
		Accounts:       account.NewService(repository.NewAccountRepository(pool)),
		Ready:          pool.Ping,
		Logger:         logger,
		RequestTimeout: cfg.RequestTimeout,
	})

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("starting server", "addr", cfg.HTTPAddr)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		return err
	case <-ctx.Done():
		stop()
		logger.Info("shutting down server")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		return err
	}

	if err := <-serverErrors; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
