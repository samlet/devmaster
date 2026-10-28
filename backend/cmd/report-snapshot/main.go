// devmaster-report-snapshot 固化报表日快照（CFD / 吞吐读它）。
//
// 日报：`-days 1`；回填：`-days 60`；不传 -product 则遍历组织内所有产品。
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/config"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/logging"
	reportpostgres "github.com/xiaofeiwu/devmaster/backend/internal/report/adapter/postgres"
	reportapp "github.com/xiaofeiwu/devmaster/backend/internal/report/application"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "report-snapshot failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "path to YAML configuration file")
	organizationID := flag.String("organization-id", "00000000-0000-0000-0000-000000000001", "organization")
	productFlag := flag.String("product", "", "product UUID; empty means all products")
	days := flag.Int("days", 1, "how many days back to snapshot")
	flag.Parse()

	cfg, err := config.Load(firstNonEmpty(*configPath, os.Getenv("DEVMASTER_CONFIG_FILE")))
	if err != nil {
		return err
	}
	logger := logging.New(cfg.Logging)
	org, err := uuid.Parse(*organizationID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	db, err := database.Open(ctx, cfg.Database, logger)
	if err != nil {
		return err
	}
	defer db.Close()

	service := reportapp.NewService(reportpostgres.NewRepository(db))
	actor := reportapp.Actor{Subject: "report-snapshot", OrganizationID: org}
	now := time.Now().UTC()
	from := now.AddDate(0, 0, -*days)

	productIDs, err := resolveProducts(ctx, db, org, *productFlag)
	if err != nil {
		return err
	}
	for _, productID := range productIDs {
		count, err := service.SnapshotRange(ctx, actor, productID, from, now)
		if err != nil {
			return fmt.Errorf("snapshot product %s: %w", productID, err)
		}
		fmt.Printf("product %s：写入 %d 天快照\n", productID, count)
	}
	return nil
}

func resolveProducts(ctx context.Context, db *database.DB, org uuid.UUID, raw string) ([]uuid.UUID, error) {
	if raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("product: %w", err)
		}
		return []uuid.UUID{parsed}, nil
	}
	var ids []uuid.UUID
	err := db.InOrgTx(ctx, org, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT id FROM products WHERE organization_id = $1 AND status = 'ACTIVE' ORDER BY key`, org)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	return ids, err
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
