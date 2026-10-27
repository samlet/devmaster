package grpc

import (
	"errors"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
)

// appError 把领域错误翻译成 gRPC 状态码。
//
// 未识别的错误统一返回 Internal，并且不把原始信息透给客户端：
// 数据库错误里常带 SQL 片段与表名，透出去等于免费侦察。
func appError(logger *slog.Logger, err error) error {
	if err == nil {
		return nil
	}
	var typed *apperr.Error
	if errors.As(err, &typed) {
		switch typed.Kind {
		case apperr.KindInvalid:
			return status.Error(codes.InvalidArgument, typed.Error())
		case apperr.KindNotFound:
			return status.Error(codes.NotFound, typed.Error())
		case apperr.KindConflict:
			return status.Error(codes.AlreadyExists, typed.Error())
		case apperr.KindFailedPrecondition:
			return status.Error(codes.FailedPrecondition, typed.Error())
		}
	}
	logger.Error("unhandled application error", slog.String("error", err.Error()))
	return status.Error(codes.Internal, "internal error")
}
