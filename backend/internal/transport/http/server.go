// Package http 提供运维面（healthz/metrics）、控制台只读面（GET /api/*）
// 与一个写端点（POST /api/releases）。
//
// 读面刻意不引 gRPC-Web/Connect：Next.js 服务端组件直读 JSON，少一层协议转换，
// 也少一份生成物。
//
// 写端点只此一个，用 BFF 签名（HMAC + 时间戳）而不是用户 token：签名证明请求
// 来自我们自己的 Next 服务，用户身份由 BFF 从会话里取后写进请求体，客户端
// 无法伪造。其余写操作仍走 gRPC（带完整鉴权与幂等）。
package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	boardapp "github.com/xiaofeiwu/devmaster/backend/internal/board/application"
	giteaapp "github.com/xiaofeiwu/devmaster/backend/internal/gitea/application"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/observability"
	"github.com/xiaofeiwu/devmaster/backend/internal/product/application"
	"github.com/xiaofeiwu/devmaster/backend/internal/product/domain"
	reportapp "github.com/xiaofeiwu/devmaster/backend/internal/report/application"
	sprintapp "github.com/xiaofeiwu/devmaster/backend/internal/sprint/application"
	sprintdomain "github.com/xiaofeiwu/devmaster/backend/internal/sprint/domain"
	versionapp "github.com/xiaofeiwu/devmaster/backend/internal/version/application"
	versiondomain "github.com/xiaofeiwu/devmaster/backend/internal/version/domain"
	workitemapplication "github.com/xiaofeiwu/devmaster/backend/internal/workitem/application"
	workitemdomain "github.com/xiaofeiwu/devmaster/backend/internal/workitem/domain"
	worklogapplication "github.com/xiaofeiwu/devmaster/backend/internal/worklog/application"
	worklogdomain "github.com/xiaofeiwu/devmaster/backend/internal/worklog/domain"
)

type ProductReader interface {
	List(ctx context.Context, actor application.Actor, filter application.ListFilter) ([]domain.Product, int, error)
}

type WorkItemReader interface {
	List(ctx context.Context, actor workitemapplication.Actor, filter workitemapplication.ListFilter) ([]workitemdomain.WorkItem, int, error)
	Get(ctx context.Context, actor workitemapplication.Actor, id uuid.UUID) (workitemdomain.WorkItem, error)
	GetByKey(ctx context.Context, actor workitemapplication.Actor, key string) (workitemdomain.WorkItem, error)
	ListComments(ctx context.Context, actor workitemapplication.Actor, workItemID uuid.UUID) ([]workitemdomain.Comment, error)
	ListLinks(ctx context.Context, actor workitemapplication.Actor, workItemID uuid.UUID) ([]workitemdomain.Link, error)
	ListEvents(ctx context.Context, actor workitemapplication.Actor, workItemID uuid.UUID) ([]workitemapplication.EventRecord, error)
}

// WorkItemWriter 是工作项的写端口：创建、编辑、指派、流转。
type WorkItemWriter interface {
	Create(ctx context.Context, actor workitemapplication.Actor, cmd workitemapplication.CreateCommand) (workitemdomain.WorkItem, error)
	Update(ctx context.Context, actor workitemapplication.Actor, cmd workitemapplication.UpdateCommand) (workitemdomain.WorkItem, error)
	Assign(ctx context.Context, actor workitemapplication.Actor, cmd workitemapplication.AssignCommand) (workitemdomain.WorkItem, error)
	Transition(ctx context.Context, actor workitemapplication.Actor, cmd workitemapplication.TransitionCommand) (workitemdomain.WorkItem, error)
	AddComment(ctx context.Context, actor workitemapplication.Actor, workItemID uuid.UUID, body string) (workitemdomain.Comment, error)
	AddLink(ctx context.Context, actor workitemapplication.Actor, fromID, toID uuid.UUID, linkType workitemdomain.LinkType) (workitemdomain.Link, error)
	RemoveLink(ctx context.Context, actor workitemapplication.Actor, fromID, toID uuid.UUID, linkType workitemdomain.LinkType) error
}

type SprintReader interface {
	List(ctx context.Context, actor sprintapp.Actor, productID uuid.UUID) ([]sprintdomain.Sprint, error)
}

type BoardReader interface {
	Get(ctx context.Context, actor boardapp.Actor, productID uuid.UUID, sprintID *uuid.UUID) (boardapp.BoardView, error)
}

type VersionReader interface {
	List(ctx context.Context, actor versionapp.Actor, productID uuid.UUID) ([]versiondomain.Version, error)
}

// ReleaseSubmitter 是提交发布的写端口。
type ReleaseSubmitter interface {
	SubmitRelease(ctx context.Context, actor versionapp.Actor, cmd versionapp.SubmitReleaseCommand) (versiondomain.ReleaseRecord, error)
}

// WorkLogStore 是工时的读写端口。
//
// 工时是 append-only：没有「修改」，只有「记录」和「冲正」（追加一条反向记录）。
type WorkLogStore interface {
	List(ctx context.Context, actor worklogapplication.Actor, filter worklogapplication.ListFilter) ([]worklogdomain.WorkLog, int, error)
	Sum(ctx context.Context, actor worklogapplication.Actor, filter worklogapplication.ListFilter) (int, error)
	Log(ctx context.Context, actor worklogapplication.Actor, cmd worklogapplication.LogCommand) (worklogdomain.WorkLog, error)
	Reverse(ctx context.Context, actor worklogapplication.Actor, workLogID uuid.UUID, minutes *int, note *string) (worklogdomain.WorkLog, error)
}

// SubjectResolver 是主体目录的只读端口（解析 keycloak/<sub> 到锚点）。
type SubjectResolver interface {
	ResolveSubject(ctx context.Context, sourceSystem, externalID string) (anchorID string, found bool, err error)
}

// ConsoleDeps 是控制台 BFF 通道的依赖：写端点与身份解析。
// 打包传参，以后加控制台能力不必再改构造器签名。
type ConsoleDeps struct {
	Releases  ReleaseSubmitter
	WorkItems WorkItemWriter
	WorkLogs  WorkLogStore
	Subjects  SubjectResolver
	BFFSecret string
}

