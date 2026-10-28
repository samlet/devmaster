// devmaster-perfbaseline 造 10 万工作项并测报表接口 P95。
//
// 这是回归基线，不是容量承诺：本地 PostgreSQL + 单进程 server。
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/config"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/logging"
)

const perfProductID = "7e000000-0000-4000-8000-000000000001"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "perfbaseline failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "path to YAML configuration file")
	organizationID := flag.String("organization-id", "00000000-0000-0000-0000-000000000001", "organization")
	items := flag.Int("items", 100000, "number of work items to seed")
	requests := flag.Int("requests", 20, "requests per endpoint")
	api := flag.String("api", "http://127.0.0.1:8291", "devmaster HTTP API")
	ownerDSN := flag.String("owner-dsn", "postgres://devmaster_owner@127.0.0.1:5432/devmaster_dev?sslmode=disable", "owner DSN for cleanup")
	cleanup := flag.Bool("cleanup", true, "delete seeded data after measuring")
	flag.Parse()

	cfg, err := config.Load(firstNonEmpty(*configPath, os.Getenv("DEVMASTER_CONFIG_FILE")))
	if err != nil {
		return err
	}
	logger := logging.New(cfg.Logging)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	db, err := database.Open(ctx, cfg.Database, logger)
	if err != nil {
		return err
	}
	defer db.Close()

	org, err := uuid.Parse(*organizationID)
	if err != nil {
		return err
	}
	if err := seed(ctx, db, org, *items); err != nil {
		return err
	}
	fmt.Printf("已造数：%d 个工作项\n", *items)
	if *cleanup {
		defer func() {
			if err := clean(context.Background(), *ownerDSN, org); err != nil {
				fmt.Fprintf(os.Stderr, "cleanup: %v\n", err)
			} else {
				fmt.Println("已清理造数")
			}
		}()
	}

	endpoints := []string{
		"/api/reports/cycle-time?days=30",
		"/api/reports/team-load",
		"/api/reports/throughput?weeks=12",
		"/api/reports/cfd?days=14",
	}
	failed := false
	for _, endpoint := range endpoints {
		p95, err := measure(*api+endpoint, *requests)
		if err != nil {
			return err
		}
		status := "PASS"
		if p95 >= 300 {
			status = "FAIL"
			failed = true
		}
		fmt.Printf("%-45s P95=%6.1fms  %s\n", endpoint, p95, status)
	}
	if failed {
		return fmt.Errorf("P95 超过 300ms 基线")
	}
	return nil
}

func seed(ctx context.Context, db *database.DB, org uuid.UUID, items int) error {
	return db.InOrgTx(ctx, org, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO products (id, organization_id, key, name, status)
			VALUES ($1,$2,'PERF','性能基线产品','ACTIVE')
			ON CONFLICT (organization_id, key) DO NOTHING`, perfProductID, org); err != nil {
			return fmt.Errorf("insert perf product: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO work_items (id, organization_id, key, product_id, type, status, title,
				reporter_id, assignee_id, rank, created_at, updated_at)
			SELECT gen_random_uuid(), $1, 'PERF-' || i, $2, 'TASK',
			       CASE WHEN i % 5 = 0 THEN 'IN_PROGRESS' ELSE 'DONE' END,
			       'perf item ' || i, 'perf', 'perf-user-' || (i % 20),
			       lpad(to_hex(i), 10, '0'),
			       now() - ((i % 60) || ' days')::interval,
			       now() - ((i % 60) || ' days')::interval
			  FROM generate_series(1, $3) AS i
			ON CONFLICT (organization_id, key) DO NOTHING`, org, perfProductID, items); err != nil {
			return fmt.Errorf("insert perf items: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO work_item_events (organization_id, work_item_id, event_type, to_status, actor_id, payload, occurred_at)
			SELECT $1, id, 'created', 'BACKLOG', 'perf', '{}', created_at
			  FROM work_items WHERE organization_id = $1 AND product_id = $2`, org, perfProductID); err != nil {
			return fmt.Errorf("insert created events: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO work_item_events (organization_id, work_item_id, event_type, to_status, actor_id, payload, occurred_at)
			SELECT $1, id, 'transitioned',
			       CASE WHEN status = 'DONE' THEN 'DONE' ELSE 'IN_PROGRESS' END,
			       'perf', '{}', created_at + interval '2 days'
			  FROM work_items WHERE organization_id = $1 AND product_id = $2`, org, perfProductID); err != nil {
			return fmt.Errorf("insert transition events: %w", err)
		}
		// 造数后立即 ANALYZE：否则规划器拿不到新分布，报表会按全表扫描跑。
		if _, err := tx.ExecContext(ctx, `ANALYZE work_items`); err != nil {
			return fmt.Errorf("analyze work_items: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `ANALYZE work_item_events`); err != nil {
			return fmt.Errorf("analyze work_item_events: %w", err)
		}
		return nil
	})
}

func clean(ctx context.Context, ownerDSN string, org uuid.UUID) error {
	owner, err := sql.Open("pgx", ownerDSN)
	if err != nil {
		return err
	}
	defer owner.Close()
	if _, err := owner.ExecContext(ctx, `ALTER TABLE work_item_events DISABLE TRIGGER work_item_events_no_update`); err != nil {
		return err
	}
	defer func() {
		_, _ = owner.ExecContext(context.Background(),
			`ALTER TABLE work_item_events ENABLE TRIGGER work_item_events_no_update`)
	}()
	statements := []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM work_item_events WHERE organization_id = $1 AND work_item_id IN
			(SELECT id FROM work_items WHERE organization_id = $1 AND product_id = $2)`,
			[]any{org, perfProductID}},
		{`DELETE FROM work_items WHERE organization_id = $1 AND product_id = $2`,
			[]any{org, perfProductID}},
		{`DELETE FROM product_counters WHERE product_id = $1`, []any{perfProductID}},
		{`DELETE FROM products WHERE organization_id = $1 AND id = $2`, []any{org, perfProductID}},
	}
	for _, statement := range statements {
		if _, err := owner.ExecContext(ctx, statement.sql, statement.args...); err != nil {
			return err
		}
	}
	return nil
}

func measure(url string, requests int) (float64, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	samples := make([]float64, 0, requests)
	for i := 0; i < requests; i++ {
		started := time.Now()
		response, err := client.Get(url)
		if err != nil {
			return 0, err
		}
		var payload any
		_ = json.NewDecoder(response.Body).Decode(&payload)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return 0, fmt.Errorf("%s returned %d", url, response.StatusCode)
		}
		samples = append(samples, float64(time.Since(started).Microseconds())/1000)
	}
	sort.Float64s(samples)
	index := int(float64(len(samples))*0.95) - 1
	if index < 0 {
		index = 0
	}
	return samples[index], nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
