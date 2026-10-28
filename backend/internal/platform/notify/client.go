// Package notify 发布通知意图到 notify（NATS JetStream，subject notify.intent.v1）。
//
// 通知是旁路：发不出去不能影响发布本身，所以调用方只记录错误，不改变业务结果。
package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/config"
)

const subject = "notify.intent.v1"

var namespace = uuid.NewSHA1(uuid.NameSpaceDNS, []byte("devmaster.notify"))

type Recipient struct {
	RecipientID string `json:"recipient_id"`
	UserID      string `json:"user_id,omitempty"`
	Phone       string `json:"phone,omitempty"`
	Email       string `json:"email,omitempty"`
}

type Intent struct {
	IntentID   string            `json:"intent_id"`
	Tenant     string            `json:"tenant"`
	Template   string            `json:"template"`
	Category   string            `json:"category"`
	Recipient  Recipient         `json:"recipient"`
	Params     map[string]any    `json:"params,omitempty"`
	DedupeKey  string            `json:"dedupe_key,omitempty"`
	OccurredAt string            `json:"occurred_at"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type Client struct {
	conn *nats.Conn
	js   jetstream.JetStream
	cfg  config.NotifyConfig
}

func New(cfg config.NotifyConfig) (*Client, error) {
	conn, err := nats.Connect(cfg.NATSURL, nats.Name("devmaster-notify"), nats.Timeout(5*time.Second))
	if err != nil {
		return nil, fmt.Errorf("connect notify nats: %w", err)
	}
	js, err := jetstream.New(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("notify jetstream: %w", err)
	}
	return &Client{conn: conn, js: js, cfg: cfg}, nil
}

func (c *Client) Close() { c.conn.Close() }

func (c *Client) Publish(ctx context.Context, intent Intent) error {
	intent.Tenant = c.cfg.Tenant
	if intent.Recipient.RecipientID == "" {
		intent.Recipient.RecipientID = c.cfg.RecipientID
	}
	if intent.IntentID == "" {
		intent.IntentID = uuid.NewSHA1(namespace, []byte(intent.Template+":"+intent.DedupeKey)).String()
	}
	if intent.OccurredAt == "" {
		intent.OccurredAt = time.Now().UTC().Format(time.RFC3339)
	}
	payload, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	message := &nats.Msg{Subject: subject, Data: payload, Header: nats.Header{}}
	message.Header.Set("Nats-Msg-Id", intent.IntentID)
	if _, err := c.js.PublishMsg(ctx, message); err != nil {
		return fmt.Errorf("publish notify intent: %w", err)
	}
	return nil
}

func (c *Client) ReleaseFailed(ctx context.Context, versionID, version, reason string) error {
	return c.Publish(ctx, Intent{
		Template:  "release.failed",
		Category:  "transactional",
		DedupeKey: "devmaster:release:" + versionID + ":failed",
		Params:    map[string]any{"version": version, "version_id": versionID, "reason": reason},
		Metadata:  map[string]string{"version_id": versionID},
	})
}

// ReleaseApprovalPending 是催办：发布已提交但审批还没结论。
// dedupe_key 固定到版本级，notify 侧不会因为工作流多轮提醒而重复轰炸。
func (c *Client) ReleaseApprovalPending(ctx context.Context, versionID, version, waited, threshold, submittedBy string) error {
	return c.Publish(ctx, Intent{
		Template:  "release.approval_pending",
		Category:  "transactional",
		DedupeKey: "devmaster:release:" + versionID + ":approval-pending",
		Params: map[string]any{
			"version": version, "version_id": versionID,
			"waited": waited, "threshold": threshold, "submitted_by": submittedBy,
		},
		Metadata: map[string]string{"version_id": versionID},
	})
}

func (c *Client) ReleasePublished(ctx context.Context, versionID, version, tag string) error {
	return c.Publish(ctx, Intent{
		Template:  "release.published",
		Category:  "transactional",
		DedupeKey: "devmaster:release:" + versionID + ":published",
		Params:    map[string]any{"version": version, "tag": tag},
		Metadata:  map[string]string{"version_id": versionID},
	})
}
