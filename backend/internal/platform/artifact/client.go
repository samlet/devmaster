// Package artifact 是 artifact（内容与证据仓）的最小 gRPC 客户端。
//
// 它只做一件事：把一份证据文本封成一个不可变修订，并回读校验字节一致。
// devmaster 不代理字节、不存预签名 URL —— 只存 content_id:revision_id 这种
// 固定版本引用（docs/06-集成契约与事件设计.md §8）。
//
// 幂等键带载荷指纹：同一份证据重试会命中 artifact 的幂等记录并回放同一
// 修订，不会在 artifact 里堆出多份内容。指纹变了就是另一份证据，这是刻意
// 的——同一版本重新生成证据说明前一份不再可信。
package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	contentv1 "github.com/xiaofeiwu/artifact/gen/go/content/v1"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/config"
)

// Evidence 是一份待存证的内容。Role 是 artifact 侧的资源角色，必须与
// registry 里 content type 的 allowed_roles 一致（devmaster 统一用 evidence）。
type Evidence struct {
	ContentType string
	Title       string
	Metadata    map[string]any
	Role        string
	Text        string
}

// Sealed 是一次存证的结果。Ref 是唯一该被持久化的东西。
type Sealed struct {
	ContentID     string
	RevisionID    string
	RevisionNo    uint32
	Ref           string
	PayloadSHA256 string
	ManifestHash  string
	Replayed      bool
}

// Client 是 artifact ContentService 的客户端。零值不可用，用 New 构造。
type Client struct {
	content contentv1.ContentServiceClient
	conn    *grpc.ClientConn
	cfg     config.ArtifactConfig
	tenant  uuid.UUID
	org     uuid.UUID
	tokens  oauth2.TokenSource
}

// New 建立连接与令牌源。配置不完整时直接报错——存证是发布链的一环，
// 「静默降级成没存证」比启动失败更危险。
//
// dialOptions 仅供测试注入（bufconn 等）；生产调用不传。
func New(ctx context.Context, cfg config.ArtifactConfig, dialOptions ...grpc.DialOption) (*Client, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	tenant, err := uuid.Parse(cfg.TenantID)
	if err != nil || tenant == uuid.Nil {
		return nil, fmt.Errorf("artifact tenant_id must be a nonzero UUID")
	}
	org, err := uuid.Parse(cfg.OrganizationID)
	if err != nil || org == uuid.Nil {
		return nil, fmt.Errorf("artifact organization_id must be a nonzero UUID")
	}
	tokenURL := strings.TrimSpace(cfg.TokenURL)
	if tokenURL == "" {
		tokenURL, err = discoverTokenURL(ctx, cfg.Issuer)
		if err != nil {
			return nil, err
		}
	}
	if cfg.TLS {
		return nil, fmt.Errorf("artifact client does not support TLS yet; leave tls disabled for local deployments")
	}
	options := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	options = append(options, dialOptions...)
	conn, err := grpc.NewClient(cfg.Address, options...)
	if err != nil {
		return nil, fmt.Errorf("dial artifact %s: %w", cfg.Address, err)
	}
	credentials := clientcredentials.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		TokenURL:     tokenURL,
	}
	tokens := credentials.TokenSource(ctx)
	return &Client{
		content: contentv1.NewContentServiceClient(conn),
		conn:    conn,
		cfg:     cfg,
		tenant:  tenant,
		org:     org,
		tokens:  tokens,
	}, nil
}

// Close 释放连接。
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// EvidenceType 返回发布证据使用的 artifact content type。放在客户端上，
// 调用方就不必自己记住这个字符串。
func (c *Client) EvidenceType() string {
	if c == nil {
		return ""
	}
	return c.cfg.TestReportType
}

