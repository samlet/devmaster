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
)

type ProductReader interface {
	List(ctx context.Context, actor application.Actor, filter application.ListFilter) ([]domain.Product, int, error)
}

type WorkItemReader interface {
	List(ctx context.Context, actor workitemapplication.Actor, filter workitemapplication.ListFilter) ([]workitemdomain.WorkItem, int, error)
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

// SubjectResolver 是主体目录的只读端口（解析 keycloak/<sub> 到锚点）。
type SubjectResolver interface {
	ResolveSubject(ctx context.Context, sourceSystem, externalID string) (anchorID string, found bool, err error)
}

// ConsoleDeps 是控制台 BFF 通道的依赖：写端点与身份解析。
// 打包传参，以后加控制台能力不必再改构造器签名。
type ConsoleDeps struct {
	Releases  ReleaseSubmitter
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
	mux.HandleFunc("/api/sprints", s.handleSprints)
	mux.HandleFunc("/api/board", s.handleBoard)
	mux.HandleFunc("/api/versions", s.handleVersions)
	mux.HandleFunc("/api/releases", s.handleSubmitRelease)
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
