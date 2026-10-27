// Package http 提供运维面（healthz/metrics）与只读控制台面（/api/*）。
//
// 只读面刻意不引 gRPC-Web/Connect：M0 的 Next.js 控制台用服务端组件直读 JSON，
// 少一层协议转换，也少一份生成物。写操作仍然只走 gRPC（带鉴权与幂等）。
package http

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/observability"
	"github.com/xiaofeiwu/devmaster/backend/internal/product/application"
	"github.com/xiaofeiwu/devmaster/backend/internal/product/domain"
	workitemapplication "github.com/xiaofeiwu/devmaster/backend/internal/workitem/application"
	workitemdomain "github.com/xiaofeiwu/devmaster/backend/internal/workitem/domain"
)

type ProductReader interface {
	List(ctx context.Context, actor application.Actor, filter application.ListFilter) ([]domain.Product, int, error)
}

type WorkItemReader interface {
	List(ctx context.Context, actor workitemapplication.Actor, filter workitemapplication.ListFilter) ([]workitemdomain.WorkItem, int, error)
}

type Server struct {
	products  ProductReader
	workItems WorkItemReader
	metrics   *observability.Metrics
	health    observability.Health
	logger    *slog.Logger
	// defaultOrganizationID 只在 auth.enabled=false 时使用。
	// 生产环境必须由网关注入 X-Organization-Id，否则只读面会跨组织泄露。
	defaultOrganizationID uuid.UUID
}

func NewServer(products ProductReader, workItems WorkItemReader, metrics *observability.Metrics, health observability.Health, logger *slog.Logger, defaultOrganizationID uuid.UUID) *Server {
	return &Server{
		products:              products,
		workItems:             workItems,
		metrics:               metrics,
		health:                health,
		logger:                logger,
		defaultOrganizationID: defaultOrganizationID,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/healthz", s.health)
	mux.Handle("/metrics", s.metrics.Handler())
	mux.HandleFunc("/api/products", s.handleProducts)
	mux.HandleFunc("/api/work-items", s.handleWorkItems)
	return cors(mux)
}

type productView struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func (s *Server) handleProducts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	organizationID, err := s.organizationFrom(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	filter := application.ListFilter{}
	switch strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("status"))) {
	case "":
	case string(domain.StatusActive):
		filter.Status = domain.StatusActive
	case string(domain.StatusArchived):
		filter.Status = domain.StatusArchived
	default:
		http.Error(w, "status must be ACTIVE or ARCHIVED", http.StatusBadRequest)
		return
	}
	filter.Limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	filter.Offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))

	items, total, err := s.products.List(r.Context(), application.Actor{
		Subject:        "http-readonly",
		OrganizationID: organizationID,
	}, filter)
	if err != nil {
		s.logger.Error("list products failed", slog.String("error", err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]productView, 0, len(items))
	for _, item := range items {
		out = append(out, productView{
			ID:          item.ID.String(),
			Key:         item.Key,
			Name:        item.Name,
			Description: item.Description,
			Status:      string(item.Status),
			CreatedAt:   item.CreatedAt.Format(time.RFC3339),
			UpdatedAt:   item.UpdatedAt.Format(time.RFC3339),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": out, "total": total})
}

func (s *Server) organizationFrom(r *http.Request) (uuid.UUID, error) {
	if raw := strings.TrimSpace(r.Header.Get("X-Organization-Id")); raw != "" {
		return uuid.Parse(raw)
	}
	if s.defaultOrganizationID != uuid.Nil {
		return s.defaultOrganizationID, nil
	}
	return uuid.Nil, errors.New("missing X-Organization-Id")
}

// cors 只放行本机控制台。生产必须显式配置来源，不能用通配。
func cors(next http.Handler) http.Handler {
	allowed := map[string]bool{
		"http://127.0.0.1:3031": true,
		"http://localhost:3031": true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowed[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Organization-Id")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type workItemView struct {
	ID         string `json:"id"`
	Key        string `json:"key"`
	ProductID  string `json:"product_id"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	Resolution string `json:"resolution"`
	Title      string `json:"title"`
	Priority   string `json:"priority"`
	AssigneeID string `json:"assignee_id"`
	ReporterID string `json:"reporter_id"`
	Version    int32  `json:"version"`
	UpdatedAt  string `json:"updated_at"`
}

func (s *Server) handleWorkItems(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	organizationID, err := s.organizationFrom(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	filter := workitemapplication.ListFilter{
		Query: strings.TrimSpace(r.URL.Query().Get("q")),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("product_id")); raw != "" {
		productID, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			http.Error(w, "product_id must be a valid UUID", http.StatusBadRequest)
			return
		}
		filter.ProductID = &productID
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("type")); raw != "" {
		itemType, parseErr := workitemdomain.ParseType(raw)
		if parseErr != nil {
			http.Error(w, parseErr.Error(), http.StatusBadRequest)
			return
		}
		filter.Type = itemType
	}
	for _, raw := range strings.Split(r.URL.Query().Get("status"), ",") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		status, parseErr := workitemdomain.ParseStatus(raw)
		if parseErr != nil {
			http.Error(w, parseErr.Error(), http.StatusBadRequest)
			return
		}
		filter.Statuses = append(filter.Statuses, status)
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("assignee_id")); raw != "" {
		filter.AssigneeID = &raw
	}
	filter.Limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	filter.Offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))

	items, total, err := s.workItems.List(r.Context(), workitemapplication.Actor{
		Subject:        "http-readonly",
		OrganizationID: organizationID,
	}, filter)
	if err != nil {
		s.logger.Error("list work items failed", slog.String("error", err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]workItemView, 0, len(items))
	for _, item := range items {
		out = append(out, workItemView{
			ID:         item.ID.String(),
			Key:        item.Key,
			ProductID:  item.ProductID.String(),
			Type:       string(item.Type),
			Status:     string(item.Status),
			Resolution: resolutionOrEmpty(item.Resolution),
			Title:      item.Title,
			Priority:   priorityLabel(item.Priority),
			AssigneeID: stringOrEmpty(item.AssigneeID),
			ReporterID: item.ReporterID,
			Version:    int32(item.Version),
			UpdatedAt:  item.UpdatedAt.Format(time.RFC3339),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": out, "total": total})
}

func priorityLabel(value workitemdomain.Priority) string {
	return "P" + strconv.Itoa(int(value))
}

func resolutionOrEmpty(value *workitemdomain.Resolution) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