type ReportReader interface {
	CycleTime(ctx context.Context, actor reportapp.Actor, productID *uuid.UUID, from, to time.Time) ([]reportapp.CycleSummary, error)
	Throughput(ctx context.Context, actor reportapp.Actor, productID *uuid.UUID, from, to time.Time) ([]reportapp.ThroughputBucket, error)
	TeamLoad(ctx context.Context, actor reportapp.Actor, productID *uuid.UUID) ([]reportapp.LoadRow, error)
	CFD(ctx context.Context, actor reportapp.Actor, productID *uuid.UUID, from, to time.Time) ([]reportapp.CFDPoint, error)
}

type GiteaInbound interface {
	RecordDelivery(ctx context.Context, organizationID uuid.UUID, deliveryID, eventType string, payload []byte) (bool, error)
	HandlePush(ctx context.Context, organizationID uuid.UUID, repo, branch string, commits []giteaapp.Commit) (int, error)
	HandlePullRequest(ctx context.Context, organizationID uuid.UUID, repo string, pr giteaapp.PullRequest) (int, error)
}

type Server struct {
	products    ProductReader
	workItems   WorkItemReader
	sprints     SprintReader
	board       BoardReader
	versions    VersionReader
	releases    ReleaseSubmitter
	workItemsW  WorkItemWriter
	workLogs    WorkLogStore
	subjects    SubjectResolver
	bffSecret   string
	reports     ReportReader
	gitea       GiteaInbound
	giteaSecret string
	giteaOrg    uuid.UUID
	metrics     *observability.Metrics
	health      observability.Health
	logger      *slog.Logger
	// defaultOrganizationID 只在 auth.enabled=false 时使用。
	// 生产环境必须由网关注入 X-Organization-Id，否则只读面会跨组织泄露。
	defaultOrganizationID uuid.UUID
}

