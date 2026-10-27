package grpc

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	devv1 "github.com/xiaofeiwu/devmaster/backend/gen/go/dev/v1"
	boardapp "github.com/xiaofeiwu/devmaster/backend/internal/board/application"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/auth"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/idempotency"
	sprintapp "github.com/xiaofeiwu/devmaster/backend/internal/sprint/application"
	sprintdomain "github.com/xiaofeiwu/devmaster/backend/internal/sprint/domain"
)

type SprintService struct {
	devv1.UnimplementedSprintServiceServer
	service *sprintapp.Service
	db      *database.DB
	logger  *slog.Logger
}

func NewSprintService(service *sprintapp.Service, db *database.DB, logger *slog.Logger) *SprintService {
	return &SprintService{service: service, db: db, logger: logger}
}

func sprintActorFrom(ctx context.Context) (sprintapp.Actor, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return sprintapp.Actor{}, status.Error(codes.Unauthenticated, "no authenticated principal in context")
	}
	return sprintapp.Actor{
		Subject:        principal.Subject,
		OrganizationID: principal.OrganizationID,
		Roles:          principal.Roles,
	}, nil
}

var sprintStateProto = map[sprintdomain.State]devv1.SprintState{
	sprintdomain.StatePlanned: devv1.SprintState_SPRINT_STATE_PLANNED,
	sprintdomain.StateActive:  devv1.SprintState_SPRINT_STATE_ACTIVE,
	sprintdomain.StateClosed:  devv1.SprintState_SPRINT_STATE_CLOSED,
}

func sprintToProto(sprint sprintdomain.Sprint) *devv1.Sprint {
	out := &devv1.Sprint{
		Id:              sprint.ID.String(),
		OrganizationId:  sprint.OrganizationID.String(),
		BoardId:         sprint.BoardID.String(),
		Name:            sprint.Name,
		Goal:            sprint.Goal,
		State:           sprintStateProto[sprint.State],
		CommittedPoints: int32(sprint.CommittedPoints),
		Version:         int32(sprint.Version),
		CreatedAt:       timestamppb.New(sprint.CreatedAt),
		UpdatedAt:       timestamppb.New(sprint.UpdatedAt),
	}
	if sprint.StartAt != nil {
		out.StartAt = timestamppb.New(*sprint.StartAt)
	}
	if sprint.EndAt != nil {
		out.EndAt = timestamppb.New(*sprint.EndAt)
	}
	if sprint.CapacityPoints != nil {
		out.CapacityPoints = int32(*sprint.CapacityPoints)
	}
	if sprint.ClosedAt != nil {
		out.ClosedAt = timestamppb.New(*sprint.ClosedAt)
	}
	return out
}

func (s *SprintService) CreateSprint(ctx context.Context, req *devv1.CreateSprintRequest) (*devv1.CreateSprintResponse, error) {
	actor, err := sprintActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	key, _, err := requireWriteOptions(req.GetOptions())
	if err != nil {
		return nil, err
	}
	productID, err := uuid.Parse(strings.TrimSpace(req.GetProductId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "product_id must be a valid UUID")
	}
	command := sprintapp.CreateCommand{
		ProductID: productID,
		Name:      req.GetName(),
		Goal:      req.GetGoal(),
		StartAt:   timePtr(req.GetStartAt()),
		EndAt:     timePtr(req.GetEndAt()),
	}
	if req.GetCapacityPoints() > 0 {
		capacity := int16(req.GetCapacityPoints())
		command.CapacityPoints = &capacity
	}
	return idempotency.Do(ctx, s.db, actor.OrganizationID, "sprint.create", key, req,
		func() *devv1.CreateSprintResponse { return &devv1.CreateSprintResponse{} },
		func(ctx context.Context) (*devv1.CreateSprintResponse, error) {
			sprint, err := s.service.Create(ctx, actor, command)
			if err != nil {
				return nil, appError(s.logger, err)
			}
			return &devv1.CreateSprintResponse{Sprint: sprintToProto(sprint)}, nil
		})
}

