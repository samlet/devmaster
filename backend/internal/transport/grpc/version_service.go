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
	versionapp "github.com/xiaofeiwu/devmaster/backend/internal/version/application"
	versiondomain "github.com/xiaofeiwu/devmaster/backend/internal/version/domain"
)

type VersionService struct {
	devv1.UnimplementedVersionServiceServer
	service *versionapp.Service
	db      *database.DB
	logger  *slog.Logger
}

func NewVersionService(service *versionapp.Service, db *database.DB, logger *slog.Logger) *VersionService {
	return &VersionService{service: service, db: db, logger: logger}
}

func versionActorFrom(ctx context.Context) (versionapp.Actor, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return versionapp.Actor{}, status.Error(codes.Unauthenticated, "no authenticated principal in context")
	}
	return versionapp.Actor{
		Subject:        principal.Subject,
		OrganizationID: principal.OrganizationID,
		Roles:          principal.Roles,
	}, nil
}

var versionStatusProto = map[versiondomain.Status]devv1.VersionStatus{
	versiondomain.StatusPlanned:    devv1.VersionStatus_VERSION_STATUS_PLANNED,
	versiondomain.StatusInProgress: devv1.VersionStatus_VERSION_STATUS_IN_PROGRESS,
	versiondomain.StatusReleased:   devv1.VersionStatus_VERSION_STATUS_RELEASED,
	versiondomain.StatusArchived:   devv1.VersionStatus_VERSION_STATUS_ARCHIVED,
	versiondomain.StatusCancelled:  devv1.VersionStatus_VERSION_STATUS_CANCELLED,
}

var releaseStatusProto = map[versiondomain.ReleaseStatus]devv1.ReleaseStatus{
	versiondomain.ReleasePendingApproval: devv1.ReleaseStatus_RELEASE_STATUS_PENDING_APPROVAL,
	versiondomain.ReleaseApproved:        devv1.ReleaseStatus_RELEASE_STATUS_APPROVED,
	versiondomain.ReleaseTagged:          devv1.ReleaseStatus_RELEASE_STATUS_TAGGED,
	versiondomain.ReleasePublished:       devv1.ReleaseStatus_RELEASE_STATUS_PUBLISHED,
	versiondomain.ReleaseFailed:          devv1.ReleaseStatus_RELEASE_STATUS_FAILED,
}

func versionToProto(version versiondomain.Version) *devv1.Version {
	out := &devv1.Version{
		Id:             version.ID.String(),
		OrganizationId: version.OrganizationID.String(),
		ProductId:      version.ProductID.String(),
		Name:           version.Name,
		Status:         versionStatusProto[version.Status],
		Version:        int32(version.Version),
		CreatedAt:      timestamppb.New(version.CreatedAt),
		UpdatedAt:      timestamppb.New(version.UpdatedAt),
	}
	if version.MilestoneLabel != nil {
		out.MilestoneLabel = *version.MilestoneLabel
	}
	if version.PlannedDate != nil {
		out.PlannedDate = version.PlannedDate.Format("2006-01-02")
	}
	if version.ReleasedAt != nil {
		out.ReleasedAt = timestamppb.New(*version.ReleasedAt)
	}
	if version.NotesRef != nil {
		out.NotesRef = *version.NotesRef
	}
	if version.ScopeFrozenAt != nil {
		out.ScopeFrozenAt = timestamppb.New(*version.ScopeFrozenAt)
	}
	return out
}

func releaseToProto(release versiondomain.ReleaseRecord) *devv1.ReleaseRecord {
	out := &devv1.ReleaseRecord{
		Id:             release.ID.String(),
		OrganizationId: release.OrganizationID.String(),
		VersionId:      release.VersionID.String(),
		Status:         releaseStatusProto[release.Status],
		SubmittedBy:    release.SubmittedBy,
		Version:        int32(release.Version),
		CreatedAt:      timestamppb.New(release.CreatedAt),
		UpdatedAt:      timestamppb.New(release.UpdatedAt),
	}
	if release.Tag != nil {
		out.Tag = *release.Tag
	}
	if release.CommitFrom != nil {
		out.CommitFrom = *release.CommitFrom
	}
	if release.CommitTo != nil {
		out.CommitTo = *release.CommitTo
	}
	if release.ApprovalInstanceID != nil {
		out.ApprovalInstanceId = *release.ApprovalInstanceID
	}
	if release.ApproverID != nil {
		out.ApproverId = *release.ApproverID
	}
	if release.EvidenceRef != nil {
		out.EvidenceRef = *release.EvidenceRef
	}
	if release.FailureReason != nil {
		out.FailureReason = *release.FailureReason
	}
	if release.ReleasedBy != nil {
		out.ReleasedBy = *release.ReleasedBy
	}
	if release.ReleasedAt != nil {
		out.ReleasedAt = timestamppb.New(*release.ReleasedAt)
	}
	return out
}

