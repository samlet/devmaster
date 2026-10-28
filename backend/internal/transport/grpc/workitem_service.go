package grpc

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	devv1 "github.com/xiaofeiwu/devmaster/backend/gen/go/dev/v1"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/auth"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/idempotency"
	workitemapplication "github.com/xiaofeiwu/devmaster/backend/internal/workitem/application"
	workitemdomain "github.com/xiaofeiwu/devmaster/backend/internal/workitem/domain"
)

type WorkItemService struct {
	devv1.UnimplementedWorkItemServiceServer
	service *workitemapplication.Service
	db      *database.DB
	logger  *slog.Logger
}

func NewWorkItemService(service *workitemapplication.Service, db *database.DB, logger *slog.Logger) *WorkItemService {
	return &WorkItemService{service: service, db: db, logger: logger}
}

func workActorFrom(ctx context.Context) (workitemapplication.Actor, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return workitemapplication.Actor{}, status.Error(codes.Unauthenticated, "no authenticated principal in context")
	}
	return workitemapplication.Actor{
		Subject:        principal.Subject,
		OrganizationID: principal.OrganizationID,
		Roles:          principal.Roles,
	}, nil
}

var (
	workItemTypeProto = map[workitemdomain.Type]devv1.WorkItemType{
		workitemdomain.TypeEpic:    devv1.WorkItemType_WORK_ITEM_TYPE_EPIC,
		workitemdomain.TypeStory:   devv1.WorkItemType_WORK_ITEM_TYPE_STORY,
		workitemdomain.TypeTask:    devv1.WorkItemType_WORK_ITEM_TYPE_TASK,
		workitemdomain.TypeBug:     devv1.WorkItemType_WORK_ITEM_TYPE_BUG,
		workitemdomain.TypeSubtask: devv1.WorkItemType_WORK_ITEM_TYPE_SUBTASK,
	}
	workItemTypeDomain = map[devv1.WorkItemType]workitemdomain.Type{
		devv1.WorkItemType_WORK_ITEM_TYPE_EPIC:    workitemdomain.TypeEpic,
		devv1.WorkItemType_WORK_ITEM_TYPE_STORY:   workitemdomain.TypeStory,
		devv1.WorkItemType_WORK_ITEM_TYPE_TASK:    workitemdomain.TypeTask,
		devv1.WorkItemType_WORK_ITEM_TYPE_BUG:     workitemdomain.TypeBug,
		devv1.WorkItemType_WORK_ITEM_TYPE_SUBTASK: workitemdomain.TypeSubtask,
	}
	statusProto = map[workitemdomain.Status]devv1.WorkItemStatus{
		workitemdomain.StatusBacklog:    devv1.WorkItemStatus_WORK_ITEM_STATUS_BACKLOG,
		workitemdomain.StatusTodo:       devv1.WorkItemStatus_WORK_ITEM_STATUS_TODO,
		workitemdomain.StatusOpen:       devv1.WorkItemStatus_WORK_ITEM_STATUS_OPEN,
		workitemdomain.StatusInProgress: devv1.WorkItemStatus_WORK_ITEM_STATUS_IN_PROGRESS,
		workitemdomain.StatusInReview:   devv1.WorkItemStatus_WORK_ITEM_STATUS_IN_REVIEW,
		workitemdomain.StatusResolved:   devv1.WorkItemStatus_WORK_ITEM_STATUS_RESOLVED,
		workitemdomain.StatusVerified:   devv1.WorkItemStatus_WORK_ITEM_STATUS_VERIFIED,
		workitemdomain.StatusDone:       devv1.WorkItemStatus_WORK_ITEM_STATUS_DONE,
		workitemdomain.StatusClosed:     devv1.WorkItemStatus_WORK_ITEM_STATUS_CLOSED,
		workitemdomain.StatusReopened:   devv1.WorkItemStatus_WORK_ITEM_STATUS_REOPENED,
		workitemdomain.StatusWontfix:    devv1.WorkItemStatus_WORK_ITEM_STATUS_WONTFIX,
		workitemdomain.StatusCancelled:  devv1.WorkItemStatus_WORK_ITEM_STATUS_CANCELLED,
	}
	statusDomain = map[devv1.WorkItemStatus]workitemdomain.Status{
		devv1.WorkItemStatus_WORK_ITEM_STATUS_BACKLOG:     workitemdomain.StatusBacklog,
		devv1.WorkItemStatus_WORK_ITEM_STATUS_TODO:        workitemdomain.StatusTodo,
		devv1.WorkItemStatus_WORK_ITEM_STATUS_OPEN:        workitemdomain.StatusOpen,
		devv1.WorkItemStatus_WORK_ITEM_STATUS_IN_PROGRESS: workitemdomain.StatusInProgress,
		devv1.WorkItemStatus_WORK_ITEM_STATUS_IN_REVIEW:   workitemdomain.StatusInReview,
		devv1.WorkItemStatus_WORK_ITEM_STATUS_RESOLVED:    workitemdomain.StatusResolved,
		devv1.WorkItemStatus_WORK_ITEM_STATUS_VERIFIED:    workitemdomain.StatusVerified,
		devv1.WorkItemStatus_WORK_ITEM_STATUS_DONE:        workitemdomain.StatusDone,
		devv1.WorkItemStatus_WORK_ITEM_STATUS_CLOSED:      workitemdomain.StatusClosed,
		devv1.WorkItemStatus_WORK_ITEM_STATUS_REOPENED:    workitemdomain.StatusReopened,
		devv1.WorkItemStatus_WORK_ITEM_STATUS_WONTFIX:     workitemdomain.StatusWontfix,
		devv1.WorkItemStatus_WORK_ITEM_STATUS_CANCELLED:   workitemdomain.StatusCancelled,
	}
	resolutionProto = map[workitemdomain.Resolution]devv1.Resolution{
		workitemdomain.ResolutionFixed:           devv1.Resolution_RESOLUTION_FIXED,
		workitemdomain.ResolutionWontfix:         devv1.Resolution_RESOLUTION_WONTFIX,
		workitemdomain.ResolutionDuplicate:       devv1.Resolution_RESOLUTION_DUPLICATE,
		workitemdomain.ResolutionCannotReproduce: devv1.Resolution_RESOLUTION_CANNOT_REPRODUCE,
		workitemdomain.ResolutionByDesign:        devv1.Resolution_RESOLUTION_BY_DESIGN,
		workitemdomain.ResolutionObsolete:        devv1.Resolution_RESOLUTION_OBSOLETE,
	}
	resolutionDomain = map[devv1.Resolution]workitemdomain.Resolution{
		devv1.Resolution_RESOLUTION_FIXED:            workitemdomain.ResolutionFixed,
		devv1.Resolution_RESOLUTION_WONTFIX:          workitemdomain.ResolutionWontfix,
		devv1.Resolution_RESOLUTION_DUPLICATE:        workitemdomain.ResolutionDuplicate,
		devv1.Resolution_RESOLUTION_CANNOT_REPRODUCE: workitemdomain.ResolutionCannotReproduce,
		devv1.Resolution_RESOLUTION_BY_DESIGN:        workitemdomain.ResolutionByDesign,
		devv1.Resolution_RESOLUTION_OBSOLETE:         workitemdomain.ResolutionObsolete,
	}
	severityProto = map[workitemdomain.Severity]devv1.Severity{
		workitemdomain.SeverityBlocker:  devv1.Severity_SEVERITY_BLOCKER,
		workitemdomain.SeverityCritical: devv1.Severity_SEVERITY_CRITICAL,
		workitemdomain.SeverityMajor:    devv1.Severity_SEVERITY_MAJOR,
		workitemdomain.SeverityMinor:    devv1.Severity_SEVERITY_MINOR,
		workitemdomain.SeverityTrivial:  devv1.Severity_SEVERITY_TRIVIAL,
	}
	severityDomain = map[devv1.Severity]workitemdomain.Severity{
		devv1.Severity_SEVERITY_BLOCKER:  workitemdomain.SeverityBlocker,
		devv1.Severity_SEVERITY_CRITICAL: workitemdomain.SeverityCritical,
		devv1.Severity_SEVERITY_MAJOR:    workitemdomain.SeverityMajor,
		devv1.Severity_SEVERITY_MINOR:    workitemdomain.SeverityMinor,
		devv1.Severity_SEVERITY_TRIVIAL:  workitemdomain.SeverityTrivial,
	}
	linkTypeProto = map[workitemdomain.LinkType]devv1.LinkType{
		workitemdomain.LinkBlocks:     devv1.LinkType_LINK_TYPE_BLOCKS,
		workitemdomain.LinkBlockedBy:  devv1.LinkType_LINK_TYPE_BLOCKED_BY,
		workitemdomain.LinkRelates:    devv1.LinkType_LINK_TYPE_RELATES,
		workitemdomain.LinkDuplicates: devv1.LinkType_LINK_TYPE_DUPLICATES,
		workitemdomain.LinkCauses:     devv1.LinkType_LINK_TYPE_CAUSES,
	}
	linkTypeDomain = map[devv1.LinkType]workitemdomain.LinkType{
		devv1.LinkType_LINK_TYPE_BLOCKS:     workitemdomain.LinkBlocks,
		devv1.LinkType_LINK_TYPE_BLOCKED_BY: workitemdomain.LinkBlockedBy,
		devv1.LinkType_LINK_TYPE_RELATES:    workitemdomain.LinkRelates,
		devv1.LinkType_LINK_TYPE_DUPLICATES: workitemdomain.LinkDuplicates,
		devv1.LinkType_LINK_TYPE_CAUSES:     workitemdomain.LinkCauses,
	}
)

