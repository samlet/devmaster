// Package dbtest 提供集成测试用的真实数据库连接。
//
// 集成测试默认跳过（DEVMASTER_RUN_INTEGRATION=1 才跑），避免在没有库的机器上误红。
// 断言连接用 devmaster_app（非 owner），这样 RLS 是真正生效的；
// 清库用 devmaster_owner，因为 TRUNCATE 需要表所有权。
package dbtest

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/config"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/logging"
)

var (
	appOnce sync.Once
	appDB   *database.DB
	appErr  error
)

func Enabled() bool { return os.Getenv("DEVMASTER_RUN_INTEGRATION") == "1" }

func Require(t *testing.T) {
	t.Helper()
	if !Enabled() {
		t.Skip("set DEVMASTER_RUN_INTEGRATION=1 and run `make migrate-test` first")
	}
}

func DB(t *testing.T) *database.DB {
	t.Helper()
	Require(t)
	appOnce.Do(func() {
		cfg := config.Default()
		cfg.Database.Name = envOr("DEVMASTER_DATABASE_NAME", "devmaster_test")
		cfg.Database.User = "devmaster_app"
		cfg.Database.Password = "devmaster_dev_only"
		appDB, appErr = database.Open(context.Background(), cfg.Database,
			logging.New(config.LoggingConfig{Level: "error", Format: "text"}))
	})
	if appErr != nil {
		t.Fatalf("open test database: %v", appErr)
	}
	return appDB
}

// Cleanup 清空业务表。测试之间不共享状态，避免"单独跑通过、一起跑失败"。
func Cleanup(t *testing.T) {
	t.Helper()
	Require(t)
	owner, err := sql.Open("pgx", ownerDSN())
	if err != nil {
		t.Fatalf("open owner connection: %v", err)
	}
	defer owner.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const statement = `TRUNCATE TABLE products, outbox_events, inbox_events, idempotency_keys
		RESTART IDENTITY CASCADE`
	if _, err := owner.ExecContext(ctx, statement); err != nil {
		t.Fatalf("truncate test database: %v", err)
	}
}

func ownerDSN() string {
	return "postgres://devmaster_owner@" + envOr("DEVMASTER_DATABASE_HOST", "127.0.0.1") +
		":5432/" + envOr("DEVMASTER_DATABASE_NAME", "devmaster_test") + "?sslmode=disable"
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