func (s *SprintService) UpdateSprint(ctx context.Context, req *devv1.UpdateSprintRequest) (*devv1.UpdateSprintResponse, error) {
	actor, err := sprintActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	key, expectedVersion, err := requireWriteOptions(req.GetOptions())
	if err != nil {
		return nil, err
	}
	sprintID, err := uuid.Parse(strings.TrimSpace(req.GetId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "id must be a valid UUID")
	}
	fields := sprintdomain.UpdateFields{
		Name: req.Name,
		Goal: req.Goal,
	}
	if req.StartAt != nil {
		fields.StartAt = timePtr(req.StartAt)
	}
	if req.EndAt != nil {
		fields.EndAt = timePtr(req.EndAt)
	}
	if req.CapacityPoints != nil {
		capacity := int16(*req.CapacityPoints)
		fields.CapacityPoints = &capacity
	}
	return idempotency.Do(ctx, s.db, actor.OrganizationID, "sprint.update", key, req,
		func() *devv1.UpdateSprintResponse { return &devv1.UpdateSprintResponse{} },
		func(ctx context.Context) (*devv1.UpdateSprintResponse, error) {
			sprint, err := s.service.Update(ctx, actor, sprintapp.UpdateCommand{
				SprintID:        sprintID,
				ExpectedVersion: int(expectedVersion),
				Fields:          fields,
			})
			if err != nil {
				return nil, appError(s.logger, err)
			}
			return &devv1.UpdateSprintResponse{Sprint: sprintToProto(sprint)}, nil
		})
}

func (s *SprintService) StartSprint(ctx context.Context, req *devv1.StartSprintRequest) (*devv1.StartSprintResponse, error) {
	actor, err := sprintActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	key, expectedVersion, err := requireWriteOptions(req.GetOptions())
	if err != nil {
		return nil, err
	}
	sprintID, err := uuid.Parse(strings.TrimSpace(req.GetId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "id must be a valid UUID")
	}
	return idempotency.Do(ctx, s.db, actor.OrganizationID, "sprint.start", key, req,
		func() *devv1.StartSprintResponse { return &devv1.StartSprintResponse{} },
		func(ctx context.Context) (*devv1.StartSprintResponse, error) {
			sprint, committed, err := s.service.Start(ctx, actor, sprintID, int(expectedVersion))
			if err != nil {
				return nil, appError(s.logger, err)
			}
			return &devv1.StartSprintResponse{Sprint: sprintToProto(sprint), CommittedPoints: int32(committed)}, nil
		})
}

func (s *SprintService) CloseSprint(ctx context.Context, req *devv1.CloseSprintRequest) (*devv1.CloseSprintResponse, error) {
	actor, err := sprintActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	key, expectedVersion, err := requireWriteOptions(req.GetOptions())
	if err != nil {
		return nil, err
	}
	sprintID, err := uuid.Parse(strings.TrimSpace(req.GetId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "id must be a valid UUID")
	}
	var moveTo *uuid.UUID
	if raw := strings.TrimSpace(req.GetMoveToSprintId()); raw != "" {
		parsed, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			return nil, status.Error(codes.InvalidArgument, "move_to_sprint_id must be a valid UUID")
		}
		moveTo = &parsed
	}
	return idempotency.Do(ctx, s.db, actor.OrganizationID, "sprint.close", key, req,
		func() *devv1.CloseSprintResponse { return &devv1.CloseSprintResponse{} },
		func(ctx context.Context) (*devv1.CloseSprintResponse, error) {
			sprint, err := s.service.Close(ctx, actor, sprintapp.CloseCommand{
				SprintID:        sprintID,
				ExpectedVersion: int(expectedVersion),
				MoveToSprintID:  moveTo,
			})
			if err != nil {
				return nil, appError(s.logger, err)
			}
			return &devv1.CloseSprintResponse{Sprint: sprintToProto(sprint)}, nil
		})
}

func (s *SprintService) GetSprint(ctx context.Context, req *devv1.GetSprintRequest) (*devv1.GetSprintResponse, error) {
	actor, err := sprintActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	sprintID, err := uuid.Parse(strings.TrimSpace(req.GetId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "id must be a valid UUID")
	}
	sprint, err := s.service.Get(ctx, actor, sprintID)
	if err != nil {
		return nil, appError(s.logger, err)
	}
	return &devv1.GetSprintResponse{Sprint: sprintToProto(sprint)}, nil
}

func (s *SprintService) ListSprints(ctx context.Context, req *devv1.ListSprintsRequest) (*devv1.ListSprintsResponse, error) {
	actor, err := sprintActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	productID, err := uuid.Parse(strings.TrimSpace(req.GetProductId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "product_id must be a valid UUID")
	}
	sprints, err := s.service.List(ctx, actor, productID)
	if err != nil {
		return nil, appError(s.logger, err)
	}
	out := make([]*devv1.Sprint, 0, len(sprints))
	for _, sprint := range sprints {
		out = append(out, sprintToProto(sprint))
	}
	return &devv1.ListSprintsResponse{Sprints: out}, nil
}

