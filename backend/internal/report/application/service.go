// Package application 是报表的读模型：只查 work_items + work_item_events。
package application

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
	"github.com/xiaofeiwu/devmaster/backend/internal/report/domain"
)

type Actor struct {
	Subject        string
	OrganizationID uuid.UUID
}

type CycleSample struct {
	Type  string
	Hours float64
}

type ThroughputBucket struct {
	Week  time.Time
	Type  string
	Count int
}

type LoadRow struct {
	Assignee   string
	OpenItems  int
	WIPItems   int
	OpenPoints int
}

type CFDPoint struct {
	Date       time.Time
	Todo       int
	InProgress int
	InReview   int
	Done       int
}

type Repository interface {
	CycleSamples(ctx context.Context, organizationID uuid.UUID, productID *uuid.UUID, from, to time.Time) ([]CycleSample, error)
	Throughput(ctx context.Context, organizationID uuid.UUID, productID *uuid.UUID, from, to time.Time) ([]ThroughputBucket, error)
	TeamLoad(ctx context.Context, organizationID uuid.UUID, productID *uuid.UUID) ([]LoadRow, error)
	CFD(ctx context.Context, organizationID uuid.UUID, productID *uuid.UUID, from, to time.Time) ([]CFDPoint, error)
}

type CycleSummary struct {
	Type  string  `json:"type"`
	P50   float64 `json:"p50_hours"`
	P85   float64 `json:"p85_hours"`
	P95   float64 `json:"p95_hours"`
	Count int     `json:"count"`
}

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service { return &Service{repo: repo} }

func (s *Service) CycleTime(ctx context.Context, actor Actor, productID *uuid.UUID, from, to time.Time) ([]CycleSummary, error) {
	if err := validateRange(from, to); err != nil {
		return nil, err
	}
	samples, err := s.repo.CycleSamples(ctx, actor.OrganizationID, productID, from, to)
	if err != nil {
		return nil, err
	}
	byType := map[string][]float64{}
	for _, sample := range samples {
		byType[sample.Type] = append(byType[sample.Type], sample.Hours)
	}
	out := make([]CycleSummary, 0, len(byType))
	for itemType, values := range byType {
		summary, ok := domain.Summarize(values, 5)
		if !ok {
			continue
		}
		out = append(out, CycleSummary{
			Type: itemType, P50: round1(summary.P50), P85: round1(summary.P85),
			P95: round1(summary.P95), Count: summary.Count,
		})
	}
	return out, nil
}

func (s *Service) Throughput(ctx context.Context, actor Actor, productID *uuid.UUID, from, to time.Time) ([]ThroughputBucket, error) {
	if err := validateRange(from, to); err != nil {
		return nil, err
	}
	return s.repo.Throughput(ctx, actor.OrganizationID, productID, from, to)
}

func (s *Service) TeamLoad(ctx context.Context, actor Actor, productID *uuid.UUID) ([]LoadRow, error) {
	return s.repo.TeamLoad(ctx, actor.OrganizationID, productID)
}

func (s *Service) CFD(ctx context.Context, actor Actor, productID *uuid.UUID, from, to time.Time) ([]CFDPoint, error) {
	if err := validateRange(from, to); err != nil {
		return nil, err
	}
	if to.Sub(from) > 90*24*time.Hour {
		return nil, apperr.Invalid("range", "cumulative flow supports at most 90 days")
	}
	return s.repo.CFD(ctx, actor.OrganizationID, productID, from, to)
}

func validateRange(from, to time.Time) error {
	if !to.After(from) {
		return apperr.Invalid("range", "to must be after from")
	}
	if to.Sub(from) > 366*24*time.Hour {
		return apperr.Invalid("range", "range must be within 366 days")
	}
	return nil
}

func round1(value float64) float64 {
	return float64(int64(value*10+0.5)) / 10
}
