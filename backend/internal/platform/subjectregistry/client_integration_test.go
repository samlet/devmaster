package subjectregistry_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	subjectv1 "github.com/xiaofeiwu/subject-registry/gen/go/subject/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/config"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/subjectregistry"
)

// 真连本机 subject-registry。验证两件事：
//  1. 未登记返回 found=false 且 err=nil（调用方据此降级，不重试）；
//  2. 已登记能解析出 anchor_id。
//
// 前置：subjectd 在 127.0.0.1:50095，SUBJECT_SERVICE_TOKEN 与服务端一致。
func TestResolveAgainstRealRegistry(t *testing.T) {
	if os.Getenv("DEVMASTER_RUN_INTEGRATION") != "1" {
		t.Skip("set DEVMASTER_RUN_INTEGRATION=1")
	}
	address := envOrDefault("SUBJECT_REGISTRY_ADDRESS", "127.0.0.1:50095")
	token := os.Getenv("SUBJECT_REGISTRY_TOKEN")
	if len(token) < 32 {
		t.Fatalf("SUBJECT_REGISTRY_TOKEN must be set (>=32 chars) for this test")
	}

	client, err := subjectregistry.New(config.SubjectRegistryConfig{
		Enabled: true, Address: address, ServiceToken: token, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()
	ctx := context.Background()

	// 未登记的 id：必须是 found=false + err=nil。
	if anchorID, found, err := client.ResolveSubject(ctx, "keycloak", "never-bound-"+uuid.NewString()); err != nil {
		t.Fatalf("missing alias must not error: %v", err)
	} else if found || anchorID != "" {
		t.Fatalf("missing alias resolved: anchor=%q found=%v", anchorID, found)
	}

	// 登记一条，再解析。
	externalID := "devmaster-it-" + uuid.NewString()
	anchorID := bindViaRegistry(t, address, token, externalID)
	resolved, found, err := client.ResolveSubject(ctx, "keycloak", externalID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !found || resolved != anchorID {
		t.Fatalf("resolved=%q found=%v want %q", resolved, found, anchorID)
	}
}

// bindViaRegistry 直接用 registry 的 Bind RPC 准备数据。
// devmaster 生产代码不做 Bind（登记是各域在用户首次出现时的事），测试里替它做。
func bindViaRegistry(t *testing.T, address, token, externalID string) string {
	t.Helper()
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial registry: %v", err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "x-subject-token", token, "x-actor", "devmaster-it")
	response, err := subjectv1.NewSubjectRegistryServiceClient(conn).Bind(ctx, &subjectv1.BindRequest{
		Kind:           subjectv1.SubjectKind_SUBJECT_KIND_PERSON,
		DisplayName:    "devmaster integration test",
		SourceSystem:   "keycloak",
		ExternalId:     externalID,
		Method:         subjectv1.AliasMethod_ALIAS_METHOD_MANUAL,
		IdempotencyKey: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	return response.GetAnchor().GetId()
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