func workItemToProto(item workitemdomain.WorkItem) *devv1.WorkItem {
	out := &devv1.WorkItem{
		Id:             item.ID.String(),
		OrganizationId: item.OrganizationID.String(),
		Key:            item.Key,
		ProductId:      item.ProductID.String(),
		Type:           workItemTypeProto[item.Type],
		Status:         statusProto[item.Status],
		Title:          item.Title,
		DescriptionMd:  item.DescriptionMD,
		Priority:       priorityProto(item.Priority),
		ReporterId:     item.ReporterID,
		Rank:           item.Rank,
		ReopenedCount:  int32(item.ReopenedCount),
		Version:        int32(item.Version),
		CreatedAt:      timestamppb.New(item.CreatedAt),
		UpdatedAt:      timestamppb.New(item.UpdatedAt),
	}
	if item.Resolution != nil {
		out.Resolution = resolutionProto[*item.Resolution]
	}
	if item.AcceptanceCriteria != nil {
		out.AcceptanceCriteria = *item.AcceptanceCriteria
	}
	if item.Severity != nil {
		out.Severity = severityProto[*item.Severity]
	}
	if item.Environment != nil {
		out.Environment = *item.Environment
	}
	if item.AssigneeID != nil {
		out.AssigneeId = *item.AssigneeID
	}
	if item.ParentID != nil {
		out.ParentId = item.ParentID.String()
	}
	if item.ProjectCode != nil {
		out.ProjectCode = *item.ProjectCode
	}
	if item.WBSCode != nil {
		out.WbsCode = *item.WBSCode
	}
	if item.ExternalRef != nil {
		out.ExternalRef = *item.ExternalRef
	}
	if item.ResolvedAt != nil {
		out.ResolvedAt = timestamppb.New(*item.ResolvedAt)
	}
	if item.ClosedAt != nil {
		out.ClosedAt = timestamppb.New(*item.ClosedAt)
	}
	if item.SprintID != nil {
		out.SprintId = item.SprintID.String()
	}
	if item.StoryPoints != nil {
		out.StoryPoints = int32(*item.StoryPoints)
	}
	if item.EstimateMinutes != nil {
		out.EstimateMinutes = *item.EstimateMinutes
	}
	if item.Platform != nil {
		out.Platform = *item.Platform
	}
	if item.BuildNumber != nil {
		out.BuildNumber = *item.BuildNumber
	}
	if item.ReproSteps != nil {
		out.ReproSteps = *item.ReproSteps
	}
	if item.ExpectedBehavior != nil {
		out.ExpectedBehavior = *item.ExpectedBehavior
	}
	if item.ActualBehavior != nil {
		out.ActualBehavior = *item.ActualBehavior
	}
	if item.VersionID != nil {
		out.VersionId = item.VersionID.String()
	}
	if item.AffectsVersionID != nil {
		out.AffectsVersionId = item.AffectsVersionID.String()
	}
	return out
}

