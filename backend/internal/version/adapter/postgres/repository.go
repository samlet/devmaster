// Package postgres 是版本与发布记录的持久化实现。
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/outbox"
	"github.com/xiaofeiwu/devmaster/backend/internal/version/application"
	"github.com/xiaofeiwu/devmaster/backend/internal/version/domain"
)

const (
	uniqueViolation = "23505"
	foreignKey      = "23503"
)

const versionColumns = `id, organization_id, product_id, name, status, milestone_label,
	planned_date, released_at, notes_ref, scope_frozen_at, version, created_at, updated_at`

const releaseColumns = `id, organization_id, version_id, status, tag, commit_from, commit_to,
	submitted_by, approval_instance_id, approver_id, evidence_ref, failure_reason,
	released_by, released_at, version, created_at, updated_at`

type Repository struct {
	db *database.DB
}

var _ application.Repository = (*Repository)(nil)

func NewRepository(db *database.DB) *Repository { return &Repository{db: db} }

func (r *Repository) CreateVersion(ctx context.Context, version domain.Version) (domain.Version, error) {
	err := r.db.InOrgTx(ctx, version.OrganizationID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO versions (id, organization_id, product_id, name, status, milestone_label,
				planned_date, released_at, notes_ref, scope_frozen_at, version, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			version.ID, version.OrganizationID, version.ProductID, version.Name, string(version.Status),
			version.MilestoneLabel, version.PlannedDate, version.ReleasedAt, version.NotesRef,
			version.ScopeFrozenAt, version.Version, version.CreatedAt, version.UpdatedAt)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
				return apperr.Conflict("name", fmt.Sprintf("version %q already exists for this product", version.Name))
			}
			return fmt.Errorf("insert version: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.Version{}, err
	}
	return version, nil
}

func (r *Repository) GetVersion(ctx context.Context, organizationID, id uuid.UUID) (domain.Version, error) {
	var version domain.Version
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		var err error
		version, err = scanVersion(tx.QueryRowContext(ctx,
			`SELECT `+versionColumns+` FROM versions WHERE organization_id = $1 AND id = $2`,
			organizationID, id))
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Version{}, apperr.NotFound("version not found")
	}
	if err != nil {
		return domain.Version{}, err
	}
	return version, nil
}

func (r *Repository) ListVersions(ctx context.Context, organizationID, productID uuid.UUID) ([]domain.Version, error) {
	var items []domain.Version
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT `+versionColumns+` FROM versions
			 WHERE organization_id = $1 AND product_id = $2
			 ORDER BY created_at DESC`, organizationID, productID)
		if err != nil {
			return fmt.Errorf("list versions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			version, err := scanVersion(rows)
			if err != nil {
				return err
			}
			items = append(items, version)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []domain.Version{}
	}
	return items, nil
}

func (r *Repository) UpdateVersion(ctx context.Context, version domain.Version, expectedVersion int) (domain.Version, error) {
	err := r.db.InOrgTx(ctx, version.OrganizationID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE versions SET name = $1, status = $2, milestone_label = $3, planned_date = $4,
				released_at = $5, notes_ref = $6, scope_frozen_at = $7, version = $8, updated_at = $9
			 WHERE organization_id = $10 AND id = $11 AND version = $12`,
			version.Name, string(version.Status), version.MilestoneLabel, version.PlannedDate,
			version.ReleasedAt, version.NotesRef, version.ScopeFrozenAt, version.Version,
			version.UpdatedAt, version.OrganizationID, version.ID, expectedVersion)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
				return apperr.Conflict("name", "version name already exists")
			}
			return fmt.Errorf("update version: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return apperr.Conflict("version", "version was modified by someone else; reload and retry")
		}
		return nil
	})
	if err != nil {
		return domain.Version{}, err
	}
	return version, nil
}

