// Package application 处理 Gitea 入站：把提交消息里的工作项 key 变成关联事实。
package application

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// keyPattern 与工作项编号一致。只匹配形如 HOSP-123 的串，
// 避免把 "UTF-8" 这类字符串误判成工作项。
var keyPattern = regexp.MustCompile(`\b[A-Z][A-Z0-9]{1,9}-[0-9]+\b`)

type Commit struct {
	SHA         string
	Message     string
	Branch      string
	CommittedAt time.Time
	AuthorID    string
}

type PullRequest struct {
	Number     int
	State      string // OPEN / CLOSED / MERGED
	HeadBranch string
}

type Repository interface {
	RecordDelivery(ctx context.Context, organizationID uuid.UUID, deliveryID, eventType string, payload []byte) (bool, error)
	ProductKeys(ctx context.Context, organizationID uuid.UUID) ([]string, error)
	ResolveWorkItem(ctx context.Context, organizationID uuid.UUID, key string) (uuid.UUID, bool, error)
	UpsertCommit(ctx context.Context, organizationID, workItemID uuid.UUID, repo, branch, sha string, committedAt time.Time, authorID string) (bool, error)
	UpdatePullRequest(ctx context.Context, organizationID uuid.UUID, repo, branch string, prNo int, state string) (int, error)
	Cursor(ctx context.Context, organizationID uuid.UUID, repo string) (time.Time, string, error)
	AdvanceCursor(ctx context.Context, organizationID uuid.UUID, repo string, at time.Time, sha string) error
	LinkStats(ctx context.Context, organizationID uuid.UUID) (linked, denominator int, err error)
}

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service { return &Service{repo: repo} }

func (s *Service) RecordDelivery(ctx context.Context, organizationID uuid.UUID, deliveryID, eventType string, payload []byte) (bool, error) {
	return s.repo.RecordDelivery(ctx, organizationID, deliveryID, eventType, payload)
}

// HandlePush 把每个 commit 关联到消息里提到的工作项。
// 返回新增关联数（重复投递时为 0）。
func (s *Service) HandlePush(ctx context.Context, organizationID uuid.UUID, repo, branch string, commits []Commit) (int, error) {
	productKeys, err := s.repo.ProductKeys(ctx, organizationID)
	if err != nil {
		return 0, err
	}
	known := make(map[string]bool, len(productKeys))
	for _, key := range productKeys {
		known[key] = true
	}
	linked := 0
	for _, commit := range commits {
		for _, key := range FilterByProducts(ParseKeys(commit.Message), known) {
			workItemID, found, err := s.repo.ResolveWorkItem(ctx, organizationID, key)
			if err != nil {
				return linked, err
			}
			if !found {
				continue
			}
			inserted, err := s.repo.UpsertCommit(ctx, organizationID, workItemID, repo, branch,
				commit.SHA, commit.CommittedAt, commit.AuthorID)
			if err != nil {
				return linked, err
			}
			if inserted {
				linked++
			}
		}
	}
	return linked, nil
}

// HandlePullRequest 按 head 分支回填 PR 状态。webhook 不带 commit 列表，
// 分支上的提交在 push 时已经落库，这里只更新状态。
func (s *Service) HandlePullRequest(ctx context.Context, organizationID uuid.UUID, repo string, pr PullRequest) (int, error) {
	return s.repo.UpdatePullRequest(ctx, organizationID, repo, pr.HeadBranch, pr.Number, pr.State)
}

func (s *Service) Cursor(ctx context.Context, organizationID uuid.UUID, repo string) (time.Time, string, error) {
	return s.repo.Cursor(ctx, organizationID, repo)
}

func (s *Service) AdvanceCursor(ctx context.Context, organizationID uuid.UUID, repo string, at time.Time, sha string) error {
	return s.repo.AdvanceCursor(ctx, organizationID, repo, at, sha)
}

// LinkRate 返回提交关联率：有关联的工作项数 / 应有提交的工作项数。
func (s *Service) LinkRate(ctx context.Context, organizationID uuid.UUID) (float64, error) {
	linked, denominator, err := s.repo.LinkStats(ctx, organizationID)
	if err != nil {
		return 0, err
	}
	if denominator == 0 {
		return 1, nil
	}
	return float64(linked) / float64(denominator), nil
}

// FilterByProducts 丢掉不属于本组织的产品前缀。
// 正则本身会把 "UTF-8" 当成 key（UTF 是合法前缀形态），必须靠已知产品集合过滤。
func FilterByProducts(keys []string, products map[string]bool) []string {
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		index := strings.LastIndex(key, "-")
		if index <= 0 {
			continue
		}
		if products[key[:index]] {
			out = append(out, key)
		}
	}
	return out
}

// ParseKeys 提取提交消息里的工作项 key（去重，保序）。
func ParseKeys(message string) []string {
	matches := keyPattern.FindAllString(message, -1)
	seen := map[string]bool{}
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		key := strings.ToUpper(strings.TrimSpace(match))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out
}
