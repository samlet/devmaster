// Package jetstream 封装 NATS JetStream 连接与投递。
package jetstream

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/config"
)

type Client struct {
	conn   *nats.Conn
	js     jetstream.JetStream
	stream string
}

// Connect 连接 NATS 并确认流存在。
//
// ReadOnly=true 时不改写流配置：流是"拥有方"的资源，两个进程各拿自己的
// 主题前缀去 CreateOrUpdateStream 会轮流覆盖它，期间发布的事件直接丢。
func Connect(ctx context.Context, cfg config.NATSConfig) (*Client, error) {
	conn, err := nats.Connect(cfg.URL,
		nats.Name("devmaster"),
		nats.Timeout(5*time.Second),
		nats.MaxReconnects(-1),
	)
	if err != nil {
		return nil, fmt.Errorf("connect nats %s: %w", cfg.URL, err)
	}
	js, err := jetstream.New(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("jetstream context: %w", err)
	}

	if _, err := js.Stream(ctx, cfg.Stream); err != nil {
		if cfg.ReadOnly {
			conn.Close()
			return nil, fmt.Errorf("stream %s not found (read_only=true): %w", cfg.Stream, err)
		}
		if _, createErr := js.CreateStream(ctx, jetstream.StreamConfig{
			Name:        cfg.Stream,
			Description: "devmaster 领域事件",
			Subjects:    []string{cfg.SubjectPrefix + ".>"},
			Retention:   jetstream.LimitsPolicy,
			MaxAge:      7 * 24 * time.Hour,
			Storage:     jetstream.FileStorage,
			Discard:     jetstream.DiscardOld,
			Duplicates:  2 * time.Minute,
		}); createErr != nil {
			conn.Close()
			return nil, fmt.Errorf("create stream %s: %w", cfg.Stream, createErr)
		}
	}
	return &Client{conn: conn, js: js, stream: cfg.Stream}, nil
}

func (c *Client) Publish(ctx context.Context, subject string, data []byte, headers map[string]string) error {
	message := &nats.Msg{Subject: subject, Data: data, Header: nats.Header{}}
	for key, value := range headers {
		message.Header.Set(key, value)
	}
	if _, err := c.js.PublishMsg(ctx, message); err != nil {
		return fmt.Errorf("publish %s: %w", subject, err)
	}
	return nil
}

func (c *Client) Close() { c.conn.Close() }
