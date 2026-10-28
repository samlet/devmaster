// Package application 是版本与发布的用例层。
package application

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/outbox"
	"github.com/xiaofeiwu/devmaster/backend/internal/version/domain"
)

type Actor struct {
	Subject        string
	OrganizationID uuid.UUID
	Roles          []string
}

type Repository interface {
	CreateVersion(ctx context.Context, version domain.Version) (domain.Version, error)
	GetVersion(ctx context.Context, organizationID, id uuid.UUID) (domain.Version, error)
	ListVersions(ctx context.Context, organizationID, productID uuid.UUID) ([]domain.Version, error)
	UpdateVersion(ctx context.Context, version domain.Version, expectedVersion int) (domain.Version, error)
	AssignItems(ctx context.Context, organizationID, versionID, productID uuid.UUID, itemIDs []uuid.UUID, actorID string) (int, error)
	CountItems(ctx context.Context, organizationID, versionID uuid.UUID) (int, error)
	ProductCode(ctx context.Context, organizationID, productID uuid.UUID) (string, error)
	SubmitRelease(ctx context.Context, version domain.Version, expectedVersion int, release domain.ReleaseRecord, outboxEvents []outbox.Event) (domain.Version, domain.ReleaseRecord, error)
	GetRelease(ctx context.Context, organizationID, id uuid.UUID) (domain.ReleaseRecord, error)
	ListReleases(ctx context.Context, organizationID, versionID uuid.UUID) ([]domain.ReleaseRecord, error)
	FindInflightRelease(ctx context.Context, organizationID, versionID uuid.UUID) (domain.ReleaseRecord, bool, error)
	UpdateRelease(ctx context.Context, release domain.ReleaseRecord, expectedVersion int, publishedVersion *domain.Version, outboxEvents []outbox.Event) (domain.ReleaseRecord, error)
}

type CreateCommand struct {
	ProductID      uuid.UUID
	Name           string
	MilestoneLabel *string
	PlannedDate    *time.Time
	NotesRef       *string
}

type UpdateCommand struct {
	VersionID       uuid.UUID
	ExpectedVersion int
	Fields          domain.UpdateFields
}

type SubmitReleaseCommand struct {
	VersionID  uuid.UUID
	CommitFrom *string
	CommitTo   *string
}

type Service struct {
	repo     Repository
	producer string
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo, producer: "devmaster-server"}
}

func (s *Service) Create(ctx context.Context, actor Actor, cmd CreateCommand) (domain.Version, error) {
	version, err := domain.NewVersion(domain.NewVersionInput{
		OrganizationID: actor.OrganizationID,
		ProductID:      cmd.ProductID,
		Name:           cmd.Name,
		MilestoneLabel: cmd.MilestoneLabel,
		PlannedDate:    cmd.PlannedDate,
		NotesRef:       cmd.NotesRef,
	})
	if err != nil {
		return domain.Version{}, err
	}
	created, err := s.repo.CreateVersion(ctx, version)
	if err != nil {
		return domain.Version{}, err
	}
	return created, nil
}

func (s *Service) Update(ctx context.Context, actor Actor, cmd UpdateCommand) (domain.Version, error) {
	version, err := s.repo.GetVersion(ctx, actor.OrganizationID, cmd.VersionID)
	if err != nil {
		return domain.Version{}, err
	}
	changed, err := version.ApplyUpdate(cmd.Fields)
	if err != nil {
		return domain.Version{}, err
	}
	if len(changed) == 0 {
		return version, nil
	}
	return s.repo.UpdateVersion(ctx, version, cmd.ExpectedVersion)
}

func (s *Service) Get(ctx context.Context, actor Actor, versionID uuid.UUID) (domain.Version, error) {
	return s.repo.GetVersion(ctx, actor.OrganizationID, versionID)
}

func (s *Service) List(ctx context.Context, actor Actor, productID uuid.UUID) ([]domain.Version, error) {
	return s.repo.ListVersions(ctx, actor.OrganizationID, productID)
}

