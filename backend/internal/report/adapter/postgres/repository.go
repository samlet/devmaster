// Package postgres 是报表的 SQL 读模型。
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"

	boarddomain "github.com/xiaofeiwu/devmaster/backend/internal/board/domain"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
	"github.com/xiaofeiwu/devmaster/backend/internal/report/application"
	workitemdomain "github.com/xiaofeiwu/devmaster/backend/internal/workitem/domain"
)

const doneStatuses = `('DONE','CLOSED','VERIFIED','WONTFIX')`

type Repository struct {
	db *database.DB
}

var _ application.Repository = (*Repository)(nil)

func NewRepository(db *database.DB) *Repository { return &Repository{db: db} }

func (r *Repository) CycleSamples(ctx context.Context, organizationID uuid.UUID, productID *uuid.UUID, from, to time.Time) ([]application.CycleSample, error) {
	var samples []application.CycleSample
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		query := `
			WITH done AS (
				SELECT w.id, w.type, w.created_at, min(e.occurred_at) AS done_at
				  FROM work_items w
				  JOIN work_item_events e ON e.work_item_id = w.id
				 WHERE w.organization_id = $1
				   AND e.to_status IN ` + doneStatuses + `
				   AND e.occurred_at >= $2 AND e.occurred_at < $3`
		args := []any{organizationID, from, to}
		if productID != nil {
			args = append(args, *productID)
			query += fmt.Sprintf(" AND w.product_id = $%d", len(args))
		}
		query += `
				 GROUP BY w.id, w.type, w.created_at
			)
			SELECT type, extract(epoch FROM (done_at - created_at)) / 3600.0 FROM done`
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("cycle samples: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var sample application.CycleSample
			if err := rows.Scan(&sample.Type, &sample.Hours); err != nil {
				return err
			}
			samples = append(samples, sample)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return samples, nil
}

func (r *Repository) Throughput(ctx context.Context, organizationID uuid.UUID, productID *uuid.UUID, from, to time.Time) ([]application.ThroughputBucket, error) {
	var buckets []application.ThroughputBucket
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		query := `
			WITH done AS (
				SELECT w.id, w.type, min(e.occurred_at) AS done_at
				  FROM work_items w
				  JOIN work_item_events e ON e.work_item_id = w.id
				 WHERE w.organization_id = $1
				   AND e.to_status IN ` + doneStatuses + `
				   AND e.occurred_at >= $2 AND e.occurred_at < $3`
		args := []any{organizationID, from, to}
		if productID != nil {
			args = append(args, *productID)
			query += fmt.Sprintf(" AND w.product_id = $%d", len(args))
		}
		query += `
				 GROUP BY w.id, w.type
			)
			SELECT date_trunc('week', done_at)::date, type, count(*)
			  FROM done GROUP BY 1, 2 ORDER BY 1, 2`
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("throughput: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var bucket application.ThroughputBucket
			if err := rows.Scan(&bucket.Week, &bucket.Type, &bucket.Count); err != nil {
				return err
			}
			buckets = append(buckets, bucket)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return buckets, nil
}

func (r *Repository) TeamLoad(ctx context.Context, organizationID uuid.UUID, productID *uuid.UUID) ([]application.LoadRow, error) {
	var rowsOut []application.LoadRow
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		query := `
			SELECT coalesce(nullif(btrim(assignee_id), ''), 'UNASSIGNED'),
			       count(*) FILTER (WHERE status NOT IN ('DONE','CLOSED','VERIFIED','WONTFIX','CANCELLED')),
			       count(*) FILTER (WHERE status IN ('IN_PROGRESS','IN_REVIEW','RESOLVED')),
			       coalesce(sum(story_points) FILTER (
			           WHERE status NOT IN ('DONE','CLOSED','VERIFIED','WONTFIX','CANCELLED')), 0)
			  FROM work_items
			 WHERE organization_id = $1`
		args := []any{organizationID}
		if productID != nil {
			args = append(args, *productID)
			query += fmt.Sprintf(" AND product_id = $%d", len(args))
		}
		query += ` GROUP BY 1 ORDER BY 2 DESC, 1`
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("team load: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var row application.LoadRow
			if err := rows.Scan(&row.Assignee, &row.OpenItems, &row.WIPItems, &row.OpenPoints); err != nil {
				return err
			}
			rowsOut = append(rowsOut, row)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return rowsOut, nil
}

// CFD 用"当前存量 − 窗口内事件"倒推窗口起点，再按天正推。
//
// 只扫窗口内的事件（14 天约 5 万条），不对全部历史做 DISTINCT ON；
// 10 万项下这是 1.5s → 100ms 级别的差别。
func (r *Repository) CFD(ctx context.Context, organizationID uuid.UUID, productID *uuid.UUID, from, to time.Time) ([]application.CFDPoint, error) {
	var points []application.CFDPoint
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		counts := map[boarddomain.Column]int{}
		currentQuery := `SELECT status, count(*) FROM work_items WHERE organization_id = $1`
		currentArgs := []any{organizationID}
		if productID != nil {
			currentArgs = append(currentArgs, *productID)
			currentQuery += fmt.Sprintf(" AND product_id = $%d", len(currentArgs))
		}
		currentQuery += " GROUP BY status"
		rows, err := tx.QueryContext(ctx, currentQuery, currentArgs...)
		if err != nil {
			return fmt.Errorf("cfd current: %w", err)
		}
		for rows.Next() {
			var (
				status string
				count  int
			)
			if err := rows.Scan(&status, &count); err != nil {
				rows.Close()
				return err
			}
			if column, ok := boarddomain.ColumnFor(workitemdomain.Status(status)); ok {
				counts[column] += count
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		type event struct {
			at   time.Time
			from *string
			to   string
		}
		now := time.Now().UTC()
		eventQuery := `
			SELECT e.occurred_at, e.from_status, e.to_status
			  FROM work_item_events e
			  JOIN work_items w ON w.id = e.work_item_id
			 WHERE e.organization_id = $1 AND e.occurred_at >= $2 AND e.occurred_at <= $3
			   AND e.to_status IS NOT NULL`
		eventArgs := []any{organizationID, from, now}
		if productID != nil {
			eventArgs = append(eventArgs, *productID)
			eventQuery += fmt.Sprintf(" AND w.product_id = $%d", len(eventArgs))
		}
		eventQuery += ` ORDER BY e.occurred_at, e.id`
		eventRows, err := tx.QueryContext(ctx, eventQuery, eventArgs...)
		if err != nil {
			return fmt.Errorf("cfd events: %w", err)
		}
		var events []event
		for eventRows.Next() {
			var current event
			if err := eventRows.Scan(&current.at, &current.from, &current.to); err != nil {
				eventRows.Close()
				return err
			}
			events = append(events, current)
		}
		eventRows.Close()
		if err := eventRows.Err(); err != nil {
			return err
		}

		// 倒推：把窗口内的事件从当前存量里减回去，得到 from 时点的存量。
		for i := len(events) - 1; i >= 0; i-- {
			if column, ok := boarddomain.ColumnFor(workitemdomain.Status(events[i].to)); ok {
				counts[column]--
			}
			if events[i].from != nil {
				if column, ok := boarddomain.ColumnFor(workitemdomain.Status(*events[i].from)); ok {
					counts[column]++
				}
			}
		}

		index := 0
		for day := from.UTC().Truncate(24 * time.Hour); day.Before(to); day = day.AddDate(0, 0, 1) {
			end := day.AddDate(0, 0, 1)
			for index < len(events) && events[index].at.Before(end) {
				if column, ok := boarddomain.ColumnFor(workitemdomain.Status(events[index].to)); ok {
					counts[column]++
				}
				if events[index].from != nil {
					if column, ok := boarddomain.ColumnFor(workitemdomain.Status(*events[index].from)); ok {
						counts[column]--
					}
				}
				index++
			}
			points = append(points, application.CFDPoint{
				Date:       day,
				Todo:       counts[boarddomain.ColumnTodo],
				InProgress: counts[boarddomain.ColumnInProgress],
				InReview:   counts[boarddomain.ColumnInReview],
				Done:       counts[boarddomain.ColumnDone],
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return points, nil
}