func priorityProto(value workitemdomain.Priority) devv1.Priority {
	switch value {
	case workitemdomain.PriorityP0:
		return devv1.Priority_PRIORITY_P0
	case workitemdomain.PriorityP1:
		return devv1.Priority_PRIORITY_P1
	case workitemdomain.PriorityP2:
		return devv1.Priority_PRIORITY_P2
	case workitemdomain.PriorityP3:
		return devv1.Priority_PRIORITY_P3
	default:
		return devv1.Priority_PRIORITY_UNSPECIFIED
	}
}

func priorityFromProto(value devv1.Priority) (workitemdomain.Priority, error) {
	switch value {
	case devv1.Priority_PRIORITY_P0:
		return workitemdomain.PriorityP0, nil
	case devv1.Priority_PRIORITY_P1:
		return workitemdomain.PriorityP1, nil
	case devv1.Priority_PRIORITY_P2:
		return workitemdomain.PriorityP2, nil
	case devv1.Priority_PRIORITY_P3:
		return workitemdomain.PriorityP3, nil
	default:
		return 0, status.Error(codes.InvalidArgument, "priority is required")
	}
}

func (s *WorkItemService) CreateWorkItem(ctx context.Context, req *devv1.CreateWorkItemRequest) (*devv1.CreateWorkItemResponse, error) {
	actor, err := workActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	key, _, err := requireWriteOptions(req.GetOptions())
	if err != nil {
		return nil, err
	}
	itemType, ok := workItemTypeDomain[req.GetType()]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "type is required")
	}
	priority := workitemdomain.PriorityP2
	if req.GetPriority() != devv1.Priority_PRIORITY_UNSPECIFIED {
		priority, err = priorityFromProto(req.GetPriority())
		if err != nil {
			return nil, err
		}
	}
	var severity *workitemdomain.Severity
	if req.GetSeverity() != devv1.Severity_SEVERITY_UNSPECIFIED {
		value, ok := severityDomain[req.GetSeverity()]
		if !ok {
			return nil, status.Error(codes.InvalidArgument, "unknown severity")
		}
		severity = &value
	}
	productID, err := uuid.Parse(strings.TrimSpace(req.GetProductId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "product_id must be a valid UUID")
	}
	var parentID *uuid.UUID
	if raw := strings.TrimSpace(req.GetParentId()); raw != "" {
		parsed, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			return nil, status.Error(codes.InvalidArgument, "parent_id must be a valid UUID")
		}
		parentID = &parsed
	}

	return idempotency.Do(ctx, s.db, actor.OrganizationID, "workitem.create", key, req,
		func() *devv1.CreateWorkItemResponse { return &devv1.CreateWorkItemResponse{} },
		func(ctx context.Context) (*devv1.CreateWorkItemResponse, error) {
			item, err := s.service.Create(ctx, actor, workitemapplication.CreateCommand{
				ProductID:          productID,
				Type:               itemType,
				Title:              req.GetTitle(),
				DescriptionMD:      req.GetDescriptionMd(),
				AcceptanceCriteria: stringPtr(req.GetAcceptanceCriteria()),
				Priority:           priority,
				Severity:           severity,
				Environment:        stringPtr(req.GetEnvironment()),
				AssigneeID:         stringPtr(req.GetAssigneeId()),
				ParentID:           parentID,
				ProjectCode:        stringPtr(req.GetProjectCode()),
				WBSCode:            stringPtr(req.GetWbsCode()),
				ExternalRef:        stringPtr(req.GetExternalRef()),
				StoryPoints:        int16Ptr(req.StoryPoints),
				EstimateMinutes:    req.EstimateMinutes,
				Platform:           stringPtr(req.GetPlatform()),
				BuildNumber:        stringPtr(req.GetBuildNumber()),
				ReproSteps:         stringPtr(req.GetReproSteps()),
				ExpectedBehavior:   stringPtr(req.GetExpectedBehavior()),
				ActualBehavior:     stringPtr(req.GetActualBehavior()),
				VersionID:          uuidPtr(req.GetVersionId()),
				AffectsVersionID:   uuidPtr(req.GetAffectsVersionId()),
			})
			if err != nil {
				return nil, appError(s.logger, err)
			}
			return &devv1.CreateWorkItemResponse{WorkItem: workItemToProto(item)}, nil
		})
}

