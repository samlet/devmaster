// devmaster-reconcile 做"事件折叠对账"：当前态必须等于事件日志折叠出来的状态。
//
// 这是 ADR-0002 的守卫。事件日志是审计依据，一旦它和当前态漂移，
// "三个月前这个工作项长什么样"就再也答不出来 —— 而且不会以 ERROR 出现，
// 只会让审计和报表悄悄失真。所以它必须是一个可定期运行、失败即非零退出的命令。
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
	workitempostgres "github.com/xiaofeiwu/devmaster/backend/internal/workitem/adapter/postgres"
	workitemapplication "github.com/xiaofeiwu/devmaster/backend/internal/workitem/application"
	workitemdomain "github.com/xiaofeiwu/devmaster/backend/internal/workitem/domain"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "devmaster-reconcile failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "path to YAML configuration file")
	organizationID := flag.String("organization-id", "", "organization to reconcile")
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	db, err := database.Open(ctx, cfg.Database, logger)
	if err != nil {
		return err
	}
	defer db.Close()

	repo := workitempostgres.NewRepository(db)
	service := workitemapplication.NewService(repo)
	actor := workitemapplication.Actor{Subject: "reconcile", OrganizationID: orgID}

	checked := 0
	mismatches := 0
	for offset := 0; ; offset += 200 {
		items, total, err := service.List(ctx, actor, workitemapplication.ListFilter{Limit: 200, Offset: offset})
		if err != nil {
			return err
		}
		for _, item := range items {
			events, err := service.ListEvents(ctx, actor, item.ID)
			if err != nil {
				return err
			}
			folded, ok := foldStatus(events)
			if !ok {
				fmt.Printf("MISMATCH %s：没有事件\n", item.Key)
				mismatches++
				continue
			}
			checked++
			if folded != item.Status {
				fmt.Printf("MISMATCH %s：当前态 %s，事件折叠 %s\n", item.Key, item.Status, folded)
				mismatches++
			}
		}
		if offset+len(items) >= total || len(items) == 0 {
			break
		}
	}

	fmt.Printf("对账完成：检查 %d 条，不一致 %d 条\n", checked, mismatches)
	if mismatches > 0 {
		return fmt.Errorf("%d work items drifted from their event log", mismatches)
	}
	return nil
}

// foldStatus 从事件日志还原当前状态：created 给出初始态，之后的 transitioned 覆盖。
// assigned/updated/linked/comment 不改状态，跳过。
func foldStatus(events []workitemapplication.EventRecord) (workitemdomain.Status, bool) {
	var (
		status workitemdomain.Status
		found  bool
	)
	for _, event := range events {
		if event.ToStatus == nil {
			continue
		}
		switch event.EventType {
		case "created", "transitioned":
			status = *event.ToStatus
			found = true
		}
	}
	return status, found
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