// AssignItems 把工作项挂到版本上；版本范围一旦冻结就拒绝。
func (s *Service) AssignItems(ctx context.Context, actor Actor, versionID uuid.UUID, itemIDs []uuid.UUID) (int, error) {
	version, err := s.repo.GetVersion(ctx, actor.OrganizationID, versionID)
	if err != nil {
		return 0, err
	}
	if err := version.EnsureScopeMutable(); err != nil {
		return 0, err
	}
	if len(itemIDs) == 0 {
		return 0, apperr.Invalid("work_item_ids", "must not be empty")
	}
	return s.repo.AssignItems(ctx, actor.OrganizationID, versionID, version.ProductID, itemIDs, actor.Subject)
}

// SubmitRelease 提交发布：冻结范围 + 建发布记录 + 写 E9（自寻址事件）。
//
// 不在请求里同步起 Temporal：写库成功但起流程失败会让版本卡在
// PENDING_APPROVAL 且无法重提。由 consumer 消费 E9 起流程，失败可重放。
func (s *Service) SubmitRelease(ctx context.Context, actor Actor, cmd SubmitReleaseCommand) (domain.ReleaseRecord, error) {
	version, err := s.repo.GetVersion(ctx, actor.OrganizationID, cmd.VersionID)
	if err != nil {
		return domain.ReleaseRecord{}, err
	}
	if err := version.EnsureScopeMutable(); err != nil {
		return domain.ReleaseRecord{}, err
	}
	if _, found, err := s.repo.FindInflightRelease(ctx, actor.OrganizationID, cmd.VersionID); err != nil {
		return domain.ReleaseRecord{}, err
	} else if found {
		return domain.ReleaseRecord{}, apperr.FailedPrecondition("release", "a release attempt is already in flight")
	}
	itemCount, err := s.repo.CountItems(ctx, actor.OrganizationID, cmd.VersionID)
	if err != nil {
		return domain.ReleaseRecord{}, err
	}
	if itemCount == 0 {
		return domain.ReleaseRecord{}, apperr.FailedPrecondition("version_id", "version has no work items to release")
	}
	release, err := domain.NewReleaseRecord(domain.NewReleaseInput{
		OrganizationID: actor.OrganizationID,
		VersionID:      cmd.VersionID,
		CommitFrom:     cmd.CommitFrom,
		CommitTo:       cmd.CommitTo,
		SubmittedBy:    actor.Subject,
	})
	if err != nil {
		return domain.ReleaseRecord{}, err
	}
	if err := version.Freeze(); err != nil {
		return domain.ReleaseRecord{}, err
	}
	productCode, err := s.repo.ProductCode(ctx, actor.OrganizationID, version.ProductID)
	if err != nil {
		return domain.ReleaseRecord{}, err
	}
	event, err := outbox.NewEvent(outbox.NewEventParams{
		OrganizationID:   actor.OrganizationID,
		Producer:         s.producer,
		AggregateType:    "release",
		AggregateID:      release.ID,
		AggregateVersion: 1,
		EventType:        "dev.v1.release.submitted",
		Payload: map[string]any{
			"version_id":        version.ID.String(),
			"release_record_id": release.ID.String(),
			"product_code":      productCode,
			"version":           version.Name,
			"submitted_by":      actor.Subject,
		},
	})
	if err != nil {
		return domain.ReleaseRecord{}, err
	}
	_, submitted, err := s.repo.SubmitRelease(ctx, version, version.Version-1, release, []outbox.Event{event})
	if err != nil {
		return domain.ReleaseRecord{}, err
	}
	return submitted, nil
}

// EnsureMutable 供其它模块在改"版本成员"前校验范围是否冻结。
func (s *Service) EnsureMutable(ctx context.Context, actor Actor, versionID uuid.UUID) error {
	version, err := s.repo.GetVersion(ctx, actor.OrganizationID, versionID)
	if err != nil {
		return err
	}
	return version.EnsureScopeMutable()
}

func (s *Service) ProductCode(ctx context.Context, actor Actor, productID uuid.UUID) (string, error) {
	return s.repo.ProductCode(ctx, actor.OrganizationID, productID)
}

func (s *Service) ItemCount(ctx context.Context, actor Actor, versionID uuid.UUID) (int, error) {
	return s.repo.CountItems(ctx, actor.OrganizationID, versionID)
}

func (s *Service) GetRelease(ctx context.Context, actor Actor, releaseID uuid.UUID) (domain.ReleaseRecord, error) {
	return s.repo.GetRelease(ctx, actor.OrganizationID, releaseID)
}