func NewServer(products ProductReader, workItems WorkItemReader, sprints SprintReader, board BoardReader, versions VersionReader, reports ReportReader, giteaInbound GiteaInbound, giteaSecret string, giteaOrg uuid.UUID, metrics *observability.Metrics, health observability.Health, logger *slog.Logger, defaultOrganizationID uuid.UUID, console ConsoleDeps) *Server {
	return &Server{
		products:              products,
		workItems:             workItems,
		sprints:               sprints,
		board:                 board,
		versions:              versions,
		releases:              console.Releases,
		workItemsW:            console.WorkItems,
		workLogs:              console.WorkLogs,
		subjects:              console.Subjects,
		bffSecret:             console.BFFSecret,
		reports:               reports,
		gitea:                 giteaInbound,
		giteaSecret:           giteaSecret,
		giteaOrg:              giteaOrg,
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
	mux.HandleFunc("POST /api/work-items", s.handleCreateWorkItem)
	mux.HandleFunc("GET /api/work-items/{key}", s.handleWorkItemDetail)
	mux.HandleFunc("PATCH /api/work-items/{key}", s.handleUpdateWorkItem)
	mux.HandleFunc("POST /api/work-items/{key}/comments", s.handleAddComment)
	mux.HandleFunc("POST /api/work-items/{key}/links", s.handleAddLink)
	mux.HandleFunc("DELETE /api/work-items/{key}/links", s.handleRemoveLink)
	mux.HandleFunc("/api/work-items/{key}/transition", s.handleTransitionWorkItem)
	mux.HandleFunc("/api/sprints", s.handleSprints)
	mux.HandleFunc("/api/board", s.handleBoard)
	mux.HandleFunc("/api/versions", s.handleVersions)
	mux.HandleFunc("/api/releases", s.handleSubmitRelease)
	mux.HandleFunc("/api/worklogs", s.handleWorkLogs)
	mux.HandleFunc("POST /api/worklogs/{id}/reverse", s.handleReverseWorkLog)
	mux.HandleFunc("/api/subjects/resolve", s.handleResolveSubject)
	mux.HandleFunc("/api/reports/cycle-time", s.handleCycleTime)
	mux.HandleFunc("/api/reports/throughput", s.handleThroughput)
	mux.HandleFunc("/api/reports/team-load", s.handleTeamLoad)
	mux.HandleFunc("/api/reports/cfd", s.handleCFD)
	mux.HandleFunc("/integrations/gitea/webhook", s.handleGiteaWebhook)
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
	// NextStatuses 是当前状态下允许流转到的目标，前端据此渲染按钮。
	// 由领域状态机给出，不在前端硬编码——否则状态机一改前端就骗人。
	NextStatuses []string `json:"next_statuses"`
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
			ID:           item.ID.String(),
			Key:          item.Key,
			ProductID:    item.ProductID.String(),
			Type:         string(item.Type),
			Status:       string(item.Status),
			Resolution:   resolutionOrEmpty(item.Resolution),
			Title:        item.Title,
			Priority:     priorityLabel(item.Priority),
			AssigneeID:   stringOrEmpty(item.AssigneeID),
			ReporterID:   item.ReporterID,
			Version:      int32(item.Version),
			UpdatedAt:    item.UpdatedAt.Format(time.RFC3339),
			NextStatuses: statusNames(workitemdomain.AllowedTransitions(item.Type, item.Status)),
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

type sprintView struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Goal            string `json:"goal"`
	State           string `json:"state"`
	StartAt         string `json:"start_at"`
	EndAt           string `json:"end_at"`
	CommittedPoints int32  `json:"committed_points"`
	Version         int32  `json:"version"`
}

func (s *Server) handleSprints(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	organizationID, err := s.organizationFrom(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	productID, err := uuid.Parse(strings.TrimSpace(r.URL.Query().Get("product_id")))
	if err != nil {
		http.Error(w, "product_id must be a valid UUID", http.StatusBadRequest)
		return
	}
	sprints, err := s.sprints.List(r.Context(), sprintapp.Actor{
		Subject:        "http-readonly",
		OrganizationID: organizationID,
	}, productID)
	if err != nil {
		s.logger.Error("list sprints failed", slog.String("error", err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]sprintView, 0, len(sprints))
	for _, sprint := range sprints {
		out = append(out, sprintView{
			ID:              sprint.ID.String(),
			Name:            sprint.Name,
			Goal:            sprint.Goal,
			State:           string(sprint.State),
			StartAt:         formatTime(sprint.StartAt),
			EndAt:           formatTime(sprint.EndAt),
			CommittedPoints: int32(sprint.CommittedPoints),
			Version:         int32(sprint.Version),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": out})
}

type boardItemView struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Title       string `json:"title"`
	Type        string `json:"type"`
	Status      string `json:"status"`
	Priority    string `json:"priority"`
	AssigneeID  string `json:"assignee_id"`
	StoryPoints int32  `json:"story_points"`
	Rank        string `json:"rank"`
	Version     int32  `json:"version"`
}

type boardColumnView struct {
	Column    string          `json:"column"`
	Statuses  []string        `json:"statuses"`
	WorkItems []boardItemView `json:"work_items"`
}

func (s *Server) handleBoard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	organizationID, err := s.organizationFrom(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	productID, err := uuid.Parse(strings.TrimSpace(r.URL.Query().Get("product_id")))
	if err != nil {
		http.Error(w, "product_id must be a valid UUID", http.StatusBadRequest)
		return
	}
	var sprintID *uuid.UUID
	if raw := strings.TrimSpace(r.URL.Query().Get("sprint_id")); raw != "" {
		parsed, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			http.Error(w, "sprint_id must be a valid UUID", http.StatusBadRequest)
			return
		}
		sprintID = &parsed
	}
	view, err := s.board.Get(r.Context(), boardapp.Actor{
		Subject:        "http-readonly",
		OrganizationID: organizationID,
	}, productID, sprintID)
	if err != nil {
		s.logger.Error("get board failed", slog.String("error", err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	response := map[string]any{"columns": []boardColumnView{}}
	if view.Sprint != nil {
		response["sprint"] = sprintView{
			ID:              view.Sprint.ID.String(),
			Name:            view.Sprint.Name,
			Goal:            view.Sprint.Goal,
			State:           string(view.Sprint.State),
			StartAt:         formatTime(view.Sprint.StartAt),
			EndAt:           formatTime(view.Sprint.EndAt),
			CommittedPoints: int32(view.Sprint.CommittedPoints),
			Version:         int32(view.Sprint.Version),
		}
	}
	columns := make([]boardColumnView, 0, len(view.Columns))
	for _, column := range view.Columns {
		items := make([]boardItemView, 0, len(column.Items))
		for _, item := range column.Items {
			points := int32(0)
			if item.StoryPoints != nil {
				points = int32(*item.StoryPoints)
			}
			items = append(items, boardItemView{
				ID:          item.ID.String(),
				Key:         item.Key,
				Title:       item.Title,
				Type:        string(item.Type),
				Status:      string(item.Status),
				Priority:    priorityLabel(item.Priority),
				AssigneeID:  stringOrEmpty(item.AssigneeID),
				StoryPoints: points,
				Rank:        item.Rank,
				Version:     int32(item.Version),
			})
		}
		columns = append(columns, boardColumnView{
			Column:    string(column.Column),
			Statuses:  column.Statuses,
			WorkItems: items,
		})
	}
	response["columns"] = columns
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func formatTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.Format(time.RFC3339)
}

type versionView struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	MilestoneLabel string `json:"milestone_label"`
	PlannedDate    string `json:"planned_date"`
	ReleasedAt     string `json:"released_at"`
	ScopeFrozen    bool   `json:"scope_frozen"`
}

func (s *Server) handleVersions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	organizationID, err := s.organizationFrom(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	productID, err := uuid.Parse(strings.TrimSpace(r.URL.Query().Get("product_id")))
	if err != nil {
		http.Error(w, "product_id must be a valid UUID", http.StatusBadRequest)
		return
	}
	versions, err := s.versions.List(r.Context(), versionapp.Actor{
		Subject:        "http-readonly",
		OrganizationID: organizationID,
	}, productID)
	if err != nil {
		s.logger.Error("list versions failed", slog.String("error", err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]versionView, 0, len(versions))
	for _, version := range versions {
		view := versionView{
			ID:          version.ID.String(),
			Name:        version.Name,
			Status:      string(version.Status),
			ScopeFrozen: version.ScopeFrozenAt != nil,
		}
		if version.MilestoneLabel != nil {
			view.MilestoneLabel = *version.MilestoneLabel
		}
		if version.PlannedDate != nil {
			view.PlannedDate = version.PlannedDate.Format("2006-01-02")
		}
		if version.ReleasedAt != nil {
			view.ReleasedAt = version.ReleasedAt.Format(time.RFC3339)
		}
		out = append(out, view)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": out})
}

type giteaPushPayload struct {
	Ref        string `json:"ref"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Commits []struct {
		ID        string    `json:"id"`
		Message   string    `json:"message"`
		Timestamp time.Time `json:"timestamp"`
		Author    struct {
			Username string `json:"username"`
		} `json:"author"`
	} `json:"commits"`
}

type giteaPullRequestPayload struct {
	Action      string `json:"action"`
	Number      int    `json:"number"`
	PullRequest struct {
		Number int    `json:"number"`
		State  string `json:"state"`
		Merged bool   `json:"merged"`
		Head   struct {
			Ref string `json:"ref"`
		} `json:"head"`
	} `json:"pull_request"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

// handleGiteaWebhook 校验 HMAC 后把 push/PR 事件落成提交关联。
//
// 幂等键是 X-Gitea-Delivery：Gitea 重投同一条 delivery 时直接 200，
// 不重复写关联，也不重复推进任何状态。
func (s *Server) handleGiteaWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.gitea == nil || s.giteaSecret == "" {
		http.Error(w, "gitea webhook is not configured", http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	if err != nil {
		http.Error(w, "cannot read body", http.StatusBadRequest)
		return
	}
	if !validGiteaSignature(s.giteaSecret, body, r.Header.Get("X-Gitea-Signature")) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	eventType := r.Header.Get("X-Gitea-Event")
	deliveryID := strings.TrimSpace(r.Header.Get("X-Gitea-Delivery"))
	if deliveryID == "" {
		hash := sha256.Sum256(body)
		deliveryID = hex.EncodeToString(hash[:])
	}
	inserted, err := s.gitea.RecordDelivery(r.Context(), s.giteaOrg, deliveryID, eventType, body)
	if err != nil {
		s.logger.Error("record gitea delivery failed", slog.String("error", err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !inserted {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "duplicate": true})
		return
	}

	affected := 0
	switch eventType {
	case "push":
		var payload giteaPushPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "invalid push payload", http.StatusBadRequest)
			return
		}
		branch := strings.TrimPrefix(payload.Ref, "refs/heads/")
		commits := make([]giteaapp.Commit, 0, len(payload.Commits))
		for _, commit := range payload.Commits {
			commits = append(commits, giteaapp.Commit{
				SHA:         commit.ID,
				Message:     commit.Message,
				Branch:      branch,
				CommittedAt: commit.Timestamp,
				AuthorID:    commit.Author.Username,
			})
		}
		affected, err = s.gitea.HandlePush(r.Context(), s.giteaOrg, payload.Repository.FullName, branch, commits)
	case "pull_request":
		var payload giteaPullRequestPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "invalid pull_request payload", http.StatusBadRequest)
			return
		}
		state := strings.ToUpper(payload.PullRequest.State)
		if payload.PullRequest.Merged {
			state = "MERGED"
		}
		affected, err = s.gitea.HandlePullRequest(r.Context(), s.giteaOrg, payload.Repository.FullName, giteaapp.PullRequest{
			Number:     payload.PullRequest.Number,
			State:      state,
			HeadBranch: payload.PullRequest.Head.Ref,
		})
	default:
		// create/ping 等事件只记投递，不改变关联。
	}
	if err != nil {
		s.logger.Error("handle gitea event failed",
			slog.String("event", eventType), slog.String("error", err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "affected": affected})
}

func validGiteaSignature(secret string, body []byte, signature string) bool {
	provided, err := hex.DecodeString(strings.TrimSpace(signature))
	if err != nil || len(provided) == 0 {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hmac.Equal(mac.Sum(nil), provided)
}

// handleSubmitRelease 是控制台唯一的写端点：提交一次发布。
//
// 校验顺序刻意是「先签名后解析」：未签名的请求连 body 都不读，
// 不给探测者任何解析反馈。
func (s *Server) handleSubmitRelease(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}
	if !s.validBFFSignature(r, body) {
		http.Error(w, "invalid bff signature", http.StatusUnauthorized)
		return
	}
	var payload struct {
		VersionID   string `json:"version_id"`
		CommitFrom  string `json:"commit_from"`
		CommitTo    string `json:"commit_to"`
		SubmittedBy string `json:"submitted_by"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	versionID, err := uuid.Parse(strings.TrimSpace(payload.VersionID))
	if err != nil {
		http.Error(w, "version_id must be a valid UUID", http.StatusBadRequest)
		return
	}
	submittedBy := strings.TrimSpace(payload.SubmittedBy)
	if submittedBy == "" {
		http.Error(w, "submitted_by is required", http.StatusBadRequest)
		return
	}
	organizationID, err := s.organizationFrom(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cmd := versionapp.SubmitReleaseCommand{VersionID: versionID}
	if from := strings.TrimSpace(payload.CommitFrom); from != "" {
		cmd.CommitFrom = &from
	}
	if to := strings.TrimSpace(payload.CommitTo); to != "" {
		cmd.CommitTo = &to
	}
	release, err := s.releases.SubmitRelease(r.Context(), versionapp.Actor{
		Subject: submittedBy, OrganizationID: organizationID,
	}, cmd)
	if err != nil {
		s.writeAppError(w, "submit release failed", err)
		return
	}
	s.logger.Info("release submitted via console",
		slog.String("release_id", release.ID.String()),
		slog.String("version_id", versionID.String()),
		slog.String("submitted_by", submittedBy))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"release_id": release.ID.String(),
		"status":     string(release.Status),
	})
}

// validBFFSignature 校验 Next 服务端的 HMAC 签名。
//
// 未配置密钥时一律拒绝：写端点不能有「没配就放行」的降级，
// 那是把一次配置遗漏变成永久后门。
func (s *Server) validBFFSignature(r *http.Request, body []byte) bool {
	if strings.TrimSpace(s.bffSecret) == "" {
		return false
	}
	timestamp := strings.TrimSpace(r.Header.Get("X-BFF-Timestamp"))
	signature := strings.TrimSpace(r.Header.Get("X-BFF-Signature"))
	unix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	// 5 分钟窗口：写请求的重放代价比读大得多，时间戳进签名防篡改。
	if delta := time.Since(time.Unix(unix, 0)); delta > 5*time.Minute || delta < -5*time.Minute {
		return false
	}
	mac := hmac.New(sha256.New, []byte(s.bffSecret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("\n"))
	mac.Write(body)
	provided, err := hex.DecodeString(signature)
	if err != nil || len(provided) == 0 {
		return false
	}
	return hmac.Equal(mac.Sum(nil), provided)
}

// writeAppError 把领域错误翻译成 HTTP 状态码，语义与 gRPC 面一致。
func (s *Server) writeAppError(w http.ResponseWriter, logMessage string, err error) {
	kind, ok := apperr.KindOf(err)
	if !ok {
		s.logger.Error(logMessage, slog.String("error", err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	switch kind {
	case apperr.KindInvalid:
		http.Error(w, err.Error(), http.StatusBadRequest)
	case apperr.KindNotFound:
		http.Error(w, err.Error(), http.StatusNotFound)
	case apperr.KindConflict:
		http.Error(w, err.Error(), http.StatusConflict)
	case apperr.KindFailedPrecondition:
		// 422 而不是 400：请求本身合法，是当前状态不允许（如已有在途发布）。
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
	default:
		s.logger.Error(logMessage, slog.String("error", err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// handleResolveSubject 把 keycloak/<sub> 解析成主体锚点。
//
// 为什么要过这一道而不是让浏览器直接问 subject-registry：registry 只开 gRPC，
// 浏览器调不到；而且解析需要 service token，那个令牌不能下发到前端。
//
// 未接入主体目录时返回 found=false 而不是错误——调用方据此退化为「只知道 sub」，
// 收件箱少一个 user: 键，但页面照常打开。
func (s *Server) handleResolveSubject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// GET 没有 body，签名覆盖空 body——时间戳仍在签名里，防重放。
	if !s.validBFFSignature(r, nil) {
		http.Error(w, "invalid bff signature", http.StatusUnauthorized)
		return
	}
	sourceSystem := strings.TrimSpace(r.URL.Query().Get("source"))
	externalID := strings.TrimSpace(r.URL.Query().Get("external_id"))
	if sourceSystem == "" || externalID == "" {
		http.Error(w, "source and external_id are required", http.StatusBadRequest)
		return
	}
	if s.subjects == nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"found": false, "anchor_id": ""})
		return
	}
	anchorID, found, err := s.subjects.ResolveSubject(r.Context(), sourceSystem, externalID)
	if err != nil {
		// 目录故障不阻塞页面：返回 found=false，调用方退化。
		s.logger.Warn("resolve subject failed",
			slog.String("source", sourceSystem), slog.String("external_id", externalID),
			slog.String("error", err.Error()))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"found": false, "anchor_id": ""})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"found": found, "anchor_id": anchorID})
}

// handleWorkLogs：GET 列表+汇总，POST 记录一条工时。
func (s *Server) handleWorkLogs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listWorkLogs(w, r)
	case http.MethodPost:
		s.createWorkLog(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// listWorkLogs 一次返回列表与汇总：工时页必然两个都要，拆开就是两次往返。
func (s *Server) listWorkLogs(w http.ResponseWriter, r *http.Request) {
	organizationID, err := s.organizationFrom(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	filter := worklogapplication.ListFilter{}
	if author := strings.TrimSpace(r.URL.Query().Get("author_id")); author != "" {
		filter.AuthorID = &author
	}
	if from := strings.TrimSpace(r.URL.Query().Get("from")); from != "" {
		parsed, parseErr := time.Parse("2006-01-02", from)
		if parseErr != nil {
			http.Error(w, "from must be YYYY-MM-DD", http.StatusBadRequest)
			return
		}
		filter.From = &parsed
	}
	if to := strings.TrimSpace(r.URL.Query().Get("to")); to != "" {
		parsed, parseErr := time.Parse("2006-01-02", to)
		if parseErr != nil {
			http.Error(w, "to must be YYYY-MM-DD", http.StatusBadRequest)
			return
		}
		// 结束日含当天：前端传 to=2026-10-28 时期望包含 28 号整天。
		end := parsed.Add(24 * time.Hour)
		filter.To = &end
	}
	filter.Limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	filter.Offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))

	actor := worklogapplication.Actor{Subject: "http-readonly", OrganizationID: organizationID}
	logs, total, err := s.workLogs.List(r.Context(), actor, filter)
	if err != nil {
		s.writeAppError(w, "list work logs failed", err)
		return
	}
	sum, err := s.workLogs.Sum(r.Context(), actor, filter)
	if err != nil {
		s.writeAppError(w, "sum work logs failed", err)
		return
	}
	views := make([]map[string]any, 0, len(logs))
	for _, entry := range logs {
		view := map[string]any{
			"id":            entry.ID.String(),
			"work_item_id":  entry.WorkItemID.String(),
			"work_item_key": entry.WorkItemKey,
			"author_id":     entry.AuthorID,
			"minutes":       entry.Minutes,
			"work_date":     entry.WorkDate.Format("2006-01-02"),
			"created_at":    entry.CreatedAt.Format(time.RFC3339),
			"source":        string(entry.Source),
			"is_reversal":   entry.ReversalOf != nil,
		}
		if entry.Note != nil {
			view["note"] = *entry.Note
		}
		views = append(views, view)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items":         views,
		"total":         total,
		"total_minutes": sum,
	})
}

// createWorkLog 记一条工时。作者取自签名覆盖的 actor。
func (s *Server) createWorkLog(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<10))
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}
	if !s.validBFFSignature(r, body) {
		http.Error(w, "invalid bff signature", http.StatusUnauthorized)
		return
	}
	var payload struct {
		WorkItemKey string `json:"work_item_key"`
		Minutes     int    `json:"minutes"`
		WorkDate    string `json:"work_date"`
		Note        string `json:"note"`
		Actor       string `json:"actor"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	actor := strings.TrimSpace(payload.Actor)
	if actor == "" {
		http.Error(w, "actor is required", http.StatusBadRequest)
		return
	}
	workDate, err := time.Parse("2006-01-02", strings.TrimSpace(payload.WorkDate))
	if err != nil {
		http.Error(w, "work_date must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	organizationID, err := s.organizationFrom(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cmd := worklogapplication.LogCommand{
		WorkItemKey: strings.TrimSpace(payload.WorkItemKey),
		Minutes:     payload.Minutes,
		WorkDate:    workDate,
		Source:      worklogdomain.SourceManual,
	}
	if note := strings.TrimSpace(payload.Note); note != "" {
		cmd.Note = &note
	}
	entry, err := s.workLogs.Log(r.Context(), worklogapplication.Actor{
		Subject: actor, OrganizationID: organizationID,
	}, cmd)
	if err != nil {
		s.writeAppError(w, "log work failed", err)
		return
	}
	s.logger.Info("work logged via console",
		slog.String("work_log_id", entry.ID.String()),
		slog.String("work_item_key", entry.WorkItemKey),
		slog.Int("minutes", entry.Minutes),
		slog.String("actor", actor))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":            entry.ID.String(),
		"work_item_key": entry.WorkItemKey,
		"minutes":       entry.Minutes,
	})
}

// handleReverseWorkLog 冲正一条工时：追加一条反向记录，不修改原记录。
func (s *Server) handleReverseWorkLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<10))
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}
	if !s.validBFFSignature(r, body) {
		http.Error(w, "invalid bff signature", http.StatusUnauthorized)
		return
	}
	workLogID, err := uuid.Parse(strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		http.Error(w, "work log id must be a valid UUID", http.StatusBadRequest)
		return
	}
	var payload struct {
		Minutes *int   `json:"minutes"`
		Note    string `json:"note"`
		Actor   string `json:"actor"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	actor := strings.TrimSpace(payload.Actor)
	if actor == "" {
		http.Error(w, "actor is required", http.StatusBadRequest)
		return
	}
	note := strings.TrimSpace(payload.Note)
	if note == "" {
		http.Error(w, "note is required: 冲正必须说明原因", http.StatusBadRequest)
		return
	}
	organizationID, err := s.organizationFrom(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	entry, err := s.workLogs.Reverse(r.Context(), worklogapplication.Actor{
		Subject: actor, OrganizationID: organizationID,
	}, workLogID, payload.Minutes, &note)
	if err != nil {
		s.writeAppError(w, "reverse work log failed", err)
		return
	}
	s.logger.Info("work log reversed via console",
		slog.String("work_log_id", entry.ID.String()),
		slog.Int("minutes", entry.Minutes),
		slog.String("actor", actor))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":      entry.ID.String(),
		"minutes": entry.Minutes,
	})
}

// handleCreateWorkItem 从控制台建工作项。
//
// key 与 rank 由 repo.ReserveIdentity 分配，reporter 取自签名覆盖的 actor：
// 这两件事都不能由客户端决定，否则谁都能把工作项挂到别人名下。
func (s *Server) handleCreateWorkItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}
	if !s.validBFFSignature(r, body) {
		http.Error(w, "invalid bff signature", http.StatusUnauthorized)
		return
	}
	var payload struct {
		ProductID   string `json:"product_id"`
		Type        string `json:"type"`
		Title       string `json:"title"`
		Description string `json:"description_md"`
		Priority    *int   `json:"priority"`
		AssigneeID  string `json:"assignee_id"`
		VersionID   string `json:"version_id"`
		Actor       string `json:"actor"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	productID, err := uuid.Parse(strings.TrimSpace(payload.ProductID))
	if err != nil {
		http.Error(w, "product_id must be a valid UUID", http.StatusBadRequest)
		return
	}
	itemType, err := workitemdomain.ParseType(payload.Type)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(payload.Title)
	if title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	actor := strings.TrimSpace(payload.Actor)
	if actor == "" {
		http.Error(w, "actor is required", http.StatusBadRequest)
		return
	}
	// 默认 P2：建单时多数人不关心优先级，逼他们选只会得到一个随手选的值。
	priority := workitemdomain.PriorityP2
	if payload.Priority != nil {
		priority, err = workitemdomain.ParsePriority(int16(*payload.Priority))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	cmd := workitemapplication.CreateCommand{
		ProductID:     productID,
		Type:          itemType,
		Title:         title,
		DescriptionMD: strings.TrimSpace(payload.Description),
		Priority:      priority,
	}
	if assignee := strings.TrimSpace(payload.AssigneeID); assignee != "" {
		cmd.AssigneeID = &assignee
	}
	if raw := strings.TrimSpace(payload.VersionID); raw != "" {
		versionID, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			http.Error(w, "version_id must be a valid UUID", http.StatusBadRequest)
			return
		}
		cmd.VersionID = &versionID
	}
	organizationID, err := s.organizationFrom(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	item, err := s.workItemsW.Create(r.Context(), workitemapplication.Actor{
		Subject: actor, OrganizationID: organizationID,
	}, cmd)
	if err != nil {
		s.writeAppError(w, "create work item failed", err)
		return
	}
	s.logger.Info("work item created via console",
		slog.String("work_item_id", item.ID.String()),
		slog.String("key", item.Key),
		slog.String("actor", actor))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":      item.ID.String(),
		"key":     item.Key,
		"status":  string(item.Status),
		"version": int32(item.Version),
	})
}

// handleUpdateWorkItem 编辑标题/优先级/描述，并可改经办人。
//
// 经办人走 Assign 而不是 Update：领域里指派是独立动作（有自己的守卫与事件），
// 塞进 UpdateFields 会把两件事的审计混在一起。这里按顺序调两次，
// 第二次用第一次返回的版本——两次写都必须过 CAS。
func (s *Server) handleUpdateWorkItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}
	if !s.validBFFSignature(r, body) {
		http.Error(w, "invalid bff signature", http.StatusUnauthorized)
		return
	}
	var payload struct {
		// 指针：新建的工作项版本就是 0，用 int 无法区分「没传」和「传了 0」。
		ExpectedVersion *int    `json:"expected_version"`
		Title           *string `json:"title"`
		Description     *string `json:"description_md"`
		Priority        *int    `json:"priority"`
		// AssigneeID 显式给出才处理：空串表示取消指派，缺省表示不动。
		AssigneeID *string `json:"assignee_id"`
		Actor      string  `json:"actor"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	actor := strings.TrimSpace(payload.Actor)
	if actor == "" {
		http.Error(w, "actor is required", http.StatusBadRequest)
		return
	}
	if payload.ExpectedVersion == nil {
		http.Error(w, "expected_version is required", http.StatusBadRequest)
		return
	}
	existing, actorRef, err := s.workItemByKey(r, actor)
	if err != nil {
		s.writeAppError(w, "resolve work item failed", err)
		return
	}
	workItemID := existing.ID

	fields := workitemdomain.UpdateFields{}
	if payload.Title != nil {
		fields.Title = payload.Title
	}
	if payload.Description != nil {
		fields.DescriptionMD = payload.Description
	}
	if payload.Priority != nil {
		priority, parseErr := workitemdomain.ParsePriority(int16(*payload.Priority))
		if parseErr != nil {
			http.Error(w, parseErr.Error(), http.StatusBadRequest)
			return
		}
		fields.Priority = &priority
	}

	item, err := s.workItemsW.Update(r.Context(), actorRef, workitemapplication.UpdateCommand{
		WorkItemID:      workItemID,
		ExpectedVersion: *payload.ExpectedVersion,
		Fields:          fields,
	})
	if err != nil {
		s.writeAppError(w, "update work item failed", err)
		return
	}
	if payload.AssigneeID != nil {
		var assignee *string
		if trimmed := strings.TrimSpace(*payload.AssigneeID); trimmed != "" {
			assignee = &trimmed
		}
		item, err = s.workItemsW.Assign(r.Context(), actorRef, workitemapplication.AssignCommand{
			WorkItemID:      workItemID,
			AssigneeID:      assignee,
			ExpectedVersion: int(item.Version),
		})
		if err != nil {
			s.writeAppError(w, "assign work item failed", err)
			return
		}
	}
	s.logger.Info("work item updated via console",
		slog.String("work_item_id", workItemID.String()),
		slog.String("actor", actor))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":      item.ID.String(),
		"status":  string(item.Status),
		"version": int32(item.Version),
	})
}

// handleWorkItemDetail 一次返回详情页需要的全部内容。
//
// 不做「先拿 item 再分别拿评论/关联/事件」的四次往返：详情页必然都要，
// 拆开只会让页面变慢、错误态变多。代价是单个 handler 稍长，值得。
func (s *Server) handleWorkItemDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	organizationID, err := s.organizationFrom(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	actor := workitemapplication.Actor{Subject: "http-readonly", OrganizationID: organizationID}
	key := strings.TrimSpace(r.PathValue("key"))
	item, err := s.workItems.GetByKey(r.Context(), actor, key)
	if err != nil {
		s.writeAppError(w, "get work item failed", err)
		return
	}
	comments, err := s.workItems.ListComments(r.Context(), actor, item.ID)
	if err != nil {
		s.writeAppError(w, "list comments failed", err)
		return
	}
	links, err := s.workItems.ListLinks(r.Context(), actor, item.ID)
	if err != nil {
		s.writeAppError(w, "list links failed", err)
		return
	}
	events, err := s.workItems.ListEvents(r.Context(), actor, item.ID)
	if err != nil {
		s.writeAppError(w, "list events failed", err)
		return
	}

	commentViews := make([]map[string]any, 0, len(comments))
	for _, comment := range comments {
		commentViews = append(commentViews, map[string]any{
			"id":         comment.ID.String(),
			"author_id":  comment.AuthorID,
			"body_md":    comment.BodyMD,
			"created_at": comment.CreatedAt.Format(time.RFC3339),
		})
	}
	// 关联分出入向，并补上另一端的 key/标题/状态——只给 UUID 的列表没法读。
	outgoing := make([]map[string]any, 0, len(links))
	incoming := make([]map[string]any, 0, len(links))
	for _, link := range links {
		otherID := link.ToID
		direction := "outgoing"
		if link.ToID == item.ID {
			otherID = link.FromID
			direction = "incoming"
		}
		other, getErr := s.workItems.Get(r.Context(), actor, otherID)
		if getErr != nil {
			continue
		}
		entry := map[string]any{
			"type":         string(link.Type),
			"other_id":     other.ID.String(),
			"other_key":    other.Key,
			"other_title":  other.Title,
			"other_status": string(other.Status),
		}
		if direction == "outgoing" {
			outgoing = append(outgoing, entry)
		} else {
			incoming = append(incoming, entry)
		}
	}
	eventViews := make([]map[string]any, 0, len(events))
	for _, event := range events {
		entry := map[string]any{
			"event_type":  event.EventType,
			"actor_id":    event.ActorID,
			"occurred_at": event.OccurredAt.Format(time.RFC3339),
			"payload":     event.Payload,
		}
		if event.FromStatus != nil {
			entry["from_status"] = string(*event.FromStatus)
		}
		if event.ToStatus != nil {
			entry["to_status"] = string(*event.ToStatus)
		}
		eventViews = append(eventViews, entry)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"item":     workItemToView(item),
		"comments": commentViews,
		"links":    map[string]any{"outgoing": outgoing, "incoming": incoming},
		"events":   eventViews,
	})
}

// handleAddComment 追加一条评论。
func (s *Server) handleAddComment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}
	if !s.validBFFSignature(r, body) {
		http.Error(w, "invalid bff signature", http.StatusUnauthorized)
		return
	}
	var payload struct {
		BodyMD string `json:"body_md"`
		Actor  string `json:"actor"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	actor := strings.TrimSpace(payload.Actor)
	if actor == "" {
		http.Error(w, "actor is required", http.StatusBadRequest)
		return
	}
	item, actorRef, err := s.workItemByKey(r, actor)
	if err != nil {
		s.writeAppError(w, "resolve work item failed", err)
		return
	}
	comment, err := s.workItemsW.AddComment(r.Context(), actorRef, item.ID, payload.BodyMD)
	if err != nil {
		s.writeAppError(w, "add comment failed", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":         comment.ID.String(),
		"author_id":  comment.AuthorID,
		"body_md":    comment.BodyMD,
		"created_at": comment.CreatedAt.Format(time.RFC3339),
	})
}

// handleAddLink 建一条关联。目标用 key 指定，便于人直接填。
func (s *Server) handleAddLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<10))
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}
	if !s.validBFFSignature(r, body) {
		http.Error(w, "invalid bff signature", http.StatusUnauthorized)
		return
	}
	var payload struct {
		TargetKey string `json:"target_key"`
		Type      string `json:"type"`
		Actor     string `json:"actor"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	actor := strings.TrimSpace(payload.Actor)
	if actor == "" {
		http.Error(w, "actor is required", http.StatusBadRequest)
		return
	}
	linkType, err := workitemdomain.ParseLinkType(payload.Type)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	source, actorRef, err := s.workItemByKey(r, actor)
	if err != nil {
		s.writeAppError(w, "resolve work item failed", err)
		return
	}
	target, err := s.workItems.GetByKey(r.Context(), actorRef, strings.TrimSpace(payload.TargetKey))
	if err != nil {
		s.writeAppError(w, "resolve target work item failed", err)
		return
	}
	if _, err := s.workItemsW.AddLink(r.Context(), actorRef, source.ID, target.ID, linkType); err != nil {
		s.writeAppError(w, "add link failed", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleRemoveLink 删一条关联。用 query 参数传目标与类型（DELETE 带 body 不通用）。
func (s *Server) handleRemoveLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.validBFFSignature(r, nil) {
		http.Error(w, "invalid bff signature", http.StatusUnauthorized)
		return
	}
	actor := strings.TrimSpace(r.URL.Query().Get("actor"))
	if actor == "" {
		http.Error(w, "actor is required", http.StatusBadRequest)
		return
	}
	linkType, err := workitemdomain.ParseLinkType(r.URL.Query().Get("type"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	source, actorRef, err := s.workItemByKey(r, actor)
	if err != nil {
		s.writeAppError(w, "resolve work item failed", err)
		return
	}
	target, err := s.workItems.GetByKey(r.Context(), actorRef, strings.TrimSpace(r.URL.Query().Get("target_key")))
	if err != nil {
		s.writeAppError(w, "resolve target work item failed", err)
		return
	}
	if err := s.workItemsW.RemoveLink(r.Context(), actorRef, source.ID, target.ID, linkType); err != nil {
		s.writeAppError(w, "remove link failed", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// workItemByKey 把路径里的 key 解析成工作项与 actor。
// 单条路径统一用 key：URL 可读，内部再换成 UUID 去调领域服务。
func (s *Server) workItemByKey(r *http.Request, actor string) (workitemdomain.WorkItem, workitemapplication.Actor, error) {
	organizationID, err := s.organizationFrom(r)
	if err != nil {
		return workitemdomain.WorkItem{}, workitemapplication.Actor{}, err
	}
	actorRef := workitemapplication.Actor{Subject: actor, OrganizationID: organizationID}
	item, err := s.workItems.GetByKey(r.Context(), actorRef, strings.TrimSpace(r.PathValue("key")))
	if err != nil {
		return workitemdomain.WorkItem{}, workitemapplication.Actor{}, err
	}
	return item, actorRef, nil
}

// workItemToView 把领域对象转成控制台视图（列表与详情共用）。
func workItemToView(item workitemdomain.WorkItem) workItemView {
	return workItemView{
		ID:           item.ID.String(),
		Key:          item.Key,
		ProductID:    item.ProductID.String(),
		Type:         string(item.Type),
		Status:       string(item.Status),
		Resolution:   resolutionOrEmpty(item.Resolution),
		Title:        item.Title,
		Priority:     priorityLabel(item.Priority),
		AssigneeID:   stringOrEmpty(item.AssigneeID),
		ReporterID:   item.ReporterID,
		Version:      int32(item.Version),
		UpdatedAt:    item.UpdatedAt.Format(time.RFC3339),
		NextStatuses: statusNames(workitemdomain.AllowedTransitions(item.Type, item.Status)),
	}
}

// handleTransitionWorkItem 是控制台的第二个写端点：工作项状态流转。
//
// 与提交发布同一条 BFF 签名通道。actor 与角色放在 body 里而不是 header：
// 签名覆盖 body，改不动；header 不参与签名，放身份就等于留了一个可篡改的口子。
func (s *Server) handleTransitionWorkItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<10))
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}
	if !s.validBFFSignature(r, body) {
		http.Error(w, "invalid bff signature", http.StatusUnauthorized)
		return
	}
	var payload struct {
		To              string   `json:"to"`
		Resolution      string   `json:"resolution"`
		Comment         string   `json:"comment"`
		Actor           string   `json:"actor"`
		ActorRoles      []string `json:"actor_roles"`
		ExpectedVersion *int     `json:"expected_version"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	to, err := workitemdomain.ParseStatus(payload.To)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	actor := strings.TrimSpace(payload.Actor)
	if actor == "" {
		http.Error(w, "actor is required", http.StatusBadRequest)
		return
	}
	if payload.ExpectedVersion == nil {
		http.Error(w, "expected_version is required", http.StatusBadRequest)
		return
	}
	cmd := workitemapplication.TransitionCommand{
		To:              to,
		Comment:         strings.TrimSpace(payload.Comment),
		ExpectedVersion: *payload.ExpectedVersion,
	}
	if raw := strings.TrimSpace(payload.Resolution); raw != "" {
		resolution, parseErr := workitemdomain.ParseResolution(raw)
		if parseErr != nil {
			http.Error(w, parseErr.Error(), http.StatusBadRequest)
			return
		}
		cmd.Resolution = &resolution
	}
	existing, actorRef, err := s.workItemByKey(r, actor)
	if err != nil {
		s.writeAppError(w, "resolve work item failed", err)
		return
	}
	actorRef.Roles = payload.ActorRoles
	cmd.WorkItemID = existing.ID
	item, err := s.workItemsW.Transition(r.Context(), actorRef, cmd)
	if err != nil {
		s.writeAppError(w, "transition work item failed", err)
		return
	}
	s.logger.Info("work item transitioned via console",
		slog.String("work_item_id", item.ID.String()),
		slog.String("to", string(item.Status)),
		slog.String("actor", actor))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":      item.ID.String(),
		"status":  string(item.Status),
		"version": int32(item.Version),
	})
}

