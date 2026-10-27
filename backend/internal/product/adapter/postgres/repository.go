// Package postgres 是产品仓储的持久化实现。
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/outbox"
	"github.com/xiaofeiwu/devmaster/backend/internal/product/application"
	"github.com/xiaofeiwu/devmaster/backend/internal/product/domain"
)

const uniqueViolation = "23505"

type Repository struct {
	db     *database.DB
	outbox *outbox.Repository
}

var _ application.Repository = (*Repository)(nil)

func NewRepository(db *database.DB) *Repository {
	return &Repository{db: db, outbox: outbox.NewRepository()}
}

func (r *Repository) Create(ctx context.Context, product domain.Product, events []outbox.Event) (domain.Product, error) {
	err := r.db.InOrgTx(ctx, product.OrganizationID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO products (id, organization_id, key, name, description, status, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			product.ID, product.OrganizationID, product.Key, product.Name, product.Description,
			string(product.Status), product.CreatedAt, product.UpdatedAt)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
				return apperr.Conflict("key", fmt.Sprintf("product key %q already exists", product.Key))
			}
			return fmt.Errorf("insert product: %w", err)
		}
		for _, event := range events {
			if err := r.outbox.Insert(ctx, tx, event); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return domain.Product{}, err
	}
	return product, nil
}

func (r *Repository) GetByID(ctx context.Context, organizationID, id uuid.UUID) (domain.Product, error) {
	var product domain.Product
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `
			SELECT id, organization_id, key, name, description, status, created_at, updated_at
			  FROM products
			 WHERE organization_id = $1 AND id = $2`, organizationID, id).
			Scan(&product.ID, &product.OrganizationID, &product.Key, &product.Name,
				&product.Description, &product.Status, &product.CreatedAt, &product.UpdatedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Product{}, apperr.NotFound("product not found")
	}
	if err != nil {
		return domain.Product{}, fmt.Errorf("load product: %w", err)
	}
	return product, nil
}

func (r *Repository) GetByKey(ctx context.Context, organizationID uuid.UUID, key string) (domain.Product, error) {
	var product domain.Product
	normalized := strings.ToUpper(strings.TrimSpace(key))
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `
			SELECT id, organization_id, key, name, description, status, created_at, updated_at
			  FROM products
			 WHERE organization_id = $1 AND key = $2`, organizationID, normalized).
			Scan(&product.ID, &product.OrganizationID, &product.Key, &product.Name,
				&product.Description, &product.Status, &product.CreatedAt, &product.UpdatedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Product{}, apperr.NotFound(fmt.Sprintf("product %q not found", normalized))
	}
	if err != nil {
		return domain.Product{}, fmt.Errorf("load product by key: %w", err)
	}
	return product, nil
}

func (r *Repository) List(ctx context.Context, organizationID uuid.UUID, filter application.ListFilter) ([]domain.Product, int, error) {
	var (
		items []domain.Product
		total int
	)
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		where := ` WHERE organization_id = $1`
		args := []any{organizationID}
		if filter.Status != "" {
			args = append(args, string(filter.Status))
			where += fmt.Sprintf(" AND status = $%d", len(args))
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM products`+where, args...).Scan(&total); err != nil {
			return fmt.Errorf("count products: %w", err)
		}
		limit, offset := filter.Limit, filter.Offset
		if limit <= 0 || limit > 200 {
			limit = 50
		}
		args = append(args, limit, offset)
		rows, err := tx.QueryContext(ctx, `
			SELECT id, organization_id, key, name, description, status, created_at, updated_at
			  FROM products`+where+fmt.Sprintf(`
			 ORDER BY key
			 LIMIT $%d OFFSET $%d`, len(args)-1, len(args)), args...)
		if err != nil {
			return fmt.Errorf("list products: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var product domain.Product
			if err := rows.Scan(&product.ID, &product.OrganizationID, &product.Key, &product.Name,
				&product.Description, &product.Status, &product.CreatedAt, &product.UpdatedAt); err != nil {
				return fmt.Errorf("scan product: %w", err)
			}
			items = append(items, product)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, 0, err
	}
	if items == nil {
		items = []domain.Product{}
	}
	return items, total, nil
}