func (s *SprintService) AddSprintItems(ctx context.Context, req *devv1.AddSprintItemsRequest) (*devv1.AddSprintItemsResponse, error) {
	actor, err := sprintActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	key, _, err := requireWriteOptions(req.GetOptions())
	if err != nil {
		return nil, err
	}
	sprintID, itemIDs, err := parseSprintItems(req.GetSprintId(), req.GetWorkItemIds())
	if err != nil {
		return nil, err
	}
	return idempotency.Do(ctx, s.db, actor.OrganizationID, "sprint.add_items", key, req,
		func() *devv1.AddSprintItemsResponse { return &devv1.AddSprintItemsResponse{} },
		func(ctx context.Context) (*devv1.AddSprintItemsResponse, error) {
			affected, err := s.service.AddItems(ctx, actor, sprintID, itemIDs)
			if err != nil {
				return nil, appError(s.logger, err)
			}
			return &devv1.AddSprintItemsResponse{Affected: int32(affected)}, nil
		})
}

func (s *SprintService) RemoveSprintItems(ctx context.Context, req *devv1.RemoveSprintItemsRequest) (*devv1.RemoveSprintItemsResponse, error) {
	actor, err := sprintActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	key, _, err := requireWriteOptions(req.GetOptions())
	if err != nil {
		return nil, err
	}
	sprintID, itemIDs, err := parseSprintItems(req.GetSprintId(), req.GetWorkItemIds())
	if err != nil {
		return nil, err
	}
	return idempotency.Do(ctx, s.db, actor.OrganizationID, "sprint.remove_items", key, req,
		func() *devv1.RemoveSprintItemsResponse { return &devv1.RemoveSprintItemsResponse{} },
		func(ctx context.Context) (*devv1.RemoveSprintItemsResponse, error) {
			affected, err := s.service.RemoveItems(ctx, actor, sprintID, itemIDs)
			if err != nil {
				return nil, appError(s.logger, err)
			}
			return &devv1.RemoveSprintItemsResponse{Affected: int32(affected)}, nil
		})
}

func (s *SprintService) GetSprintStats(ctx context.Context, req *devv1.GetSprintStatsRequest) (*devv1.GetSprintStatsResponse, error) {
	actor, err := sprintActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	sprintID, err := uuid.Parse(strings.TrimSpace(req.GetSprintId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "sprint_id must be a valid UUID")
	}
	stats, err := s.service.Stats(ctx, actor, sprintID)
	if err != nil {
		return nil, appError(s.logger, err)
	}
	return &devv1.GetSprintStatsResponse{Stats: &devv1.SprintStats{
		SprintId:        stats.SprintID.String(),
		CommittedPoints: int32(stats.CommittedPoints),
		AddedPoints:     int32(stats.AddedPoints),
		RemovedPoints:   int32(stats.RemovedPoints),
		CompletedPoints: int32(stats.CompletedPoints),
		ScopePoints:     int32(stats.ScopePoints),
		RemainingPoints: int32(stats.RemainingPoints),
		CreepRate:       stats.CreepRate,
	}}, nil
}

func (s *SprintService) GetBurndown(ctx context.Context, req *devv1.GetBurndownRequest) (*devv1.GetBurndownResponse, error) {
	actor, err := sprintActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	sprintID, err := uuid.Parse(strings.TrimSpace(req.GetSprintId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "sprint_id must be a valid UUID")
	}
	points, err := s.service.Burndown(ctx, actor, sprintID)
	if err != nil {
		return nil, appError(s.logger, err)
	}
	out := make([]*devv1.BurndownPoint, 0, len(points))
	for _, point := range points {
		out = append(out, burndownToProto(point))
	}
	return &devv1.GetBurndownResponse{Points: out}, nil
}

func (s *SprintService) SnapshotSprint(ctx context.Context, req *devv1.SnapshotSprintRequest) (*devv1.SnapshotSprintResponse, error) {
	actor, err := sprintActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	key, _, err := requireWriteOptions(req.GetOptions())
	if err != nil {
		return nil, err
	}
	sprintID, err := uuid.Parse(strings.TrimSpace(req.GetSprintId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "sprint_id must be a valid UUID")
	}
	return idempotency.Do(ctx, s.db, actor.OrganizationID, "sprint.snapshot", key, req,
		func() *devv1.SnapshotSprintResponse { return &devv1.SnapshotSprintResponse{} },
		func(ctx context.Context) (*devv1.SnapshotSprintResponse, error) {
			point, err := s.service.Snapshot(ctx, actor, sprintID)
			if err != nil {
				return nil, appError(s.logger, err)
			}
			return &devv1.SnapshotSprintResponse{Point: burndownToProto(point)}, nil
		})
}

func burndownToProto(point sprintdomain.Snapshot) *devv1.BurndownPoint {
	return &devv1.BurndownPoint{
		Date:            point.Date.Format("2006-01-02"),
		ScopePoints:     int32(point.ScopePoints),
		CompletedPoints: int32(point.CompletedPoints),
		RemainingPoints: int32(point.RemainingPoints),
	}
}