func (s *Service) ListReleases(ctx context.Context, actor Actor, versionID uuid.UUID) ([]domain.ReleaseRecord, error) {
	return s.repo.ListReleases(ctx, actor.OrganizationID, versionID)
}

// StartApproval 记录 approver 实例 ID（工作流 Activity 调用）。
func (s *Service) StartApproval(ctx context.Context, actor Actor, releaseID uuid.UUID, instanceID string) (domain.ReleaseRecord, error) {
	release, err := s.repo.GetRelease(ctx, actor.OrganizationID, releaseID)
	if err != nil {
		return domain.ReleaseRecord{}, err
	}
	expected := release.Version
	if err := release.StartApproval(instanceID); err != nil {
		return domain.ReleaseRecord{}, err
	}
	return s.repo.UpdateRelease(ctx, release, expected, nil, nil)
}

func (s *Service) Approve(ctx context.Context, actor Actor, releaseID uuid.UUID, approverID string) (domain.ReleaseRecord, error) {
	release, err := s.repo.GetRelease(ctx, actor.OrganizationID, releaseID)
	if err != nil {
		return domain.ReleaseRecord{}, err
	}
	expected := release.Version
	if err := release.Approve(approverID); err != nil {
		return domain.ReleaseRecord{}, err
	}
	return s.repo.UpdateRelease(ctx, release, expected, nil, nil)
}

func (s *Service) MarkTagged(ctx context.Context, actor Actor, releaseID uuid.UUID, tag string) (domain.ReleaseRecord, error) {
	release, err := s.repo.GetRelease(ctx, actor.OrganizationID, releaseID)
	if err != nil {
		return domain.ReleaseRecord{}, err
	}
	expected := release.Version
	if err := release.MarkTagged(tag); err != nil {
		return domain.ReleaseRecord{}, err
	}
	return s.repo.UpdateRelease(ctx, release, expected, nil, nil)
}

// Publish 把发布记录置为 PUBLISHED、版本置为 RELEASED，并写 E6。
func (s *Service) Publish(ctx context.Context, actor Actor, releaseID uuid.UUID, evidenceRef string) (domain.ReleaseRecord, error) {
	release, err := s.repo.GetRelease(ctx, actor.OrganizationID, releaseID)
	if err != nil {
		return domain.ReleaseRecord{}, err
	}
	version, err := s.repo.GetVersion(ctx, actor.OrganizationID, release.VersionID)
	if err != nil {
		return domain.ReleaseRecord{}, err
	}
	expected := release.Version
	now := time.Now().UTC()
	if err := release.Publish(actor.Subject, now); err != nil {
		return domain.ReleaseRecord{}, err
	}
	if evidenceRef != "" {
		release.EvidenceRef = &evidenceRef
	}
	if err := version.MarkReleased(now); err != nil {
		return domain.ReleaseRecord{}, err
	}
	productCode, err := s.repo.ProductCode(ctx, actor.OrganizationID, version.ProductID)
	if err != nil {
		return domain.ReleaseRecord{}, err
	}
	event, err := outbox.NewEvent(outbox.NewEventParams{
		OrganizationID:   actor.OrganizationID,
		Producer:         s.producer,
		AggregateType:    "version",
		AggregateID:      version.ID,
		AggregateVersion: int64(version.Version),
		EventType:        "dev.v1.version.released",
		Payload: map[string]any{
			"version_id":   version.ID.String(),
			"product_code": productCode,
			"version":      version.Name,
			"tag":          deref(release.Tag),
			"commit_from":  deref(release.CommitFrom),
			"commit_to":    deref(release.CommitTo),
			"evidence_ref": deref(release.EvidenceRef),
			"released_at":  now.Format(time.RFC3339),
		},
	})
	if err != nil {
		return domain.ReleaseRecord{}, err
	}
	return s.repo.UpdateRelease(ctx, release, expected, &version, []outbox.Event{event})
}

func (s *Service) Fail(ctx context.Context, actor Actor, releaseID uuid.UUID, reason string) (domain.ReleaseRecord, error) {
	release, err := s.repo.GetRelease(ctx, actor.OrganizationID, releaseID)
	if err != nil {
		return domain.ReleaseRecord{}, err
	}
	expected := release.Version
	if err := release.Fail(reason); err != nil {
		return domain.ReleaseRecord{}, err
	}
	return s.repo.UpdateRelease(ctx, release, expected, nil, nil)
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
