package grpc

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	devv1 "github.com/xiaofeiwu/devmaster/backend/gen/go/dev/v1"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/auth"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/idempotency"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/observability"
	"github.com/xiaofeiwu/devmaster/backend/internal/product/application"
	"github.com/xiaofeiwu/devmaster/backend/internal/product/domain"
)

type ProductService struct {
	devv1.UnimplementedProductServiceServer
	service *application.Service
	db      *database.DB
	metrics *observability.Metrics
	logger  *slog.Logger
}

func NewProductService(service *application.Service, db *database.DB, metrics *observability.Metrics, logger *slog.Logger) *ProductService {
	return &ProductService{service: service, db: db, metrics: metrics, logger: logger}
}

func actorFrom(ctx context.Context) (application.Actor, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return application.Actor{}, status.Error(codes.Unauthenticated, "no authenticated principal in context")
	}
	return application.Actor{Subject: principal.Subject, OrganizationID: principal.OrganizationID}, nil
}

func productStatusToProto(status domain.Status) devv1.ProductStatus {
	switch status {
	case domain.StatusActive:
		return devv1.ProductStatus_PRODUCT_STATUS_ACTIVE
	case domain.StatusArchived:
		return devv1.ProductStatus_PRODUCT_STATUS_ARCHIVED
	default:
		return devv1.ProductStatus_PRODUCT_STATUS_UNSPECIFIED
	}
}

func productToProto(product domain.Product) *devv1.Product {
	return &devv1.Product{
		Id:             product.ID.String(),
		OrganizationId: product.OrganizationID.String(),
		Key:            product.Key,
		Name:           product.Name,
		Description:    product.Description,
		Status:         productStatusToProto(product.Status),
		CreatedAt:      timestamppb.New(product.CreatedAt),
		UpdatedAt:      timestamppb.New(product.UpdatedAt),
	}
}

func (s *ProductService) CreateProduct(ctx context.Context, req *devv1.CreateProductRequest) (*devv1.CreateProductResponse, error) {
	actor, err := actorFrom(ctx)
	if err != nil {
		return nil, err
	}
	key, _, err := requireWriteOptions(req.GetOptions())
	if err != nil {
		return nil, err
	}
	response, err := idempotency.Do(ctx, s.db, actor.OrganizationID,
		"product.create", key, req,
		func() *devv1.CreateProductResponse { return &devv1.CreateProductResponse{} },
		func(ctx context.Context) (*devv1.CreateProductResponse, error) {
			product, err := s.service.Create(ctx, actor, application.CreateCommand{
				Key:         req.GetKey(),
				Name:        req.GetName(),
				Description: req.GetDescription(),
			})
			if err != nil {
				return nil, appError(s.logger, err)
			}
			if s.metrics != nil {
				s.metrics.ProductsCreated.Inc()
			}
			return &devv1.CreateProductResponse{Product: productToProto(product)}, nil
		})
	if err != nil {
		return nil, err
	}
	return response, nil
}

func (s *ProductService) GetProduct(ctx context.Context, req *devv1.GetProductRequest) (*devv1.GetProductResponse, error) {
	actor, err := actorFrom(ctx)
	if err != nil {
		return nil, err
	}
	var product domain.Product
	switch {
	case strings.TrimSpace(req.GetId()) != "":
		id, parseErr := uuid.Parse(strings.TrimSpace(req.GetId()))
		if parseErr != nil {
			return nil, status.Error(codes.InvalidArgument, "id must be a valid UUID")
		}
		product, err = s.service.Get(ctx, actor, id)
	case strings.TrimSpace(req.GetKey()) != "":
		product, err = s.service.GetByKey(ctx, actor, req.GetKey())
	default:
		return nil, status.Error(codes.InvalidArgument, "either id or key is required")
	}
	if err != nil {
		return nil, appError(s.logger, err)
	}
	return &devv1.GetProductResponse{Product: productToProto(product)}, nil
}

func (s *ProductService) ListProducts(ctx context.Context, req *devv1.ListProductsRequest) (*devv1.ListProductsResponse, error) {
	actor, err := actorFrom(ctx)
	if err != nil {
		return nil, err
	}
	filter := application.ListFilter{}
	switch req.GetStatus() {
	case devv1.ProductStatus_PRODUCT_STATUS_ACTIVE:
		filter.Status = domain.StatusActive
	case devv1.ProductStatus_PRODUCT_STATUS_ARCHIVED:
		filter.Status = domain.StatusArchived
	case devv1.ProductStatus_PRODUCT_STATUS_UNSPECIFIED:
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown product status")
	}
	if filter.Limit, filter.Offset, err = pageWindow(req.GetPage()); err != nil {
		return nil, err
	}
	items, total, err := s.service.List(ctx, actor, filter)
	if err != nil {
		return nil, appError(s.logger, err)
	}
	out := make([]*devv1.Product, 0, len(items))
	for _, item := range items {
		out = append(out, productToProto(item))
	}
	return &devv1.ListProductsResponse{
		Products: out,
		Page:     pageResponse(total, filter.Limit, filter.Offset, len(items)),
	}, nil
}

func pageWindow(page *devv1.PageRequest) (limit, offset int, err error) {
	limit = 50
	if page != nil && page.GetPageSize() > 0 {
		limit = int(page.GetPageSize())
		if limit > 200 {
			limit = 200
		}
	}
	if token := strings.TrimSpace(page.GetPageToken()); token != "" {
		parsed, convErr := strconv.Atoi(token)
		if convErr != nil || parsed < 0 {
			return 0, 0, status.Errorf(codes.InvalidArgument,
				"page.page_token must be a non-negative integer, got %q", token)
		}
		offset = parsed
	}
	return limit, offset, nil
}

func pageResponse(total, limit, offset, returned int) *devv1.PageResponse {
	out := &devv1.PageResponse{TotalSize: int32(total)}
	if next := offset + returned; next < total {
		out.NextPageToken = strconv.Itoa(next)
	}
	return out
}

func requireWriteOptions(options *devv1.WriteOptions) (string, int64, error) {
	if options == nil {
		return "", 0, status.Error(codes.InvalidArgument, "options is required for write operations")
	}
	key := strings.TrimSpace(options.GetIdempotencyKey())
	if key == "" {
		return "", 0, status.Error(codes.InvalidArgument, "options.idempotency_key is required")
	}
	return key, options.GetExpectedVersion(), nil
}