func (s *WorkItemService) GetWorkItem(ctx context.Context, req *devv1.GetWorkItemRequest) (*devv1.GetWorkItemResponse, error) {
	actor, err := workActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	var item workitemdomain.WorkItem
	switch {
	case strings.TrimSpace(req.GetId()) != "":
		id, parseErr := uuid.Parse(strings.TrimSpace(req.GetId()))
		if parseErr != nil {
			return nil, status.Error(codes.InvalidArgument, "id must be a valid UUID")
		}
		item, err = s.service.Get(ctx, actor, id)
	case strings.TrimSpace(req.GetKey()) != "":
		item, err = s.service.GetByKey(ctx, actor, req.GetKey())
	default:
		return nil, status.Error(codes.InvalidArgument, "either id or key is required")
	}
	if err != nil {
		return nil, appError(s.logger, err)
	}
	return &devv1.GetWorkItemResponse{WorkItem: workItemToProto(item)}, nil
}

func (s *WorkItemService) UpdateWorkItem(ctx context.Context, req *devv1.UpdateWorkItemRequest) (*devv1.UpdateWorkItemResponse, error) {
	actor, err := workActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	key, expectedVersion, err := requireWriteOptions(req.GetOptions())
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(strings.TrimSpace(req.GetId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "id must be a valid UUID")
	}
	fields := workitemdomain.UpdateFields{
		Title:              req.Title,
		DescriptionMD:      req.DescriptionMd,
		AcceptanceCriteria: req.AcceptanceCriteria,
		Environment:        req.Environment,
		ProjectCode:        req.ProjectCode,
		WBSCode:            req.WbsCode,
		ExternalRef:        req.ExternalRef,
		BlockedReason:      req.BlockedReason,
		StoryPoints:        int16Ptr(req.StoryPoints),
		EstimateMinutes:    req.EstimateMinutes,
		Platform:           req.Platform,
		BuildNumber:        req.BuildNumber,
		ReproSteps:         req.ReproSteps,
		ExpectedBehavior:   req.ExpectedBehavior,
		ActualBehavior:     req.ActualBehavior,
		VersionID:          uuidPtrValue(req.VersionId),
		AffectsVersionID:   uuidPtrValue(req.AffectsVersionId),
	}
	if req.Priority != nil {
		priority, parseErr := priorityFromProto(*req.Priority)
		if parseErr != nil {
			return nil, parseErr
		}
		fields.Priority = &priority
	}
	if req.Severity != nil {
		severity, ok := severityDomain[*req.Severity]
		if !ok {
			return nil, status.Error(codes.InvalidArgument, "unknown severity")
		}
		fields.Severity = &severity
	}

	return idempotency.Do(ctx, s.db, actor.OrganizationID, "workitem.update", key, req,
		func() *devv1.UpdateWorkItemResponse { return &devv1.UpdateWorkItemResponse{} },
		func(ctx context.Context) (*devv1.UpdateWorkItemResponse, error) {
			item, err := s.service.Update(ctx, actor, workitemapplication.UpdateCommand{
				WorkItemID:      id,
				ExpectedVersion: int(expectedVersion),
				Fields:          fields,
			})
			if err != nil {
				return nil, appError(s.logger, err)
			}
			return &devv1.UpdateWorkItemResponse{WorkItem: workItemToProto(item)}, nil
		})
}

