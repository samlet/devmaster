// devmaster-snapshot 为组织内所有 ACTIVE 冲刺写当天燃尽快照。
//
// 建议由 cron 每天在固定时点跑一次（M2 用命令 + cron，M6 再挪进 Temporal 定时工作流）。
// 同一天重跑是覆盖，不会产生重复点。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/config"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/logging"
	sprintpostgres "github.com/xiaofeiwu/devmaster/backend/internal/sprint/adapter/postgres"
	sprintapp "github.com/xiaofeiwu/devmaster/backend/internal/sprint/application"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "devmaster-snapshot failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "path to YAML configuration file")
	organizationID := flag.String("organization-id", "", "organization to snapshot")
	flag.Parse()

	if *organizationID == "" {
		return fmt.Errorf("-organization-id is required（RLS 下不能跨组织扫描）")
	}
	orgID, err := uuid.Parse(*organizationID)
	if err != nil {
		return fmt.Errorf("organization-id: %w", err)
	}
	cfg, err := config.Load(firstNonEmpty(*configPath, os.Getenv("DEVMASTER_CONFIG_FILE")))
	if err != nil {
		return err
	}
	logger := logging.New(cfg.Logging)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	db, err := database.Open(ctx, cfg.Database, logger)
	if err != nil {
		return err
	}
	defer db.Close()

	service := sprintapp.NewService(sprintpostgres.NewRepository(db))
	actor := sprintapp.Actor{Subject: "snapshot", OrganizationID: orgID}
	points, err := service.SnapshotActive(ctx, actor)
	if err != nil {
		return err
	}
	for _, point := range points {
		fmt.Printf("sprint %s：范围 %d / 完成 %d / 剩余 %d（%s）\n",
			point.SprintID, point.ScopePoints, point.CompletedPoints, point.RemainingPoints,
			point.Date.Format("2006-01-02"))
	}
	fmt.Printf("快照完成：%d 个进行中冲刺\n", len(points))
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
