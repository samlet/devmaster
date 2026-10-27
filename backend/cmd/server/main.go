package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/auth"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/config"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/logging"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/observability"
	productpostgres "github.com/xiaofeiwu/devmaster/backend/internal/product/adapter/postgres"
	"github.com/xiaofeiwu/devmaster/backend/internal/product/application"
	grpctransport "github.com/xiaofeiwu/devmaster/backend/internal/transport/grpc"
	httptransport "github.com/xiaofeiwu/devmaster/backend/internal/transport/http"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "devmaster-server failed: %v\n", err)
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

	authenticator, err := auth.New(startupCtx, cfg.Auth)
	if err != nil {
		return err
	}

	metrics := observability.NewMetrics()
	productService := application.NewService(productpostgres.NewRepository(db))

	grpcServer := grpctransport.NewServer(
		authenticator, metrics, logger,
		grpctransport.NewProductService(productService, db, metrics, logger),
		grpctransport.NewSystemService(),
	)
	grpcListener, err := net.Listen("tcp", cfg.Server.GRPCAddress)
	if err != nil {
		return fmt.Errorf("listen gRPC %s: %w", cfg.Server.GRPCAddress, err)
	}

	defaultOrganizationID := uuid.Nil
	if !cfg.Auth.Enabled {
		defaultOrganizationID, _ = uuid.Parse(cfg.Auth.DemoOrganizationID)
	}
	health := observability.Health{
		Timeout: cfg.Server.HealthTimeout,
		Components: []observability.Component{
			{Name: "postgres", Check: db.Ping},
			{Name: "nats", Check: observability.TCPCheck(hostPort(cfg.NATS.URL))},
			{Name: "temporal", Check: observability.TCPCheck(cfg.Temporal.HostPort)},
		},
	}
	httpServer := &http.Server{
		Addr:    cfg.Server.HTTPAddress,
		Handler: httptransport.NewServer(productService, metrics, health, logger, defaultOrganizationID).Handler(),
	}

	errCh := make(chan error, 2)
	go func() {
		logger.Info("gRPC listening", slog.String("address", cfg.Server.GRPCAddress))
		if err := grpcServer.Serve(grpcListener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			errCh <- fmt.Errorf("serve gRPC: %w", err)
		}
	}()
	go func() {
		logger.Info("HTTP listening", slog.String("address", cfg.Server.HTTPAddress))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("serve HTTP: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown requested")
	case err := <-errCh:
		return err
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer shutdownCancel()
	grpcServer.GracefulStop()
	_ = httpServer.Shutdown(shutdownCtx)
	return nil
}

func hostPort(natsURL string) string {
	// nats://127.0.0.1:4222 → 127.0.0.1:4222
	if idx := len("nats://"); len(natsURL) > idx && natsURL[:idx] == "nats://" {
		return natsURL[idx:]
	}
	return natsURL
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