func (s *WorkItemService) TransitionWorkItem(ctx context.Context, req *devv1.TransitionWorkItemRequest) (*devv1.TransitionWorkItemResponse, error) {
	actor, err := workActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	key, expectedVersion, err := requireWriteOptions(req.GetOptions())
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(strings.TrimSpace(req.GetId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "id must be a valid UUID")
	}
	to, ok := statusDomain[req.GetTo()]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "target status is required")
	}
	var resolution *workitemdomain.Resolution
	if req.GetResolution() != devv1.Resolution_RESOLUTION_UNSPECIFIED {
		value, ok := resolutionDomain[req.GetResolution()]
		if !ok {
			return nil, status.Error(codes.InvalidArgument, "unknown resolution")
		}
		resolution = &value
	}

	return idempotency.Do(ctx, s.db, actor.OrganizationID, "workitem.transition", key, req,
		func() *devv1.TransitionWorkItemResponse { return &devv1.TransitionWorkItemResponse{} },
		func(ctx context.Context) (*devv1.TransitionWorkItemResponse, error) {
			item, err := s.service.Transition(ctx, actor, workitemapplication.TransitionCommand{
				WorkItemID:      id,
				To:              to,
				Resolution:      resolution,
				Comment:         req.GetComment(),
				ExpectedVersion: int(expectedVersion),
			})
			if err != nil {
				return nil, appError(s.logger, err)
			}
			return &devv1.TransitionWorkItemResponse{WorkItem: workItemToProto(item)}, nil
		})
}