// SealEvidence 走完 CreateContent → CreateResource → SealRevision → 回读校验。
//
// 为什么不用 CreateRevision：devmaster 一份证据就是一个内容的第一版，没有
// 「在上一版基础上改」的语义。需要新证据时载荷指纹会变，自然落成新内容。
func (c *Client) SealEvidence(ctx context.Context, evidence Evidence) (Sealed, error) {
	if strings.TrimSpace(evidence.Text) == "" {
		return Sealed{}, fmt.Errorf("evidence text is empty")
	}
	if strings.TrimSpace(evidence.ContentType) == "" || strings.TrimSpace(evidence.Title) == "" {
		return Sealed{}, fmt.Errorf("evidence content type and title are required")
	}
	if strings.TrimSpace(evidence.Role) == "" {
		return Sealed{}, fmt.Errorf("evidence role is required")
	}
	ctx, cancel := context.WithTimeout(ctx, timeoutOrDefault(c.cfg))
	defer cancel()
	ctx, err := c.authorize(ctx)
	if err != nil {
		return Sealed{}, err
	}

	payloadDigest := sha256.Sum256([]byte(evidence.Text))
	payloadSHA := hex.EncodeToString(payloadDigest[:])
	fingerprint, err := evidenceFingerprint(evidence, payloadSHA)
	if err != nil {
		return Sealed{}, err
	}

	metadata, err := structpb.NewStruct(evidence.Metadata)
	if err != nil {
		return Sealed{}, fmt.Errorf("encode evidence metadata: %w", err)
	}
	created, err := c.content.CreateContent(ctx, &contentv1.CreateContentRequest{
		OwnerOrgId:     c.org.String(),
		Namespace:      c.cfg.Namespace,
		ContentType:    evidence.ContentType,
		Title:          evidence.Title,
		Sensitivity:    contentv1.ContentSensitivity_CONTENT_SENSITIVITY_INTERNAL,
		Metadata:       metadata,
		IdempotencyKey: "devmaster-evidence-content-" + fingerprint,
	})
	if err != nil {
		return Sealed{}, fmt.Errorf("create evidence content: %w", err)
	}
	content, revision := created.GetContent(), created.GetRevision()
	if content.GetId() == "" || revision.GetId() == "" {
		return Sealed{}, fmt.Errorf("artifact returned an empty content or revision")
	}

	text := evidence.Text
	if _, err := c.content.CreateResource(ctx, &contentv1.CreateResourceRequest{
		RevisionId:     revision.GetId(),
		Role:           evidence.Role,
		Kind:           contentv1.ResourceKind_RESOURCE_KIND_INLINE_TEXT,
		IdempotencyKey: "devmaster-evidence-resource-" + fingerprint,
		Payload:        &contentv1.CreateResourceRequest_InlineText{InlineText: text},
	}); err != nil {
		return Sealed{}, fmt.Errorf("create evidence resource: %w", err)
	}

	manifestHash, err := c.seal(ctx, content.GetId(), revision.GetId(), revision.GetVersion())
	if err != nil {
		return Sealed{}, err
	}
	if err := c.verify(ctx, revision.GetId(), evidence.Role, payloadSHA); err != nil {
		return Sealed{}, err
	}

	return Sealed{
		ContentID:     content.GetId(),
		RevisionID:    revision.GetId(),
		RevisionNo:    revision.GetRevisionNo(),
		Ref:           content.GetId() + ":" + revision.GetId(),
		PayloadSHA256: payloadSHA,
		ManifestHash:  manifestHash,
		Replayed:      created.GetReplayed(),
	}, nil
}

// seal 密封修订。已密封视为成功：重试时修订可能早就是 SEALED，此时
// SealRevision 会以 FailedPrecondition 拒绝（state != WORKING），但对调用方
// 来说「它已经是密封状态」就是想要的结果。只有确认密封态确实成立才放行。
func (c *Client) seal(ctx context.Context, contentID, revisionID string, version uint64) (string, error) {
	response, err := c.content.SealRevision(ctx, &contentv1.SealRevisionRequest{
		RevisionId:      revisionID,
		ExpectedVersion: version,
	})
	if err == nil {
		return response.GetManifestHash(), nil
	}
	switch status.Code(err) {
	case codes.FailedPrecondition, codes.Aborted:
		current, getErr := c.content.GetContent(ctx, &contentv1.GetContentRequest{ContentId: contentID})
		if getErr != nil {
			return "", fmt.Errorf("seal evidence revision: %w", err)
		}
		revision := current.GetCurrentRevision()
		if revision != nil && revision.GetId() == revisionID &&
			revision.GetState() == contentv1.RevisionState_REVISION_STATE_SEALED {
			return "", nil
		}
		return "", fmt.Errorf("seal evidence revision: %w", err)
	}
	return "", fmt.Errorf("seal evidence revision: %w", err)
}

