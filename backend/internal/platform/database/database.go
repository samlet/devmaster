// Package database 负责连接池与"按组织开事务"。
//
// 组织隔离靠两件事同时成立：
//  1. 每条业务查询都带 organization_id 谓词（应用层）；
//  2. 数据库 RLS 用 app.organization_id 兜底（迁移里 FORCE ROW LEVEL SECURITY）。
//
// 第 2 条要求每个事务先把这个 GUC 设进去，否则 RLS 谓词恒为 NULL，
// 查询会"看不到任何行"——不是报错，是静默空结果，很容易被当成没数据。
package database

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib" // pgx 的 database/sql 驱动

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/config"
)

type DB struct {
	SQL *sql.DB
}

func Open(ctx context.Context, cfg config.DatabaseConfig, logger *slog.Logger) (*DB, error) {
	pool, err := sql.Open("pgx", cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	pool.SetMaxOpenConns(cfg.MaxOpenConns)
	pool.SetMaxIdleConns(cfg.MaxIdleConns)
	pool.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.PingContext(pingCtx); err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("ping database %s:%d/%s: %w", cfg.Host, cfg.Port, cfg.Name, err)
	}
	logger.Info("database connected",
		slog.String("host", cfg.Host),
		slog.Int("port", cfg.Port),
		slog.String("database", cfg.Name))
	return &DB{SQL: pool}, nil
}

func (d *DB) Close() error { return d.SQL.Close() }

// InOrgTx 在事务内设置组织上下文后执行 fn。
//
// set_config(..., true) 的第三个参数是 is_local：事务结束自动失效，
// 不会把组织串到连接池里下一个借到这条连接的请求上。用 SET 会串。
func (d *DB) InOrgTx(ctx context.Context, organizationID uuid.UUID, fn func(*sql.Tx) error) error {
	if organizationID == uuid.Nil {
		return fmt.Errorf("organization_id is required")
	}
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`SELECT set_config('app.organization_id', $1, true)`, organizationID.String()); err != nil {
		return fmt.Errorf("set organization context: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// Ping 供健康检查使用。
func (d *DB) Ping(ctx context.Context) error { return d.SQL.PingContext(ctx) }
