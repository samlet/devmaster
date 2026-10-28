package artifact_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	contentv1 "github.com/xiaofeiwu/artifact/gen/go/content/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/artifact"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/config"
)

const (
	testContentID  = "11111111-1111-7111-8111-111111111111"
	testRevisionID = "22222222-2222-7222-8222-222222222222"
)

// fakeContent 是一个可编程的 ContentService：记录调用顺序，并允许按用例
// 篡改回读内容或密封结果。
type fakeContent struct {
	contentv1.UnimplementedContentServiceServer

	mu         sync.Mutex
	calls      []string
	revisionID string
	storedText string
	sealErr    error
	// tamper 为 true 时回读返回被改过的字节，模拟证据被替换。
	tamper bool
	// alreadySealed 为 true 时 SealRevision 拒绝，但 GetContent 报 SEALED。
	alreadySealed bool
	contentKeys   []string
	resourceKeys  []string
}

func (f *fakeContent) record(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
}

func (f *fakeContent) CallNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeContent) CreateContent(ctx context.Context, request *contentv1.CreateContentRequest) (*contentv1.CreateContentResponse, error) {
	f.record("CreateContent")
	if request.GetContentType() != "devmaster.test-report" {
		return nil, status.Error(codes.FailedPrecondition, "unexpected content type")
	}
	if request.GetIdempotencyKey() == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency key is required")
	}
	f.mu.Lock()
	f.contentKeys = append(f.contentKeys, request.GetIdempotencyKey())
	f.mu.Unlock()
	metadata := request.GetMetadata().AsMap()
	if metadata["version_id"] == nil {
		return nil, status.Error(codes.InvalidArgument, "version_id metadata is required")
	}
	return &contentv1.CreateContentResponse{
		Content: &contentv1.Content{
			Id: testContentID, OwnerOrgId: request.GetOwnerOrgId(),
			Namespace: request.GetNamespace(), ContentType: request.GetContentType(),
			Title: request.GetTitle(), Version: 1,
		},
		Revision: &contentv1.ContentRevision{
			Id: testRevisionID, ContentId: testContentID, RevisionNo: 1,
			State: contentv1.RevisionState_REVISION_STATE_WORKING, Title: request.GetTitle(),
			Metadata: request.GetMetadata(), Version: 1,
		},
	}, nil
}

func (f *fakeContent) CreateResource(ctx context.Context, request *contentv1.CreateResourceRequest) (*contentv1.CreateResourceResponse, error) {
	f.record("CreateResource")
	if request.GetRevisionId() != testRevisionID {
		return nil, status.Error(codes.InvalidArgument, "unexpected revision")
	}
	if request.GetRole() != "evidence" || request.GetKind() != contentv1.ResourceKind_RESOURCE_KIND_INLINE_TEXT {
		return nil, status.Error(codes.InvalidArgument, "unexpected role or kind")
	}
	stored := request.GetInlineText()
	f.mu.Lock()
	f.storedText = stored
	f.resourceKeys = append(f.resourceKeys, request.GetIdempotencyKey())
	f.mu.Unlock()
	return &contentv1.CreateResourceResponse{Resource: &contentv1.ContentResource{
		Id: "33333333-3333-7333-8333-333333333333", RevisionId: testRevisionID,
		Role: "evidence", Kind: contentv1.ResourceKind_RESOURCE_KIND_INLINE_TEXT,
		State: contentv1.ResourceState_RESOURCE_STATE_VERIFIED, InlineText: &stored,
	}}, nil
}

func (f *fakeContent) SealRevision(ctx context.Context, request *contentv1.SealRevisionRequest) (*contentv1.SealRevisionResponse, error) {
	f.record("SealRevision")
	f.mu.Lock()
	alreadySealed := f.alreadySealed
	sealErr := f.sealErr
	f.mu.Unlock()
	if sealErr != nil {
		return nil, sealErr
	}
	if alreadySealed {
		return nil, status.Error(codes.FailedPrecondition, "revision is not working")
	}
	return &contentv1.SealRevisionResponse{
		Revision: &contentv1.ContentRevision{
			Id: testRevisionID, ContentId: testContentID, RevisionNo: 1,
			State: contentv1.RevisionState_REVISION_STATE_SEALED, Version: 2,
		},
		ManifestHash: "manifest-hash-value",
	}, nil
}