// verify 回读证据字节并比对摘要。存证的意义全在这一步：写进去的字节必须
// 能被原样读回来，否则 evidence_ref 只是一串 UUID。
func (c *Client) verify(ctx context.Context, revisionID, role, wantSHA string) error {
	listed, err := c.content.ListRevisionResources(ctx, &contentv1.ListRevisionResourcesRequest{RevisionId: revisionID})
	if err != nil {
		return fmt.Errorf("read back evidence: %w", err)
	}
	for _, resource := range listed.GetResources() {
		if resource.GetRole() != role {
			continue
		}
		digest := sha256.Sum256([]byte(resource.GetInlineText()))
		if got := hex.EncodeToString(digest[:]); got != wantSHA {
			return fmt.Errorf("evidence read-back mismatch: want %s got %s", wantSHA, got)
		}
		return nil
	}
	return fmt.Errorf("evidence resource with role %q is missing after seal", role)
}

func (c *Client) authorize(ctx context.Context) (context.Context, error) {
	token, err := c.tokens.Token()
	if err != nil {
		return nil, fmt.Errorf("obtain artifact access token: %w", err)
	}
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token.AccessToken), nil
}

// evidenceFingerprint 是幂等键的载荷指纹：同一份证据（类型、标题、元数据、
// 角色、字节）算出同一个键，重试即回放；任何一项变了就是另一份证据。
func evidenceFingerprint(evidence Evidence, payloadSHA string) (string, error) {
	canonical := struct {
		Format      string         `json:"format"`
		ContentType string         `json:"content_type"`
		Title       string         `json:"title"`
		Metadata    map[string]any `json:"metadata"`
		Role        string         `json:"role"`
		PayloadSHA  string         `json:"payload_sha256"`
	}{
		Format:      "devmaster-evidence/v1",
		ContentType: evidence.ContentType,
		Title:       evidence.Title,
		Metadata:    evidence.Metadata,
		Role:        evidence.Role,
		PayloadSHA:  payloadSHA,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("encode evidence fingerprint: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:20]), nil
}

// discoverTokenURL 从 issuer 的 OIDC 发现文档取 token_endpoint。
// 显式配了 token_url 就不走这条路（生产常把 issuer 与 token 端点分开部署）。
func discoverTokenURL(ctx context.Context, issuer string) (string, error) {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	if issuer == "" {
		return "", fmt.Errorf("artifact token_url or issuer is required")
	}
	requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return "", err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("discover artifact token endpoint: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read discovery document: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discovery document returned %d", response.StatusCode)
	}
	var document struct {
		TokenEndpoint string `json:"token_endpoint"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		return "", fmt.Errorf("decode discovery document: %w", err)
	}
	if document.TokenEndpoint == "" {
		return "", fmt.Errorf("discovery document has no token_endpoint")
	}
	return document.TokenEndpoint, nil
}

func timeoutOrDefault(cfg config.ArtifactConfig) time.Duration {
	if cfg.Timeout <= 0 {
		return 30 * time.Second
	}
	return cfg.Timeout
}

func validateConfig(cfg config.ArtifactConfig) error {
	if strings.TrimSpace(cfg.Address) == "" {
		return fmt.Errorf("artifact address is required")
	}
	if strings.TrimSpace(cfg.ClientID) == "" || strings.TrimSpace(cfg.ClientSecret) == "" {
		return fmt.Errorf("artifact client credentials are required")
	}
	if strings.TrimSpace(cfg.TokenURL) == "" && strings.TrimSpace(cfg.Issuer) == "" {
		return fmt.Errorf("artifact token_url or issuer is required")
	}
	if strings.TrimSpace(cfg.Namespace) == "" {
		return fmt.Errorf("artifact namespace is required")
	}
	if strings.TrimSpace(cfg.TestReportType) == "" || strings.TrimSpace(cfg.ReleaseNotesType) == "" {
		return fmt.Errorf("artifact content types are required")
	}
	return nil
}
