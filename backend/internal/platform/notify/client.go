// Package notify 发布通知意图到 notify（NATS JetStream，subject notify.intent.v1）。
//
// 通知是旁路：发不出去不能影响发布本身，所以调用方只记录错误，不改变业务结果。
package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

// Target 是发布类通知的收件目标。
//
// RecipientID 为空表示「用配置里的默认收件人」（团队收件箱）。调用方在能解析出
// 具体主体时传 "user:<anchor_id>"，解析不出来就留空——留空是显式降级，
// 不是静默失败。
type Target struct {
	VersionID   string
	Version     string
	RecipientID string
}

func (c *Client) ReleaseFailed(ctx context.Context, target Target, reason string) error {
	return c.Publish(ctx, Intent{
		Template:  "release.failed",
		Category:  "transactional",
		Recipient: Recipient{RecipientID: target.RecipientID},
		DedupeKey: "devmaster:release:" + target.VersionID + ":failed:" + target.RecipientID,
		Params: map[string]any{
			"version": target.Version, "version_id": target.VersionID, "reason": reason,
		},
		Metadata: map[string]string{"version_id": target.VersionID},
	})
}

// ApprovalReminder 是一次催办的全部参数。
//
// 用结构体而不是六个裸 string：它们全是 string，顺序写错编译器不会报错，
// 而错位的后果是把等待时长渲染进收件人字段——排障时根本看不出来。
type ApprovalReminder struct {
	VersionID   string
	Version     string
	Waited      string
	Threshold   string
	SubmittedBy string
	// RecipientID 是 notify 的收件人键，由调用方解析后传入：
	// 命中主体目录用 "user:<anchor_id>"，未命中降级 "role:RELEASE_APPROVER"。
	RecipientID string
}

// ReleaseApprovalPending 是催办：发布已提交但审批还没结论。
//
// dedupe_key 带上收件人：同一版本对不同收件人是两条通知。若只按版本去重，
// 「先发给角色、后来能定向到人」时第二条会被静默吃掉，本人永远收不到。
func (c *Client) ReleaseApprovalPending(ctx context.Context, reminder ApprovalReminder) error {
	recipientID := strings.TrimSpace(reminder.RecipientID)
	if recipientID == "" {
		return fmt.Errorf("approval reminder recipient is required")
	}
	return c.Publish(ctx, Intent{
		Template:  "release.approval_pending",
		Category:  "transactional",
		Recipient: Recipient{RecipientID: recipientID},
		DedupeKey: "devmaster:release:" + reminder.VersionID + ":approval-pending:" + recipientID,
		Params: map[string]any{
			"version": reminder.Version, "version_id": reminder.VersionID,
			"waited": reminder.Waited, "threshold": reminder.Threshold,
			"submitted_by": reminder.SubmittedBy,
		},
		Metadata: map[string]string{"version_id": reminder.VersionID},
	})
}

func (c *Client) ReleasePublished(ctx context.Context, target Target, tag string) error {
	return c.Publish(ctx, Intent{
		Template:  "release.published",
		Category:  "transactional",
		Recipient: Recipient{RecipientID: target.RecipientID},
		DedupeKey: "devmaster:release:" + target.VersionID + ":published:" + target.RecipientID,
		Params:    map[string]any{"version": target.Version, "tag": tag},
		Metadata:  map[string]string{"version_id": target.VersionID},
	})
}
