package grpc

import (
	"context"
	"log/slog"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	devv1 "github.com/xiaofeiwu/devmaster/backend/gen/go/dev/v1"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/auth"
	versionapp "github.com/xiaofeiwu/devmaster/backend/internal/version/application"
)

type VersionRegistryService struct {
	devv1.UnimplementedVersionRegistryServiceServer
	service *versionapp.Service
	logger  *slog.Logger
}

func NewVersionRegistryService(service *versionapp.Service, logger *slog.Logger) *VersionRegistryService {
	return &VersionRegistryService{service: service, logger: logger}
}

func (s *VersionRegistryService) CheckVersion(ctx context.Context, req *devv1.CheckVersionRequest) (*devv1.CheckVersionResponse, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no authenticated principal in context")
	}
	productCode := strings.TrimSpace(req.GetProductCode())
	versionName := strings.TrimSpace(req.GetVersion())
	if productCode == "" || versionName == "" {
		return nil, status.Error(codes.InvalidArgument, "product_code and version are required")
	}
	result, err := s.service.CheckVersion(ctx, versionapp.Actor{
		Subject: principal.Subject, OrganizationID: principal.OrganizationID, Roles: principal.Roles,
	}, productCode, versionName)
	if err != nil {
		return nil, appError(s.logger, err)
	}
	response := &devv1.CheckVersionResponse{
		Exists:    result.Exists,
		Released:  result.Released,
		VersionId: result.VersionID.String(),
		Status:    string(result.Status),
	}
	if result.ReleasedAt != nil {
		response.ReleasedAt = timestamppb.New(*result.ReleasedAt)
	}
	return response, nil
}