func parseSprintItems(rawSprintID string, rawItemIDs []string) (uuid.UUID, []uuid.UUID, error) {
	sprintID, err := uuid.Parse(strings.TrimSpace(rawSprintID))
	if err != nil {
		return uuid.Nil, nil, status.Error(codes.InvalidArgument, "sprint_id must be a valid UUID")
	}
	if len(rawItemIDs) == 0 {
		return uuid.Nil, nil, status.Error(codes.InvalidArgument, "work_item_ids must not be empty")
	}
	itemIDs := make([]uuid.UUID, 0, len(rawItemIDs))
	for _, raw := range rawItemIDs {
		parsed, parseErr := uuid.Parse(strings.TrimSpace(raw))
		if parseErr != nil {
			return uuid.Nil, nil, status.Errorf(codes.InvalidArgument, "work_item_id %q must be a valid UUID", raw)
		}
		itemIDs = append(itemIDs, parsed)
	}
	return sprintID, itemIDs, nil
}

func timePtr(value *timestamppb.Timestamp) *time.Time {
	if value == nil {
		return nil
	}
	converted := value.AsTime()
	return &converted
}

// ---------------------------------------------------------------------------
// BoardService
// ---------------------------------------------------------------------------

type BoardService struct {
	devv1.UnimplementedBoardServiceServer
	service *boardapp.Service
	db      *database.DB
	logger  *slog.Logger
}

func NewBoardService(service *boardapp.Service, db *database.DB, logger *slog.Logger) *BoardService {
	return &BoardService{service: service, db: db, logger: logger}
}

func boardActorFrom(ctx context.Context) (boardapp.Actor, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return boardapp.Actor{}, status.Error(codes.Unauthenticated, "no authenticated principal in context")
	}
	return boardapp.Actor{
		Subject:        principal.Subject,
		OrganizationID: principal.OrganizationID,
		Roles:          principal.Roles,
	}, nil
}

func (s *BoardService) GetBoard(ctx context.Context, req *devv1.GetBoardRequest) (*devv1.GetBoardResponse, error) {
	actor, err := boardActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	productID, err := uuid.Parse(strings.TrimSpace(req.GetProductId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "product_id must be a valid UUID")
	}
	var sprintID *uuid.UUID
	if raw := strings.TrimSpace(req.GetSprintId()); raw != "" {
		parsed, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			return nil, status.Error(codes.InvalidArgument, "sprint_id must be a valid UUID")
		}
		sprintID = &parsed
	}
	view, err := s.service.Get(ctx, actor, productID, sprintID)
	if err != nil {
		return nil, appError(s.logger, err)
	}
	response := &devv1.GetBoardResponse{}
	if view.Sprint != nil {
		response.Sprint = sprintToProto(*view.Sprint)
	}
	for _, column := range view.Columns {
		response.Columns = append(response.Columns, &devv1.BoardColumn{
			Column:    string(column.Column),
			Statuses:  column.Statuses,
			WorkItems: workItemsToProto(column.Items),
		})
	}
	return response, nil
}

func (s *BoardService) MoveWorkItem(ctx context.Context, req *devv1.MoveWorkItemRequest) (*devv1.MoveWorkItemResponse, error) {
	actor, err := boardActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	key, expectedVersion, err := requireWriteOptions(req.GetOptions())
	if err != nil {
		return nil, err
	}
	itemID, err := uuid.Parse(strings.TrimSpace(req.GetWorkItemId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "work_item_id must be a valid UUID")
	}
	var beforeID *uuid.UUID
	if raw := strings.TrimSpace(req.GetBeforeId()); raw != "" {
		parsed, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			return nil, status.Error(codes.InvalidArgument, "before_id must be a valid UUID")
		}
		beforeID = &parsed
	}
	targetStatus, ok := statusDomain[req.GetToStatus()]
	if !ok && req.GetToStatus() != devv1.WorkItemStatus_WORK_ITEM_STATUS_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "unknown target status")
	}
	return idempotency.Do(ctx, s.db, actor.OrganizationID, "board.move", key, req,
		func() *devv1.MoveWorkItemResponse { return &devv1.MoveWorkItemResponse{} },
		func(ctx context.Context) (*devv1.MoveWorkItemResponse, error) {
			item, err := s.service.Move(ctx, actor, boardapp.MoveCommand{
				WorkItemID:      itemID,
				ToStatus:        targetStatus,
				BeforeID:        beforeID,
				ExpectedVersion: int(expectedVersion),
				Comment:         req.GetComment(),
			})
			if err != nil {
				return nil, appError(s.logger, err)
			}
			return &devv1.MoveWorkItemResponse{WorkItem: workItemToProto(item)}, nil
		})
}
