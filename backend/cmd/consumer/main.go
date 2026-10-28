// devmaster-consumer 消费自寻址事件 dev.v1.release.submitted，启动 ReleaseWorkflow。
//
// 为什么不在 SubmitRelease 请求里同步起流程：写库成功但起流程失败会让版本
// 卡在 PENDING_APPROVAL 且无法重提。事件 + outbox + 消费者把"提交"和"起流程"
// 解耦，失败可以重放。
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/config"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/logging"
	"github.com/xiaofeiwu/devmaster/backend/internal/workflow"
)

type envelope struct {
	EventID          string          `json:"event_id"`
	EventType        string          `json:"event_type"`
	OrganizationID   string          `json:"organization_id"`
	AggregateType    string          `json:"aggregate_type"`
	AggregateID      string          `json:"aggregate_id"`
	AggregateVersion int64           `json:"aggregate_version"`
	Producer         string          `json:"producer"`
	Payload          json.RawMessage `json:"payload"`
}

type releasePayload struct {
	VersionID       string `json:"version_id"`
	ReleaseRecordID string `json:"release_record_id"`
	ProductCode     string `json:"product_code"`
	Version         string `json:"version"`
	SubmittedBy     string `json:"submitted_by"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "devmaster-consumer failed: %v\n", err)
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

	db, err := database.Open(ctx, cfg.Database, logger)
	if err != nil {
		return err
	}
	defer db.Close()

	temporalClient, err := client.Dial(client.Options{
		HostPort:  cfg.Temporal.HostPort,
		Namespace: cfg.Temporal.Namespace,
	})
	if err != nil {
		return fmt.Errorf("connect temporal: %w", err)
	}
	defer temporalClient.Close()

	conn, err := nats.Connect(cfg.NATS.URL, nats.Name("devmaster-consumer"), nats.Timeout(5*time.Second))
	if err != nil {
		return fmt.Errorf("connect nats: %w", err)
	}
	defer conn.Close()
	js, err := jetstream.New(conn)
	if err != nil {
		return fmt.Errorf("jetstream: %w", err)
	}
	consumer, err := js.CreateOrUpdateConsumer(ctx, cfg.NATS.Stream, jetstream.ConsumerConfig{
		Durable:       "devmaster-consumer",
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: "dev.v1.release.submitted",
		MaxDeliver:    10,
	})
	if err != nil {
		return fmt.Errorf("create consumer: %w", err)
	}

	handler := &releaseHandler{db: db, temporal: temporalClient, cfg: cfg, logger: logger}
	consumeCtx, err := consumer.Consume(func(message jetstream.Msg) {
		if err := handler.Handle(context.Background(), message.Data()); err != nil {
			logger.Error("handle release event failed",
				slog.String("subject", message.Subject()), slog.String("error", err.Error()))
			_ = message.Nak()
			return
		}
		_ = message.Ack()
	})
	if err != nil {
		return fmt.Errorf("consume: %w", err)
	}
	defer consumeCtx.Stop()

	logger.Info("consumer started",
		slog.String("stream", cfg.NATS.Stream),
		slog.String("subject", "dev.v1.release.submitted"))
	<-ctx.Done()
	logger.Info("consumer stopped")
	return nil
}

type releaseHandler struct {
	db       *database.DB
	temporal client.Client
	cfg      config.Config
	logger   *slog.Logger
}

func (h *releaseHandler) Handle(ctx context.Context, raw []byte) error {
	var event envelope
	if err := json.Unmarshal(raw, &event); err != nil {
		return fmt.Errorf("decode envelope: %w", err)
	}
	if event.EventType != "dev.v1.release.submitted" {
		return nil
	}
	organizationID, err := uuid.Parse(event.OrganizationID)
	if err != nil {
		return fmt.Errorf("invalid organization_id: %w", err)
	}
	var payload releasePayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	releaseID, err := uuid.Parse(payload.ReleaseRecordID)
	if err != nil {
		return fmt.Errorf("invalid release_record_id: %w", err)
	}
	versionID, err := uuid.Parse(payload.VersionID)
	if err != nil {
		return fmt.Errorf("invalid version_id: %w", err)
	}

	// 入站幂等：同一 event_id 只处理一次。插入失败（冲突）说明已处理过。
	inserted, err := h.markReceived(ctx, organizationID, event, raw)
	if err != nil {
		return err
	}
	if !inserted {
		h.logger.Info("duplicate release event skipped", slog.String("event_id", event.EventID))
		return nil
	}

	input := workflow.ReleaseInput{
		OrganizationID: organizationID,
		ReleaseID:      releaseID,
		VersionID:      versionID,
		SubmittedBy:    payload.SubmittedBy,
		PollInterval:   h.cfg.Approver.PollInterval,
		PollTimeout:    h.cfg.Approver.PollTimeout,
		ReminderAfter:  h.cfg.Notify.ApprovalReminderAfter,
	}
	options := client.StartWorkflowOptions{
		ID:                    "release-" + releaseID.String(),
		TaskQueue:             h.cfg.Temporal.TaskQueue,
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
	}
	if _, err := h.temporal.ExecuteWorkflow(ctx, options, workflow.ReleaseWorkflow, input); err != nil {
		if isAlreadyStarted(err) {
			h.logger.Info("release workflow already started", slog.String("release_id", releaseID.String()))
		} else {
			_ = h.markFailed(ctx, organizationID, event.EventID, err.Error())
			return fmt.Errorf("start release workflow: %w", err)
		}
	}
	return h.markProcessed(ctx, organizationID, event.EventID)
}

func (h *releaseHandler) markReceived(ctx context.Context, organizationID uuid.UUID, event envelope, raw []byte) (bool, error) {
	hash := sha256.Sum256(raw)
	var inserted bool
	err := h.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO inbox_events
				(organization_id, source_system, event_id, event_type, event_version, subject, payload, payload_sha256, status)
			VALUES ($1,'devmaster',$2,$3,1,$4,$5,$6,'RECEIVED')
			ON CONFLICT (source_system, event_id) DO NOTHING`,
			organizationID, event.EventID, event.EventType, event.EventType, raw, hash[:])
		if err != nil {
			return err
		}
		affected, _ := result.RowsAffected()
		inserted = affected > 0
		return nil
	})
	return inserted, err
}

func (h *releaseHandler) markProcessed(ctx context.Context, organizationID uuid.UUID, eventID string) error {
	return h.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE inbox_events SET status = 'PROCESSED', processed_at = now()
			 WHERE organization_id = $1 AND source_system = 'devmaster' AND event_id = $2`,
			organizationID, eventID)
		return err
	})
}

func (h *releaseHandler) markFailed(ctx context.Context, organizationID uuid.UUID, eventID, cause string) error {
	return h.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE inbox_events SET status = 'FAILED', attempts = attempts + 1, last_error = $3
			 WHERE organization_id = $1 AND source_system = 'devmaster' AND event_id = $2`,
			organizationID, eventID, cause)
		return err
	})
}

func isAlreadyStarted(err error) bool {
	var already *serviceerror.WorkflowExecutionAlreadyStarted
	return errors.As(err, &already)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
