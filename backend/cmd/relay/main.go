package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/config"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/jetstream"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/logging"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/observability"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/outbox"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "devmaster-relay failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "path to YAML configuration file")
	flag.Parse()

	cfg, err := config.Load(firstNonEmpty(*configPath, os.Getenv("DEVMASTER_CONFIG_FILE")))
	if err != nil {
		return err
	}
	logger := logging.New(cfg.Logging)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	startupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	db, err := database.Open(startupCtx, cfg.Database, logger)
	if err != nil {
		return err
	}
	defer db.Close()

	client, err := jetstream.Connect(startupCtx, cfg.NATS)
	if err != nil {
		return err
	}
	defer client.Close()

	metrics := observability.NewMetrics()
	dispatcher := outbox.NewDispatcher(db, outbox.NewRepository(), client, metrics, logger, cfg.Outbox)
	logger.Info("relay started", slog.String("stream", cfg.NATS.Stream))
	if err := dispatcher.Run(ctx); err != nil && ctx.Err() == nil {
		return err
	}
	logger.Info("relay stopped")
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
