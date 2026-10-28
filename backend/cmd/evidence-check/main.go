// devmaster-evidence-check 用真实配置对 artifact 走一次完整的证据存证并回读校验。
//
// 它是 make test-evidence 的实现，也是排查「存证为什么失败」的最小工具：
// 失败时把具体是哪一步（建内容/写资源/密封/回读）打出来，而不是只在发布链
// 里看到一句笼统的 activity failed。
//
// 退出码：0 存证并回读校验通过；1 存证失败；2 用法或配置错误。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/artifact"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "path to YAML configuration file")
	title := flag.String("title", "", "evidence title (default: probe title with a random suffix)")
	payload := flag.String("payload", "", "evidence text (default: built-in probe payload)")
	flag.Parse()

	cfg, err := config.Load(firstNonEmpty(*configPath, os.Getenv("DEVMASTER_CONFIG_FILE")))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if !cfg.Artifact.Enabled {
		fmt.Fprintln(os.Stderr, "artifact.enabled is false; nothing to check")
		os.Exit(2)
	}

	// 默认标题带随机后缀：probe 的语义是「现在能存证吗」，不是「复用上次的
	// 证据」。要验幂等回放就显式传同一个 -title 和 -payload。
	probeTitle := *title
	if probeTitle == "" {
		probeTitle = "devmaster evidence probe " + uuid.NewString()
	}
	text := *payload
	if strings.TrimSpace(text) == "" {
		text = "# 证据存证自检\n\n这份文本由 devmaster-evidence-check 写入并回读校验。\n"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, err := artifact.New(ctx, cfg.Artifact)
	if err != nil {
		fmt.Fprintf(os.Stderr, "artifact client: %v\n", err)
		os.Exit(2)
	}
	defer client.Close()

	sealed, err := client.SealEvidence(ctx, artifact.Evidence{
		ContentType: client.EvidenceType(),
		Title:       probeTitle,
		Metadata: map[string]any{
			"source":  "devmaster-evidence-check",
			"version": "probe",
		},
		Role: "evidence",
		Text: text,
	})
	if err != nil {
		return err
	}
	fmt.Printf("ref=%s\n", sealed.Ref)
	fmt.Printf("content_id=%s\n", sealed.ContentID)
	fmt.Printf("revision_id=%s\n", sealed.RevisionID)
	fmt.Printf("payload_sha256=%s\n", sealed.PayloadSHA256)
	fmt.Printf("manifest_hash=%s\n", sealed.ManifestHash)
	fmt.Printf("replayed=%t\n", sealed.Replayed)
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
