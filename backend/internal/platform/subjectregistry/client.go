// Package subjectregistry 是 subject-registry 的最小 gRPC 客户端。
//
// 只用到 Resolve 一个方法：把 approver 的 actorId 解析成主体锚点。
// 不做 Bind——登记别名是各域在「用户首次出现」时做的事，devmaster 只在
// 通知路由时消费结果。
package subjectregistry

import (
	"context"
	"fmt"
	"strings"
	"time"

	subjectv1 "github.com/xiaofeiwu/subject-registry/gen/go/subject/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/config"
)

// Client 是主体目录客户端。零值不可用，用 New 构造。
type Client struct {
	conn    *grpc.ClientConn
	service subjectv1.SubjectRegistryServiceClient
	token   string
	timeout time.Duration
}

// New 建立连接。地址与 token 缺失时报错——调用方应先看 Enabled 再构造。
func New(cfg config.SubjectRegistryConfig) (*Client, error) {
	if strings.TrimSpace(cfg.Address) == "" {
		return nil, fmt.Errorf("subject registry address is required")
	}
	if len(strings.TrimSpace(cfg.ServiceToken)) < 32 {
		return nil, fmt.Errorf("subject registry service token must be at least 32 characters")
	}
	conn, err := grpc.NewClient(cfg.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial subject registry %s: %w", cfg.Address, err)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Client{
		conn:    conn,
		service: subjectv1.NewSubjectRegistryServiceClient(conn),
		token:   strings.TrimSpace(cfg.ServiceToken),
		timeout: timeout,
	}, nil
}

// Close 释放连接。
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// ResolveSubject 解析外部 id。
//
// 返回 (anchorID, true, nil) 表示命中；(_, false, nil) 表示**没有登记过**——
// 这是正常结果，调用方应降级而不是重试；只有传输/服务端故障才返回 err。
func (c *Client) ResolveSubject(ctx context.Context, sourceSystem, externalID string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "x-subject-token", c.token, "x-actor", "devmaster")
	response, err := c.service.Resolve(ctx, &subjectv1.ResolveRequest{
		SourceSystem: sourceSystem,
		ExternalId:   externalID,
	})
	if err != nil {
		return "", false, fmt.Errorf("resolve subject %s/%s: %w", sourceSystem, externalID, err)
	}
	if !response.GetFound() {
		return "", false, nil
	}
	anchorID := response.GetAnchor().GetId()
	if anchorID == "" {
		return "", false, fmt.Errorf("subject registry returned found=true with an empty anchor id")
	}
	return anchorID, true, nil
}
