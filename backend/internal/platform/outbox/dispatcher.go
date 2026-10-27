package outbox

import (
	"context"
	"log/slog"
	"time"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/config"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/observability"
)

// Publisher 是投递端口。JetStream 实现它，测试可以用内存实现。
type Publisher interface {
	Publish(ctx context.Context, subject string, data []byte, headers map[string]string) error
}

type Dispatcher struct {
	db        *database.DB
	repo      *Repository
	publisher Publisher
	metrics   *observability.Metrics
	logger    *slog.Logger
	cfg       config.OutboxConfig
}

func NewDispatcher(db *database.DB, repo *Repository, publisher Publisher, metrics *observability.Metrics, logger *slog.Logger, cfg config.OutboxConfig) *Dispatcher {
	return &Dispatcher{db: db, repo: repo, publisher: publisher, metrics: metrics, logger: logger, cfg: cfg}
}

// Run 持续投递直到 ctx 取消。
func (d *Dispatcher) Run(ctx context.Context) error {
	ticker := time.NewTicker(d.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := d.RunOnce(ctx); err != nil {
				d.logger.Warn("outbox dispatch failed", slog.String("error", err.Error()))
			}
		}
	}
}

// RunOnce 投递一批，返回成功条数。
func (d *Dispatcher) RunOnce(ctx context.Context) (int, error) {
	lease := 30 * time.Second
	events, err := d.repo.ClaimBatch(ctx, d.db, d.cfg.BatchSize, lease, d.cfg.MaxAttempts)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, event := range events {
		if err := d.publisher.Publish(ctx, event.Subject, event.Payload, event.Headers); err != nil {
			backoff := d.backoff(event.Attempts)
			if markErr := d.repo.MarkFailed(ctx, d.db, event.ID, err, backoff); markErr != nil {
				d.logger.Error("mark outbox failed",
					slog.Int64("id", event.ID), slog.String("error", markErr.Error()))
			}
			if d.metrics != nil {
				d.metrics.OutboxFailed.Inc()
			}
			d.logger.Warn("outbox publish failed",
				slog.String("subject", event.Subject),
				slog.String("event_id", event.EventID.String()),
				slog.Int("attempts", event.Attempts),
				slog.Duration("backoff", backoff),
				slog.String("error", err.Error()))
			continue
		}
		if err := d.repo.MarkSent(ctx, d.db, event.ID); err != nil {
			return sent, err
		}
		sent++
		if d.metrics != nil {
			d.metrics.OutboxSent.Inc()
		}
	}
	if pending, err := d.repo.PendingCount(ctx, d.db); err == nil && d.metrics != nil {
		d.metrics.OutboxPending.Set(float64(pending))
	}
	return sent, nil
}

// backoff 是指数退避：2s → 4s → 8s …，上限 max_backoff。
// 用 attempts 而不是随机抖动：本地单 relay，确定性更好排障。
func (d *Dispatcher) backoff(attempts int) time.Duration {
	delay := d.cfg.BaseBackoff
	for i := 1; i < attempts && delay < d.cfg.MaxBackoff; i++ {
		delay *= 2
	}
	if delay > d.cfg.MaxBackoff {
		delay = d.cfg.MaxBackoff
	}
	return delay
}
