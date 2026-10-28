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
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/auth"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/idempotency"
	worklogapp "github.com/xiaofeiwu/devmaster/backend/internal/worklog/application"
	worklogdomain "github.com/xiaofeiwu/devmaster/backend/internal/worklog/domain"
)

type WorkLogService struct {
	devv1.UnimplementedWorkLogServiceServer
	service *worklogapp.Service
	db      *database.DB
	logger  *slog.Logger
}

func NewWorkLogService(service *worklogapp.Service, db *database.DB, logger *slog.Logger) *WorkLogService {
	return &WorkLogService{service: service, db: db, logger: logger}
}

func workLogActorFrom(ctx context.Context) (worklogapp.Actor, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return worklogapp.Actor{}, status.Error(codes.Unauthenticated, "no authenticated principal in context")
	}
	return worklogapp.Actor{Subject: principal.Subject, OrganizationID: principal.OrganizationID}, nil
}

var workLogSourceProto = map[worklogdomain.Source]devv1.WorkLogSource{
	worklogdomain.SourceManual: devv1.WorkLogSource_WORK_LOG_SOURCE_MANUAL,
	worklogdomain.SourceImport: devv1.WorkLogSource_WORK_LOG_SOURCE_IMPORT,
}

func workLogToProto(log worklogdomain.WorkLog) *devv1.WorkLog {
	out := &devv1.WorkLog{
		Id:             log.ID.String(),
		OrganizationId: log.OrganizationID.String(),
		WorkItemId:     log.WorkItemID.String(),
		WorkItemKey:    log.WorkItemKey,
		AuthorId:       log.AuthorID,
		Minutes:        int32(log.Minutes),
		WorkDate:       log.WorkDate.Format("2006-01-02"),
		Source:         workLogSourceProto[log.Source],
		CreatedAt:      timestamppb.New(log.CreatedAt),
	}
	if log.Note != nil {
		out.Note = *log.Note
	}
	if log.ProjectCode != nil {
		out.ProjectCode = *log.ProjectCode
	}
	if log.WBSCode != nil {
		out.WbsCode = *log.WBSCode
	}
	if log.GradeCode != nil {
		out.GradeCode = *log.GradeCode
	}
	if log.ReversalOf != nil {
		out.ReversalOf = log.ReversalOf.String()
	}
	return out
}

func (s *WorkLogService) LogWork(ctx context.Context, req *devv1.LogWorkRequest) (*devv1.LogWorkResponse, error) {
	actor, err := workLogActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	key, _, err := requireWriteOptions(req.GetOptions())
	if err != nil {
		return nil, err
	}
	workDate, err := time.Parse("2006-01-02", strings.TrimSpace(req.GetWorkDate()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "work_date must be YYYY-MM-DD")
	}
	var workItemID *uuid.UUID
	if raw := strings.TrimSpace(req.GetWorkItemId()); raw != "" {
		parsed, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			return nil, status.Error(codes.InvalidArgument, "work_item_id must be a valid UUID")
		}
		workItemID = &parsed
	}
	source := worklogdomain.SourceManual
	if req.GetSource() == devv1.WorkLogSource_WORK_LOG_SOURCE_IMPORT {
		source = worklogdomain.SourceImport
	}
	return idempotency.Do(ctx, s.db, actor.OrganizationID, "worklog.log", key, req,
		func() *devv1.LogWorkResponse { return &devv1.LogWorkResponse{} },
		func(ctx context.Context) (*devv1.LogWorkResponse, error) {
			log, err := s.service.Log(ctx, actor, worklogapp.LogCommand{
				WorkItemID:  workItemID,
				WorkItemKey: req.GetWorkItemKey(),
				Minutes:     int(req.GetMinutes()),
				WorkDate:    workDate,
				Note:        stringPtr(req.GetNote()),
				ProjectCode: stringPtr(req.GetProjectCode()),
				WBSCode:     stringPtr(req.GetWbsCode()),
				GradeCode:   stringPtr(req.GetGradeCode()),
				Source:      source,
			})
			if err != nil {
				return nil, appError(s.logger, err)
			}
			return &devv1.LogWorkResponse{WorkLog: workLogToProto(log)}, nil
		})
}

