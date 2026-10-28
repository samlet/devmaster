package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/approver"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/config"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/gitea"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/logging"
	versionpostgres "github.com/xiaofeiwu/devmaster/backend/internal/version/adapter/postgres"
	versionapplication "github.com/xiaofeiwu/devmaster/backend/internal/version/application"
	"github.com/xiaofeiwu/devmaster/backend/internal/workflow"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "devmaster-worker failed: %v\n", err)
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

	db, err := database.Open(context.Background(), cfg.Database, logger)
	if err != nil {
		return err
	}
	defer db.Close()

	temporalClient, err := client.Dial(client.Options{
		HostPort:  cfg.Temporal.HostPort,
		Namespace: cfg.Temporal.Namespace,
	})
	if err != nil {
		return fmt.Errorf("connect temporal %s: %w", cfg.Temporal.HostPort, err)
	}
	defer temporalClient.Close()

	versionService := versionapplication.NewService(versionpostgres.NewRepository(db))
	activities := &workflow.Activities{
		DB:       db,
		Versions: versionService,
		Approver: approver.New(cfg.Approver),
		Gitea:    gitea.New(cfg.Gitea),
		Logger:   logger,
	}
	workerInstance := worker.New(temporalClient, cfg.Temporal.TaskQueue, worker.Options{})
	workerInstance.RegisterWorkflow(workflow.PingWorkflow)
	workerInstance.RegisterWorkflow(workflow.ReleaseWorkflow)
	// 注册整个活动结构体：方法名（Ping）就是工作流里 ExecuteActivity 用的名字。
	workerInstance.RegisterActivity(activities)

	logger.Info("worker started",
		slog.String("namespace", cfg.Temporal.Namespace),
		slog.String("task_queue", cfg.Temporal.TaskQueue))
	if err := workerInstance.Run(worker.InterruptCh()); err != nil {
		return fmt.Errorf("run worker: %w", err)
	}
	logger.Info("worker stopped")
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
