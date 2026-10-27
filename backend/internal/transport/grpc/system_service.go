package grpc

import (
	"context"

	devv1 "github.com/xiaofeiwu/devmaster/backend/gen/go/dev/v1"
)

type SystemService struct {
	devv1.UnimplementedSystemServiceServer
}

func NewSystemService() *SystemService { return &SystemService{} }

// Health 是 gRPC 侧的最小健康面。真正的依赖探活在 HTTP /healthz：
// 那里会查数据库并返回 503，gRPC 这里只证明进程在监听。
func (s *SystemService) Health(context.Context, *devv1.HealthRequest) (*devv1.HealthResponse, error) {
	return &devv1.HealthResponse{
		Status:     "ok",
		Components: map[string]string{"service": "ok"},
	}, nil
}