func (f *fakeContent) ListRevisionResources(ctx context.Context, request *contentv1.ListRevisionResourcesRequest) (*contentv1.ListRevisionResourcesResponse, error) {
	f.record("ListRevisionResources")
	f.mu.Lock()
	text, tamper := f.storedText, f.tamper
	f.mu.Unlock()
	if tamper {
		text = text + "\n<!-- injected -->"
	}
	return &contentv1.ListRevisionResourcesResponse{Resources: []*contentv1.ContentResource{{
		Id: "33333333-3333-7333-8333-333333333333", RevisionId: testRevisionID,
		Role: "evidence", Kind: contentv1.ResourceKind_RESOURCE_KIND_INLINE_TEXT,
		State: contentv1.ResourceState_RESOURCE_STATE_VERIFIED, InlineText: &text,
	}}}, nil
}

func (f *fakeContent) GetContent(ctx context.Context, request *contentv1.GetContentRequest) (*contentv1.GetContentResponse, error) {
	f.record("GetContent")
	f.mu.Lock()
	alreadySealed := f.alreadySealed
	f.mu.Unlock()
	state := contentv1.RevisionState_REVISION_STATE_WORKING
	if alreadySealed {
		state = contentv1.RevisionState_REVISION_STATE_SEALED
	}
	return &contentv1.GetContentResponse{
		Content: &contentv1.Content{Id: testContentID, ContentType: "devmaster.test-report", Version: 2},
		CurrentRevision: &contentv1.ContentRevision{
			Id: testRevisionID, ContentId: testContentID, RevisionNo: 1, State: state, Version: 2,
		},
	}, nil
}

// newTokenServer 提供一个最小的 client_credentials token 端点。
func newTokenServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Errorf("parse token form: %v", err)
		}
		if request.Form.Get("grant_type") != "client_credentials" {
			t.Errorf("grant_type=%q", request.Form.Get("grant_type"))
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]any{
			"access_token": "test-token", "token_type": "Bearer", "expires_in": 300,
		})
	}))
	t.Cleanup(server.Close)
	return server
}

