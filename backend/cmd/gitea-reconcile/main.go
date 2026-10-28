// devmaster-gitea-reconcile 做 Gitea 每日对账：补齐 webhook 丢失的提交关联。
//
// 只靠 webhook 的关联率在真实团队里通常 70%–85%；对账把水位之后的提交
// 重新扫一遍，幂等 upsert，最后打印关联率。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/gitea/adapter/postgres"
	"github.com/xiaofeiwu/devmaster/backend/internal/gitea/application"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/config"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
	giteaclient "github.com/xiaofeiwu/devmaster/backend/internal/platform/gitea"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/logging"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "devmaster-gitea-reconcile failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "path to YAML configuration file")
	organizationID := flag.String("organization-id", "", "organization to reconcile")
	repoFlag := flag.String("repo", "", "repository full name (owner/repo); default gitea.owner/repo")
	sinceFlag := flag.String("since", "", "RFC3339 start time; default repo cursor or 30 days ago")
	flag.Parse()

	cfg, err := config.Load(firstNonEmpty(*configPath, os.Getenv("DEVMASTER_CONFIG_FILE")))
	if err != nil {
		return err
	}
	logger := logging.New(cfg.Logging)

	orgValue := firstNonEmpty(*organizationID, cfg.Gitea.OrganizationID)
	org, err := uuid.Parse(orgValue)
	if err != nil {
		return fmt.Errorf("organization-id: %w", err)
	}
	repo := firstNonEmpty(*repoFlag, cfg.Gitea.Owner+"/"+cfg.Gitea.Repo)
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("repo must be owner/repo, got %q", repo)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	db, err := database.Open(ctx, cfg.Database, logger)
	if err != nil {
		return err
	}
	defer db.Close()

	service := application.NewService(postgres.NewRepository(db))
	client := giteaclient.New(cfg.Gitea)

	since := time.Now().UTC().AddDate(0, 0, -30)
	if *sinceFlag != "" {
		parsed, err := time.Parse(time.RFC3339, *sinceFlag)
		if err != nil {
			return fmt.Errorf("since must be RFC3339: %w", err)
		}
		since = parsed
	} else if cursor, _, err := service.Cursor(ctx, org, repo); err != nil {
		return err
	} else if !cursor.IsZero() {
		since = cursor
	}

	totalLinked := 0
	latestAt := time.Time{}
	latestSHA := ""
	for page := 1; ; page++ {
		commits, err := client.ListCommits(ctx, parts[0], parts[1], since, page, 100)
		if err != nil {
			return err
		}
		if len(commits) == 0 {
			break
		}
		converted := make([]application.Commit, 0, len(commits))
		for _, commit := range commits {
			converted = append(converted, application.Commit{
				SHA:         commit.SHA,
				Message:     commit.Message,
				CommittedAt: commit.CommittedAt,
				AuthorID:    commit.AuthorID,
			})
			if commit.CommittedAt.After(latestAt) {
				latestAt = commit.CommittedAt
				latestSHA = commit.SHA
			}
		}
		linked, err := service.HandlePush(ctx, org, repo, "", converted)
		if err != nil {
			return err
		}
		totalLinked += linked
		if len(commits) < 100 {
			break
		}
	}
	if !latestAt.IsZero() {
		if err := service.AdvanceCursor(ctx, org, repo, latestAt, latestSHA); err != nil {
			return err
		}
	}
	rate, err := service.LinkRate(ctx, org)
	if err != nil {
		return err
	}
	fmt.Printf("对账完成：repo=%s since=%s 新增关联=%d 关联率=%.1f%%\n",
		repo, since.Format(time.RFC3339), totalLinked, rate*100)
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