func (s *WorkItemService) AssignWorkItem(ctx context.Context, req *devv1.AssignWorkItemRequest) (*devv1.AssignWorkItemResponse, error) {
	actor, err := workActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	key, expectedVersion, err := requireWriteOptions(req.GetOptions())
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(strings.TrimSpace(req.GetId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "id must be a valid UUID")
	}
	var assignee *string
	if req.AssigneeId != nil {
		assignee = stringPtr(*req.AssigneeId)
	}

	return idempotency.Do(ctx, s.db, actor.OrganizationID, "workitem.assign", key, req,
		func() *devv1.AssignWorkItemResponse { return &devv1.AssignWorkItemResponse{} },
		func(ctx context.Context) (*devv1.AssignWorkItemResponse, error) {
			item, err := s.service.Assign(ctx, actor, workitemapplication.AssignCommand{
				WorkItemID:      id,
				AssigneeID:      assignee,
				ExpectedVersion: int(expectedVersion),
			})
			if err != nil {
				return nil, appError(s.logger, err)
			}
			return &devv1.AssignWorkItemResponse{WorkItem: workItemToProto(item)}, nil
		})
}

func (s *WorkItemService) ListWorkItems(ctx context.Context, req *devv1.ListWorkItemsRequest) (*devv1.ListWorkItemsResponse, error) {
	actor, err := workActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	filter, err := listFilterFromProto(req)
	if err != nil {
		return nil, err
	}
	items, total, err := s.service.List(ctx, actor, filter)
	if err != nil {
		return nil, appError(s.logger, err)
	}
	return &devv1.ListWorkItemsResponse{
		WorkItems: workItemsToProto(items),
		Page:      pageResponse(total, filter.Limit, filter.Offset, len(items)),
	}, nil
}

func (s *WorkItemService) SearchWorkItems(ctx context.Context, req *devv1.SearchWorkItemsRequest) (*devv1.SearchWorkItemsResponse, error) {
	actor, err := workActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	query := strings.TrimSpace(req.GetQuery())
	if query == "" {
		return nil, status.Error(codes.InvalidArgument, "query is required")
	}
	filter := workitemapplication.ListFilter{Query: query}
	if raw := strings.TrimSpace(req.GetProductId()); raw != "" {
		productID, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			return nil, status.Error(codes.InvalidArgument, "product_id must be a valid UUID")
		}
		filter.ProductID = &productID
	}
	if filter.Limit, filter.Offset, err = pageWindow(req.GetPage()); err != nil {
		return nil, err
	}
	items, total, err := s.service.List(ctx, actor, filter)
	if err != nil {
		return nil, appError(s.logger, err)
	}
	return &devv1.SearchWorkItemsResponse{
		WorkItems: workItemsToProto(items),
		Page:      pageResponse(total, filter.Limit, filter.Offset, len(items)),
	}, nil
}

