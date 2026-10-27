package grpc

import (
	"context"
	"log/slog"
	"runtime/debug"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	devv1 "github.com/xiaofeiwu/devmaster/backend/gen/go/dev/v1"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/auth"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/observability"
)

// NewServer 组装 gRPC 服务。拦截器顺序是刻意的：
// recovery 最外层（panic 也要变成错误，不能带崩进程）→ metrics（记录真实耗时）
// → auth（身份与授权）。
func NewServer(
	authenticator *auth.Authenticator,
	metrics *observability.Metrics,
	logger *slog.Logger,
	productService *ProductService,
	workItemService *WorkItemService,
	sprintService *SprintService,
	boardService *BoardService,
	systemService *SystemService,
) *grpc.Server {
	server := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			recoveryInterceptor(logger),
			metrics.UnaryServerInterceptor(),
			authenticator.UnaryInterceptor(),
		),
	)
	devv1.RegisterProductServiceServer(server, productService)
	devv1.RegisterWorkItemServiceServer(server, workItemService)
	devv1.RegisterSprintServiceServer(server, sprintService)
	devv1.RegisterBoardServiceServer(server, boardService)
	devv1.RegisterSystemServiceServer(server, systemService)
	reflection.Register(server)
	return server
}

func recoveryInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (response any, err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("grpc handler panic",
					slog.String("method", info.FullMethod),
					slog.Any("panic", recovered),
					slog.String("stack", string(debug.Stack())))
				err = status.Error(codes.Internal, "internal error")
			}
		}()
		return handler(ctx, req)
	}
}