func (s *VersionService) CreateVersion(ctx context.Context, req *devv1.CreateVersionRequest) (*devv1.CreateVersionResponse, error) {
	actor, err := versionActorFrom(ctx)
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
	plannedDate, err := parseDate(req.GetPlannedDate())
	if err != nil {
		return nil, err
	}
	return idempotency.Do(ctx, s.db, actor.OrganizationID, "version.create", key, req,
		func() *devv1.CreateVersionResponse { return &devv1.CreateVersionResponse{} },
		func(ctx context.Context) (*devv1.CreateVersionResponse, error) {
			version, err := s.service.Create(ctx, actor, versionapp.CreateCommand{
				ProductID:      productID,
				Name:           req.GetName(),
				MilestoneLabel: stringPtr(req.GetMilestoneLabel()),
				PlannedDate:    plannedDate,
				NotesRef:       stringPtr(req.GetNotesRef()),
			})
			if err != nil {
				return nil, appError(s.logger, err)
			}
			return &devv1.CreateVersionResponse{Version: versionToProto(version)}, nil
		})
}

func (s *VersionService) UpdateVersion(ctx context.Context, req *devv1.UpdateVersionRequest) (*devv1.UpdateVersionResponse, error) {
	actor, err := versionActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	key, expectedVersion, err := requireWriteOptions(req.GetOptions())
	if err != nil {
		return nil, err
	}
	versionID, err := uuid.Parse(strings.TrimSpace(req.GetId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "id must be a valid UUID")
	}
	fields := versiondomain.UpdateFields{
		Name:           req.Name,
		MilestoneLabel: req.MilestoneLabel,
		NotesRef:       req.NotesRef,
	}
	if req.PlannedDate != nil {
		plannedDate, parseErr := parseDate(*req.PlannedDate)
		if parseErr != nil {
			return nil, parseErr
		}
		fields.PlannedDate = plannedDate
	}
	return idempotency.Do(ctx, s.db, actor.OrganizationID, "version.update", key, req,
		func() *devv1.UpdateVersionResponse { return &devv1.UpdateVersionResponse{} },
		func(ctx context.Context) (*devv1.UpdateVersionResponse, error) {
			version, err := s.service.Update(ctx, actor, versionapp.UpdateCommand{
				VersionID:       versionID,
				ExpectedVersion: int(expectedVersion),
				Fields:          fields,
			})
			if err != nil {
				return nil, appError(s.logger, err)
			}
			return &devv1.UpdateVersionResponse{Version: versionToProto(version)}, nil
		})
}

func (s *VersionService) GetVersion(ctx context.Context, req *devv1.GetVersionRequest) (*devv1.GetVersionResponse, error) {
	actor, err := versionActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	versionID, err := uuid.Parse(strings.TrimSpace(req.GetId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "id must be a valid UUID")
	}
	version, err := s.service.Get(ctx, actor, versionID)
	if err != nil {
		return nil, appError(s.logger, err)
	}
	return &devv1.GetVersionResponse{Version: versionToProto(version)}, nil
}

func (s *VersionService) ListVersions(ctx context.Context, req *devv1.ListVersionsRequest) (*devv1.ListVersionsResponse, error) {
	actor, err := versionActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	productID, err := uuid.Parse(strings.TrimSpace(req.GetProductId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "product_id must be a valid UUID")
	}
	versions, err := s.service.List(ctx, actor, productID)
	if err != nil {
		return nil, appError(s.logger, err)
	}
	out := make([]*devv1.Version, 0, len(versions))
	for _, version := range versions {
		out = append(out, versionToProto(version))
	}
	return &devv1.ListVersionsResponse{Versions: out}, nil
}

