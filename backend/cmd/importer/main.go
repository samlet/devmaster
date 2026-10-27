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
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "devmaster-importer failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "path to YAML configuration file")
	datasetPath := flag.String("dataset", "", "path to a dataset directory (manifest.json + CSV)")
	dryRun := flag.Bool("dry-run", false, "validate the dataset without touching the database")
	flag.Parse()

	if *datasetPath == "" {
		return fmt.Errorf("-dataset is required")
	}
	dataset, err := LoadDataset(*datasetPath)
	if err != nil {
		return err
	}
	fmt.Printf("数据包 %s（version %d）：产品 %d 行，工作项 %d 行\n",
		dataset.Manifest.Name, dataset.Manifest.Version,
		len(dataset.Products), len(dataset.WorkItems))
	if *dryRun {
		fmt.Println("dry-run：结构校验通过，未写库")
		return nil
	}

	organizationID, err := uuid.Parse(dataset.Manifest.OrganizationID)
	if err != nil {
		return fmt.Errorf("manifest.organization_id: %w", err)
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

	importer := NewImporter(db, organizationID)
	summary, err := importer.Run(ctx, dataset)
	if err != nil {
		return err
	}
	fmt.Printf("导入完成：产品新建 %d / 已存在 %d；工作项新建 %d / 已存在 %d\n",
		summary.ProductsCreated, summary.ProductsExisting,
		summary.ItemsCreated, summary.ItemsExisting)

	if err := importer.Verify(ctx, dataset); err != nil {
		return fmt.Errorf("expectations 校验失败：%w", err)
	}
	fmt.Println("expectations 校验通过")
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