// AssignItems 把工作项挂到版本上。已冻结的版本由应用层挡住，这里只做数据约束。
func (r *Repository) AssignItems(ctx context.Context, organizationID, versionID, productID uuid.UUID, itemIDs []uuid.UUID, actorID string) (int, error) {
	assigned := 0
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		for _, itemID := range itemIDs {
			var changedID uuid.UUID
			err := tx.QueryRowContext(ctx, `
				UPDATE work_items
				   SET version_id = $1, updated_at = now(), version = version + 1
				 WHERE organization_id = $2 AND id = $3 AND product_id = $4
				   AND (version_id IS NULL OR version_id <> $1)
				RETURNING id`,
				versionID, organizationID, itemID, productID).Scan(&changedID)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return fmt.Errorf("assign item to version: %w", err)
			}
			payload, _ := json.Marshal(map[string]any{"fields": []string{"version_id"}, "version_id": versionID.String()})
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO work_item_events (organization_id, work_item_id, event_type, actor_id, payload)
				VALUES ($1,$2,'updated',$3,$4)`,
				organizationID, itemID, actorID, payload); err != nil {
				return fmt.Errorf("insert version assignment event: %w", err)
			}
			assigned++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return assigned, nil
}

func (r *Repository) ProductIDByCode(ctx context.Context, organizationID uuid.UUID, code string) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT id FROM products WHERE organization_id = $1 AND key = $2`,
			organizationID, code).Scan(&id)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("load product by code: %w", err)
	}
	return id, true, nil
}

func (r *Repository) FindVersionByName(ctx context.Context, organizationID, productID uuid.UUID, name string) (domain.Version, bool, error) {
	var version domain.Version
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		var err error
		version, err = scanVersion(tx.QueryRowContext(ctx, `
			SELECT `+versionColumns+` FROM versions
			 WHERE organization_id = $1 AND product_id = $2 AND name = $3`,
			organizationID, productID, name))
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Version{}, false, nil
	}
	if err != nil {
		return domain.Version{}, false, fmt.Errorf("load version by name: %w", err)
	}
	return version, true, nil
}

func (r *Repository) ProductCode(ctx context.Context, organizationID, productID uuid.UUID) (string, error) {
	var code string
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx,
			`SELECT key FROM products WHERE organization_id = $1 AND id = $2`,
			organizationID, productID).Scan(&code)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", apperr.NotFound("product not found")
	}
	if err != nil {
		return "", fmt.Errorf("load product code: %w", err)
	}
	return code, nil
}

func (r *Repository) CountItems(ctx context.Context, organizationID, versionID uuid.UUID) (int, error) {
	var count int
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT count(*) FROM work_items WHERE organization_id = $1 AND version_id = $2`,
			organizationID, versionID).Scan(&count)
	})
	if err != nil {
		return 0, fmt.Errorf("count version items: %w", err)
	}
	return count, nil
}

// SubmitRelease 冻结版本范围、创建发布记录、写 E9 通知，三件事同事务。
func (r *Repository) SubmitRelease(ctx context.Context, version domain.Version, expectedVersion int, release domain.ReleaseRecord, outboxEvents []outbox.Event) (domain.Version, domain.ReleaseRecord, error) {
	err := r.db.InOrgTx(ctx, version.OrganizationID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE versions SET scope_frozen_at = $1, version = $2, updated_at = $3
			 WHERE organization_id = $4 AND id = $5 AND version = $6 AND scope_frozen_at IS NULL`,
			version.ScopeFrozenAt, version.Version, version.UpdatedAt,
			version.OrganizationID, version.ID, expectedVersion)
		if err != nil {
			return fmt.Errorf("freeze version scope: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return apperr.Conflict("scope", "version scope was already frozen or modified")
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO release_records (id, organization_id, version_id, status, tag, commit_from,
				commit_to, submitted_by, approval_instance_id, approver_id, evidence_ref,
				failure_reason, released_by, released_at, version, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
			release.ID, release.OrganizationID, release.VersionID, string(release.Status), release.Tag,
			release.CommitFrom, release.CommitTo, release.SubmittedBy, release.ApprovalInstanceID,
			release.ApproverID, release.EvidenceRef, release.FailureReason, release.ReleasedBy,
			release.ReleasedAt, release.Version, release.CreatedAt, release.UpdatedAt)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
				return apperr.Conflict("release", "a release attempt is already in flight for this version")
			}
			return fmt.Errorf("insert release record: %w", err)
		}
		for _, event := range outboxEvents {
			if err := outbox.NewRepository().Insert(ctx, tx, event); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return domain.Version{}, domain.ReleaseRecord{}, err
	}
	return version, release, nil
}

func (r *Repository) GetRelease(ctx context.Context, organizationID, id uuid.UUID) (domain.ReleaseRecord, error) {
	var release domain.ReleaseRecord
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		var err error
		release, err = scanRelease(tx.QueryRowContext(ctx,
			`SELECT `+releaseColumns+` FROM release_records WHERE organization_id = $1 AND id = $2`,
			organizationID, id))
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ReleaseRecord{}, apperr.NotFound("release record not found")
	}
	if err != nil {
		return domain.ReleaseRecord{}, err
	}
	return release, nil
}

