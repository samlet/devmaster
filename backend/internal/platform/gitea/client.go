// Package gitea 是 Gitea API 的最小客户端：目前只需要建 tag。
package gitea

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/config"
)

type Client struct {
	cfg  config.GiteaConfig
	http *http.Client
}

func New(cfg config.GiteaConfig) *Client {
	return &Client{cfg: cfg, http: &http.Client{Timeout: 15 * time.Second}}
}

// EnsureTag 建 tag；已存在视为成功（幂等）。
func (c *Client) EnsureTag(ctx context.Context, tag, target, message string) error {
	if strings.TrimSpace(c.cfg.Token) == "" || strings.TrimSpace(c.cfg.Owner) == "" || strings.TrimSpace(c.cfg.Repo) == "" {
		return fmt.Errorf("gitea is not configured (token/owner/repo)")
	}
	payload, err := json.Marshal(map[string]string{
		"tag_name": tag,
		"target":   target,
		"message":  message,
	})
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/tags",
		strings.TrimRight(c.cfg.BaseURL, "/"), c.cfg.Owner, c.cfg.Repo)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "token "+c.cfg.Token)
	request.Header.Set("Content-Type", "application/json")

	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("create tag %s: %w", tag, err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	switch response.StatusCode {
	case http.StatusCreated, http.StatusOK:
		return nil
	case http.StatusConflict:
		// tag 已存在：上一次发布成功但后续步骤失败时会走到这里，视为成功。
		return nil
	default:
		return fmt.Errorf("create tag %s returned %d: %s", tag, response.StatusCode, truncate(string(body), 300))
	}
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

type Commit struct {
	SHA         string
	Message     string
	CommittedAt time.Time
	AuthorID    string
}

type apiCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message string `json:"message"`
		Author  struct {
			Date time.Time `json:"date"`
		} `json:"author"`
	} `json:"commit"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
}

// ListCommits 拉取仓库提交（对账用）。Gitea 的 limit 上限 100。
func (c *Client) ListCommits(ctx context.Context, owner, repo string, since time.Time, page, limit int) ([]Commit, error) {
	if strings.TrimSpace(c.cfg.Token) == "" {
		return nil, fmt.Errorf("gitea is not configured (token)")
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/commits?page=%d&limit=%d",
		strings.TrimRight(c.cfg.BaseURL, "/"), owner, repo, page, limit)
	if !since.IsZero() {
		url += "&since=" + since.UTC().Format(time.RFC3339)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "token "+c.cfg.Token)

	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("list commits: %w", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 5<<20))
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list commits returned %d: %s", response.StatusCode, truncate(string(body), 300))
	}
	var raw []apiCommit
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode commits: %w", err)
	}
	out := make([]Commit, 0, len(raw))
	for _, item := range raw {
		out = append(out, Commit{
			SHA:         item.SHA,
			Message:     item.Commit.Message,
			CommittedAt: item.Commit.Author.Date,
			AuthorID:    item.Author.Login,
		})
	}
	return out, nil
}