func (s *WorkItemService) AddComment(ctx context.Context, req *devv1.AddCommentRequest) (*devv1.AddCommentResponse, error) {
	actor, err := workActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	key, _, err := requireWriteOptions(req.GetOptions())
	if err != nil {
		return nil, err
	}
	workItemID, err := uuid.Parse(strings.TrimSpace(req.GetWorkItemId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "work_item_id must be a valid UUID")
	}

	return idempotency.Do(ctx, s.db, actor.OrganizationID, "workitem.comment", key, req,
		func() *devv1.AddCommentResponse { return &devv1.AddCommentResponse{} },
		func(ctx context.Context) (*devv1.AddCommentResponse, error) {
			comment, err := s.service.AddComment(ctx, actor, workItemID, req.GetBodyMd())
			if err != nil {
				return nil, appError(s.logger, err)
			}
			return &devv1.AddCommentResponse{Comment: commentToProto(comment)}, nil
		})
}

func (s *WorkItemService) ListComments(ctx context.Context, req *devv1.ListCommentsRequest) (*devv1.ListCommentsResponse, error) {
	actor, err := workActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	workItemID, err := uuid.Parse(strings.TrimSpace(req.GetWorkItemId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "work_item_id must be a valid UUID")
	}
	comments, err := s.service.ListComments(ctx, actor, workItemID)
	if err != nil {
		return nil, appError(s.logger, err)
	}
	out := make([]*devv1.Comment, 0, len(comments))
	for _, comment := range comments {
		out = append(out, commentToProto(comment))
	}
	return &devv1.ListCommentsResponse{Comments: out}, nil
}

func (s *WorkItemService) AddLink(ctx context.Context, req *devv1.AddLinkRequest) (*devv1.AddLinkResponse, error) {
	actor, err := workActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	key, _, err := requireWriteOptions(req.GetOptions())
	if err != nil {
		return nil, err
	}
	fromID, err := uuid.Parse(strings.TrimSpace(req.GetFromId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "from_id must be a valid UUID")
	}
	toID, err := uuid.Parse(strings.TrimSpace(req.GetToId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "to_id must be a valid UUID")
	}
	linkType, ok := linkTypeDomain[req.GetType()]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "link type is required")
	}

	return idempotency.Do(ctx, s.db, actor.OrganizationID, "workitem.link", key, req,
		func() *devv1.AddLinkResponse { return &devv1.AddLinkResponse{} },
		func(ctx context.Context) (*devv1.AddLinkResponse, error) {
			link, err := s.service.AddLink(ctx, actor, fromID, toID, linkType)
			if err != nil {
				return nil, appError(s.logger, err)
			}
			return &devv1.AddLinkResponse{Link: linkToProto(link)}, nil
		})
}

func (s *WorkItemService) RemoveLink(ctx context.Context, req *devv1.RemoveLinkRequest) (*devv1.RemoveLinkResponse, error) {
	actor, err := workActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	key, _, err := requireWriteOptions(req.GetOptions())
	if err != nil {
		return nil, err
	}
	fromID, err := uuid.Parse(strings.TrimSpace(req.GetFromId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "from_id must be a valid UUID")
	}
	toID, err := uuid.Parse(strings.TrimSpace(req.GetToId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "to_id must be a valid UUID")
	}
	linkType, ok := linkTypeDomain[req.GetType()]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "link type is required")
	}

	return idempotency.Do(ctx, s.db, actor.OrganizationID, "workitem.unlink", key, req,
		func() *devv1.RemoveLinkResponse { return &devv1.RemoveLinkResponse{} },
		func(ctx context.Context) (*devv1.RemoveLinkResponse, error) {
			if err := s.service.RemoveLink(ctx, actor, fromID, toID, linkType); err != nil {
				return nil, appError(s.logger, err)
			}
			return &devv1.RemoveLinkResponse{}, nil
		})
}

func (s *WorkItemService) ListLinks(ctx context.Context, req *devv1.ListLinksRequest) (*devv1.ListLinksResponse, error) {
	actor, err := workActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	workItemID, err := uuid.Parse(strings.TrimSpace(req.GetWorkItemId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "work_item_id must be a valid UUID")
	}
	links, err := s.service.ListLinks(ctx, actor, workItemID)
	if err != nil {
		return nil, appError(s.logger, err)
	}
	out := make([]*devv1.Link, 0, len(links))
	for _, link := range links {
		out = append(out, linkToProto(link))
	}
	return &devv1.ListLinksResponse{Links: out}, nil
}