// statusNames 把领域状态列表转成 JSON 友好的字符串切片。
func statusNames(statuses []workitemdomain.Status) []string {
	out := make([]string, 0, len(statuses))
	for _, status := range statuses {
		out = append(out, string(status))
	}
	return out
}

func (s *Server) reportActor(r *http.Request) (reportapp.Actor, *uuid.UUID, error) {
	organizationID, err := s.organizationFrom(r)
	if err != nil {
		return reportapp.Actor{}, nil, err
	}
	var productID *uuid.UUID
	if raw := strings.TrimSpace(r.URL.Query().Get("product_id")); raw != "" {
		parsed, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			return reportapp.Actor{}, nil, errors.New("product_id must be a valid UUID")
		}
		productID = &parsed
	}
	return reportapp.Actor{Subject: "http-readonly", OrganizationID: organizationID}, productID, nil
}

func (s *Server) handleCycleTime(w http.ResponseWriter, r *http.Request) {
	actor, productID, err := s.reportActor(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 365 {
		days = 30
	}
	to := time.Now().UTC()
	from := to.AddDate(0, 0, -days)
	summaries, err := s.reports.CycleTime(r.Context(), actor, productID, from, to)
	if err != nil {
		s.logger.Error("cycle time report failed", slog.String("error", err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": summaries})
}

func (s *Server) handleThroughput(w http.ResponseWriter, r *http.Request) {
	actor, productID, err := s.reportActor(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	weeks, _ := strconv.Atoi(r.URL.Query().Get("weeks"))
	if weeks <= 0 || weeks > 52 {
		weeks = 12
	}
	to := time.Now().UTC()
	from := to.AddDate(0, 0, -weeks*7)
	buckets, err := s.reports.Throughput(r.Context(), actor, productID, from, to)
	if err != nil {
		s.logger.Error("throughput report failed", slog.String("error", err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": buckets})
}

func (s *Server) handleTeamLoad(w http.ResponseWriter, r *http.Request) {
	actor, productID, err := s.reportActor(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rows, err := s.reports.TeamLoad(r.Context(), actor, productID)
	if err != nil {
		s.logger.Error("team load report failed", slog.String("error", err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": rows})
}

func (s *Server) handleCFD(w http.ResponseWriter, r *http.Request) {
	actor, productID, err := s.reportActor(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 90 {
		days = 14
	}
	to := time.Now().UTC()
	from := to.AddDate(0, 0, -days)
	points, err := s.reports.CFD(r.Context(), actor, productID, from, to)
	if err != nil {
		s.logger.Error("cfd report failed", slog.String("error", err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": points})
}