func (r *Repository) ListReleases(ctx context.Context, organizationID, versionID uuid.UUID) ([]domain.ReleaseRecord, error) {
	var items []domain.ReleaseRecord
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT `+releaseColumns+` FROM release_records
			 WHERE organization_id = $1 AND version_id = $2 ORDER BY created_at DESC`,
			organizationID, versionID)
		if err != nil {
			return fmt.Errorf("list releases: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			release, err := scanRelease(rows)
			if err != nil {
				return err
			}
			items = append(items, release)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []domain.ReleaseRecord{}
	}
	return items, nil
}

func (r *Repository) FindInflightRelease(ctx context.Context, organizationID, versionID uuid.UUID) (domain.ReleaseRecord, bool, error) {
	var release domain.ReleaseRecord
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		var err error
		release, err = scanRelease(tx.QueryRowContext(ctx, `
			SELECT `+releaseColumns+` FROM release_records
			 WHERE organization_id = $1 AND version_id = $2
			   AND status IN ('PENDING_APPROVAL','APPROVED','TAGGED')
			 ORDER BY created_at DESC LIMIT 1`, organizationID, versionID))
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ReleaseRecord{}, false, nil
	}
	if err != nil {
		return domain.ReleaseRecord{}, false, err
	}
	return release, true, nil
}

// UpdateRelease 更新发布记录，可选地把版本置为已发布，并写 E6。
func (r *Repository) UpdateRelease(ctx context.Context, release domain.ReleaseRecord, expectedVersion int, publishedVersion *domain.Version, outboxEvents []outbox.Event) (domain.ReleaseRecord, error) {
	err := r.db.InOrgTx(ctx, release.OrganizationID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE release_records SET status = $1, tag = $2, approval_instance_id = $3,
				approver_id = $4, evidence_ref = $5, failure_reason = $6, released_by = $7,
				released_at = $8, version = $9, updated_at = $10
			 WHERE organization_id = $11 AND id = $12 AND version = $13`,
			string(release.Status), release.Tag, release.ApprovalInstanceID, release.ApproverID,
			release.EvidenceRef, release.FailureReason, release.ReleasedBy, release.ReleasedAt,
			release.Version, release.UpdatedAt, release.OrganizationID, release.ID, expectedVersion)
		if err != nil {
			return fmt.Errorf("update release record: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return apperr.Conflict("version", "release record was modified by someone else")
		}
		if publishedVersion != nil {
			if _, err := tx.ExecContext(ctx, `
				UPDATE versions SET status = $1, released_at = $2, updated_at = $3, version = version + 1
				 WHERE organization_id = $4 AND id = $5`,
				string(publishedVersion.Status), publishedVersion.ReleasedAt,
				publishedVersion.UpdatedAt, publishedVersion.OrganizationID,
				publishedVersion.ID); err != nil {
				return fmt.Errorf("mark version released: %w", err)
			}
		}
		for _, event := range outboxEvents {
			if err := outbox.NewRepository().Insert(ctx, tx, event); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return domain.ReleaseRecord{}, err
	}
	return release, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanVersion(row rowScanner) (domain.Version, error) {
	var (
		version domain.Version
		status  string
	)
	if err := row.Scan(&version.ID, &version.OrganizationID, &version.ProductID, &version.Name,
		&status, &version.MilestoneLabel, &version.PlannedDate, &version.ReleasedAt,
		&version.NotesRef, &version.ScopeFrozenAt, &version.Version, &version.CreatedAt,
		&version.UpdatedAt); err != nil {
		return domain.Version{}, err
	}
	version.Status = domain.Status(status)
	return version, nil
}

func scanRelease(row rowScanner) (domain.ReleaseRecord, error) {
	var (
		release domain.ReleaseRecord
		status  string
	)
	if err := row.Scan(&release.ID, &release.OrganizationID, &release.VersionID, &status,
		&release.Tag, &release.CommitFrom, &release.CommitTo, &release.SubmittedBy,
		&release.ApprovalInstanceID, &release.ApproverID, &release.EvidenceRef,
		&release.FailureReason, &release.ReleasedBy, &release.ReleasedAt, &release.Version,
		&release.CreatedAt, &release.UpdatedAt); err != nil {
		return domain.ReleaseRecord{}, err
	}
	release.Status = domain.ReleaseStatus(status)
	return release, nil
}