func newClient(t *testing.T, fake *fakeContent, tokenURL string) *artifact.Client {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	contentv1.RegisterContentServiceServer(server, fake)
	go func() {
		if err := server.Serve(listener); err != nil && !strings.Contains(err.Error(), "closed") {
			t.Logf("fake content server stopped: %v", err)
		}
	}()
	t.Cleanup(server.Stop)
	client, err := artifact.New(context.Background(), config.ArtifactConfig{
		Enabled: true, Address: "passthrough:///bufconn", TokenURL: tokenURL,
		ClientID: "content-worker", ClientSecret: "secret",
		TenantID:         "00000000-0000-0000-0000-0000000000aa",
		OrganizationID:   "00000000-0000-0000-0000-000000000001",
		Namespace:        "devmaster",
		TestReportType:   "devmaster.test-report",
		ReleaseNotesType: "devmaster.release-notes",
	}, grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return listener.DialContext(ctx)
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestSealEvidenceRunsFullChain(t *testing.T) {
	fake := &fakeContent{}
	client := newClient(t, fake, newTokenServer(t).URL)
	text := "# 发布证据\n\n版本 v1.0.0\n"
	sealed, err := client.SealEvidence(context.Background(), artifact.Evidence{
		ContentType: client.EvidenceType(),
		Title:       "HOSP v1.0.0 发布证据",
		Metadata:    map[string]any{"version_id": "v-1", "item_count": 7},
		Role:        "evidence",
		Text:        text,
	})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	want := []string{"CreateContent", "CreateResource", "SealRevision", "ListRevisionResources"}
	got := fake.CallNames()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("call order=%v want %v", got, want)
	}
	if sealed.Ref != testContentID+":"+testRevisionID {
		t.Errorf("ref=%q", sealed.Ref)
	}
	digest := sha256.Sum256([]byte(text))
	if sealed.PayloadSHA256 != hex.EncodeToString(digest[:]) {
		t.Errorf("payload sha=%q", sealed.PayloadSHA256)
	}
	if sealed.ManifestHash != "manifest-hash-value" {
		t.Errorf("manifest hash=%q", sealed.ManifestHash)
	}
}

func TestSealEvidenceFailsOnReadBackMismatch(t *testing.T) {
	fake := &fakeContent{tamper: true}
	client := newClient(t, fake, newTokenServer(t).URL)
	_, err := client.SealEvidence(context.Background(), artifact.Evidence{
		ContentType: client.EvidenceType(), Title: "evidence",
		Metadata: map[string]any{"version_id": "v-1"}, Role: "evidence",
		Text: "original bytes",
	})
	if err == nil || !strings.Contains(err.Error(), "read-back mismatch") {
		t.Fatalf("expected read-back mismatch, got %v", err)
	}
}

// 重试场景：修订已经被上一次尝试密封过，SealRevision 会以 FailedPrecondition
// 拒绝，但确认密封态成立后必须当作成功——否则工作流重试永远过不去。
func TestSealEvidenceToleratesAlreadySealedRevision(t *testing.T) {
	fake := &fakeContent{alreadySealed: true}
	client := newClient(t, fake, newTokenServer(t).URL)
	sealed, err := client.SealEvidence(context.Background(), artifact.Evidence{
		ContentType: client.EvidenceType(), Title: "evidence",
		Metadata: map[string]any{"version_id": "v-1"}, Role: "evidence",
		Text: "already sealed bytes",
	})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if sealed.Ref != testContentID+":"+testRevisionID {
		t.Errorf("ref=%q", sealed.Ref)
	}
	names := strings.Join(fake.CallNames(), ",")
	if !strings.Contains(names, "GetContent") {
		t.Errorf("expected GetContent confirmation, calls=%s", names)
	}
}

func TestSealEvidenceRejectsEmptyPayload(t *testing.T) {
	client := newClient(t, &fakeContent{}, newTokenServer(t).URL)
	if _, err := client.SealEvidence(context.Background(), artifact.Evidence{
		ContentType: client.EvidenceType(), Title: "evidence", Role: "evidence", Text: "   ",
	}); err == nil {
		t.Fatal("expected empty payload rejection")
	}
}

// 指纹决定幂等键：同一份证据重复存证必须复用同一个键（artifact 侧回放同一
// 修订），载荷或元数据变了必须是另一个键（否则新证据会被当成重放丢掉）。
func TestSealEvidenceDerivesStableIdempotencyKeys(t *testing.T) {
	fake := &fakeContent{}
	client := newClient(t, fake, newTokenServer(t).URL)
	evidence := artifact.Evidence{
		ContentType: client.EvidenceType(), Title: "same title", Role: "evidence",
		Metadata: map[string]any{"version_id": "v-1", "item_count": 7},
		Text:     "bytes",
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := client.SealEvidence(context.Background(), evidence); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
	}
	fake.mu.Lock()
	contentKeys := append([]string(nil), fake.contentKeys...)
	resourceKeys := append([]string(nil), fake.resourceKeys...)
	fake.mu.Unlock()
	if len(contentKeys) != 2 || contentKeys[0] != contentKeys[1] {
		t.Fatalf("content idempotency keys are not stable: %v", contentKeys)
	}
	if len(resourceKeys) != 2 || resourceKeys[0] != resourceKeys[1] {
		t.Fatalf("resource idempotency keys are not stable: %v", resourceKeys)
	}

	changed := evidence
	changed.Metadata = map[string]any{"version_id": "v-1", "item_count": 8}
	if _, err := client.SealEvidence(context.Background(), changed); err != nil {
		t.Fatalf("changed metadata: %v", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.contentKeys[2] == contentKeys[0] {
		t.Fatal("metadata change reused the same idempotency key")
	}
}