func (s *WorkItemService) ListWorkItemEvents(ctx context.Context, req *devv1.ListWorkItemEventsRequest) (*devv1.ListWorkItemEventsResponse, error) {
	actor, err := workActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	workItemID, err := uuid.Parse(strings.TrimSpace(req.GetWorkItemId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "work_item_id must be a valid UUID")
	}
	events, err := s.service.ListEvents(ctx, actor, workItemID)
	if err != nil {
		return nil, appError(s.logger, err)
	}
	out := make([]*devv1.WorkItemEvent, 0, len(events))
	for _, event := range events {
		payload, _ := json.Marshal(event.Payload)
		item := &devv1.WorkItemEvent{
			EventType:   event.EventType,
			ActorId:     event.ActorID,
			PayloadJson: string(payload),
			OccurredAt:  timestamppb.New(event.OccurredAt),
		}
		if event.FromStatus != nil {
			item.FromStatus = statusProto[*event.FromStatus]
		}
		if event.ToStatus != nil {
			item.ToStatus = statusProto[*event.ToStatus]
		}
		out = append(out, item)
	}
	return &devv1.ListWorkItemEventsResponse{Events: out}, nil
}

func listFilterFromProto(req *devv1.ListWorkItemsRequest) (workitemapplication.ListFilter, error) {
	filter := workitemapplication.ListFilter{}
	if raw := strings.TrimSpace(req.GetProductId()); raw != "" {
		productID, err := uuid.Parse(raw)
		if err != nil {
			return filter, status.Error(codes.InvalidArgument, "product_id must be a valid UUID")
		}
		filter.ProductID = &productID
	}
	if req.GetType() != devv1.WorkItemType_WORK_ITEM_TYPE_UNSPECIFIED {
		itemType, ok := workItemTypeDomain[req.GetType()]
		if !ok {
			return filter, status.Error(codes.InvalidArgument, "unknown work item type")
		}
		filter.Type = itemType
	}
	for _, statusValue := range req.GetStatuses() {
		parsedStatus, ok := statusDomain[statusValue]
		if !ok {
			return filter, status.Error(codes.InvalidArgument, "unknown status filter")
		}
		filter.Statuses = append(filter.Statuses, parsedStatus)
	}
	if raw := strings.TrimSpace(req.GetAssigneeId()); raw != "" {
		filter.AssigneeID = &raw
	}
	filter.Query = strings.TrimSpace(req.GetQuery())
	limit, offset, err := pageWindow(req.GetPage())
	if err != nil {
		return filter, err
	}
	filter.Limit, filter.Offset = limit, offset
	return filter, nil
}

func workItemsToProto(items []workitemdomain.WorkItem) []*devv1.WorkItem {
	out := make([]*devv1.WorkItem, 0, len(items))
	for _, item := range items {
		out = append(out, workItemToProto(item))
	}
	return out
}

func commentToProto(comment workitemdomain.Comment) *devv1.Comment {
	out := &devv1.Comment{
		Id:         comment.ID.String(),
		WorkItemId: comment.WorkItemID.String(),
		AuthorId:   comment.AuthorID,
		BodyMd:     comment.BodyMD,
		CreatedAt:  timestamppb.New(comment.CreatedAt),
	}
	if comment.EditedAt != nil {
		out.EditedAt = timestamppb.New(*comment.EditedAt)
	}
	return out
}

func linkToProto(link workitemdomain.Link) *devv1.Link {
	return &devv1.Link{
		Id:        link.ID.String(),
		FromId:    link.FromID.String(),
		ToId:      link.ToID.String(),
		Type:      linkTypeProto[link.Type],
		CreatedBy: link.CreatedBy,
		CreatedAt: timestamppb.New(link.CreatedAt),
	}
}

func uuidPtr(raw string) *uuid.UUID {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	parsed, err := uuid.Parse(trimmed)
	if err != nil {
		return nil
	}
	return &parsed
}

func uuidPtrValue(raw *string) *uuid.UUID {
	if raw == nil {
		return nil
	}
	return uuidPtr(*raw)
}

func int16Ptr(value *int32) *int16 {
	if value == nil {
		return nil
	}
	converted := int16(*value)
	return &converted
}

func stringPtr(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