func (s *WorkLogService) GetWorkLog(ctx context.Context, req *devv1.GetWorkLogRequest) (*devv1.GetWorkLogResponse, error) {
	actor, err := workLogActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(strings.TrimSpace(req.GetId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "id must be a valid UUID")
	}
	log, err := s.service.Get(ctx, actor, id)
	if err != nil {
		return nil, appError(s.logger, err)
	}
	return &devv1.GetWorkLogResponse{WorkLog: workLogToProto(log)}, nil
}

func (s *WorkLogService) ListWorkLogs(ctx context.Context, req *devv1.ListWorkLogsRequest) (*devv1.ListWorkLogsResponse, error) {
	actor, err := workLogActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	filter, err := workLogFilter(req.GetWorkItemId(), req.GetAuthorId(), req.GetFromDate(), req.GetToDate())
	if err != nil {
		return nil, err
	}
	filter.Limit, filter.Offset, err = pageWindow(req.GetPage())
	if err != nil {
		return nil, err
	}
	logs, total, err := s.service.List(ctx, actor, filter)
	if err != nil {
		return nil, appError(s.logger, err)
	}
	out := make([]*devv1.WorkLog, 0, len(logs))
	for _, log := range logs {
		out = append(out, workLogToProto(log))
	}
	return &devv1.ListWorkLogsResponse{
		WorkLogs: out,
		Page:     pageResponse(total, filter.Limit, filter.Offset, len(logs)),
	}, nil
}

func (s *WorkLogService) SumWorkLogs(ctx context.Context, req *devv1.SumWorkLogsRequest) (*devv1.SumWorkLogsResponse, error) {
	actor, err := workLogActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	filter, err := workLogFilter(req.GetWorkItemId(), "", req.GetFromDate(), req.GetToDate())
	if err != nil {
		return nil, err
	}
	total, err := s.service.Sum(ctx, actor, filter)
	if err != nil {
		return nil, appError(s.logger, err)
	}
	return &devv1.SumWorkLogsResponse{Minutes: int32(total)}, nil
}

func (s *WorkLogService) ReverseWorkLog(ctx context.Context, req *devv1.ReverseWorkLogRequest) (*devv1.ReverseWorkLogResponse, error) {
	actor, err := workLogActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	key, _, err := requireWriteOptions(req.GetOptions())
	if err != nil {
		return nil, err
	}
	workLogID, err := uuid.Parse(strings.TrimSpace(req.GetWorkLogId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "work_log_id must be a valid UUID")
	}
	var minutes *int
	if req.Minutes != nil {
		value := int(*req.Minutes)
		minutes = &value
	}
	return idempotency.Do(ctx, s.db, actor.OrganizationID, "worklog.reverse", key, req,
		func() *devv1.ReverseWorkLogResponse { return &devv1.ReverseWorkLogResponse{} },
		func(ctx context.Context) (*devv1.ReverseWorkLogResponse, error) {
			log, err := s.service.Reverse(ctx, actor, workLogID, minutes, stringPtr(req.GetNote()))
			if err != nil {
				return nil, appError(s.logger, err)
			}
			return &devv1.ReverseWorkLogResponse{WorkLog: workLogToProto(log)}, nil
		})
}

func workLogFilter(workItemID, authorID, fromDate, toDate string) (worklogapp.ListFilter, error) {
	filter := worklogapp.ListFilter{}
	if raw := strings.TrimSpace(workItemID); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			return filter, status.Error(codes.InvalidArgument, "work_item_id must be a valid UUID")
		}
		filter.WorkItemID = &parsed
	}
	if raw := strings.TrimSpace(authorID); raw != "" {
		filter.AuthorID = &raw
	}
	if raw := strings.TrimSpace(fromDate); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			return filter, status.Error(codes.InvalidArgument, "from_date must be YYYY-MM-DD")
		}
		filter.From = &parsed
	}
	if raw := strings.TrimSpace(toDate); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			return filter, status.Error(codes.InvalidArgument, "to_date must be YYYY-MM-DD")
		}
		filter.To = &parsed
	}
	return filter, nil
}