func (s *VersionService) AssignVersionItems(ctx context.Context, req *devv1.AssignVersionItemsRequest) (*devv1.AssignVersionItemsResponse, error) {
	actor, err := versionActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	key, _, err := requireWriteOptions(req.GetOptions())
	if err != nil {
		return nil, err
	}
	versionID, err := uuid.Parse(strings.TrimSpace(req.GetVersionId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "version_id must be a valid UUID")
	}
	if len(req.GetWorkItemIds()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "work_item_ids must not be empty")
	}
	itemIDs := make([]uuid.UUID, 0, len(req.GetWorkItemIds()))
	for _, raw := range req.GetWorkItemIds() {
		parsed, parseErr := uuid.Parse(strings.TrimSpace(raw))
		if parseErr != nil {
			return nil, status.Errorf(codes.InvalidArgument, "work_item_id %q must be a valid UUID", raw)
		}
		itemIDs = append(itemIDs, parsed)
	}
	return idempotency.Do(ctx, s.db, actor.OrganizationID, "version.assign_items", key, req,
		func() *devv1.AssignVersionItemsResponse { return &devv1.AssignVersionItemsResponse{} },
		func(ctx context.Context) (*devv1.AssignVersionItemsResponse, error) {
			affected, err := s.service.AssignItems(ctx, actor, versionID, itemIDs)
			if err != nil {
				return nil, appError(s.logger, err)
			}
			return &devv1.AssignVersionItemsResponse{Affected: int32(affected)}, nil
		})
}

func (s *VersionService) SubmitRelease(ctx context.Context, req *devv1.SubmitReleaseRequest) (*devv1.SubmitReleaseResponse, error) {
	actor, err := versionActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	key, _, err := requireWriteOptions(req.GetOptions())
	if err != nil {
		return nil, err
	}
	versionID, err := uuid.Parse(strings.TrimSpace(req.GetVersionId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "version_id must be a valid UUID")
	}
	return idempotency.Do(ctx, s.db, actor.OrganizationID, "version.submit_release", key, req,
		func() *devv1.SubmitReleaseResponse { return &devv1.SubmitReleaseResponse{} },
		func(ctx context.Context) (*devv1.SubmitReleaseResponse, error) {
			release, err := s.service.SubmitRelease(ctx, actor, versionapp.SubmitReleaseCommand{
				VersionID:  versionID,
				CommitFrom: stringPtr(req.GetCommitFrom()),
				CommitTo:   stringPtr(req.GetCommitTo()),
			})
			if err != nil {
				return nil, appError(s.logger, err)
			}
			return &devv1.SubmitReleaseResponse{Release: releaseToProto(release)}, nil
		})
}

func (s *VersionService) GetRelease(ctx context.Context, req *devv1.GetReleaseRequest) (*devv1.GetReleaseResponse, error) {
	actor, err := versionActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	releaseID, err := uuid.Parse(strings.TrimSpace(req.GetId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "id must be a valid UUID")
	}
	release, err := s.service.GetRelease(ctx, actor, releaseID)
	if err != nil {
		return nil, appError(s.logger, err)
	}
	return &devv1.GetReleaseResponse{Release: releaseToProto(release)}, nil
}

func (s *VersionService) ListReleases(ctx context.Context, req *devv1.ListReleasesRequest) (*devv1.ListReleasesResponse, error) {
	actor, err := versionActorFrom(ctx)
	if err != nil {
		return nil, err
	}
	versionID, err := uuid.Parse(strings.TrimSpace(req.GetVersionId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "version_id must be a valid UUID")
	}
	releases, err := s.service.ListReleases(ctx, actor, versionID)
	if err != nil {
		return nil, appError(s.logger, err)
	}
	out := make([]*devv1.ReleaseRecord, 0, len(releases))
	for _, release := range releases {
		out = append(out, releaseToProto(release))
	}
	return &devv1.ListReleasesResponse{Releases: out}, nil
}

func parseDate(raw string) (*time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	parsed, err := time.Parse("2006-01-02", trimmed)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "planned_date must be YYYY-MM-DD")
	}
	return &parsed, nil
}
