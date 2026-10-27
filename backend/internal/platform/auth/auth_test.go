package auth

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/grpc"

	devv1 "github.com/xiaofeiwu/devmaster/backend/gen/go/dev/v1"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/config"
)

var serviceDescs = []grpc.ServiceDesc{
	devv1.ProductService_ServiceDesc,
	devv1.WorkItemService_ServiceDesc,
	devv1.SystemService_ServiceDesc,
}

// TestMethodRulesCoverEveryRPC 守住 fail-closed 矩阵的完整性。
//
// 矩阵的设计是"未登记一律拒绝"。漏登记的 RPC 编译期正常，第一次调用才 403，
// 而且错误信息看起来像"账号没权限"，很容易查错方向。这个测试把它提前到 go test。
func TestMethodRulesCoverEveryRPC(t *testing.T) {
	registered := map[string]struct{}{}
	for _, desc := range serviceDescs {
		for _, method := range desc.Methods {
			full := fmt.Sprintf("/%s/%s", desc.ServiceName, method.MethodName)
			registered[full] = struct{}{}
			if _, ok := MethodRules[full]; !ok {
				t.Errorf("%s 没有在 MethodRules 里登记：调用时会返回 PermissionDenied", full)
			}
		}
	}
	for full := range MethodRules {
		if _, ok := registered[full]; !ok {
			t.Errorf("MethodRules 里的 %s 不对应任何已注册的 RPC（改名后忘了删？）", full)
		}
	}
}

func TestDemoPrincipalIsInjectedWhenAuthDisabled(t *testing.T) {
	authenticator, err := New(context.Background(), config.AuthConfig{
		Enabled:            false,
		DemoSubject:        "local-dev",
		DemoOrganizationID: "00000000-0000-0000-0000-000000000001",
		DemoRoles:          []string{"DEV_ADMIN"},
	})
	if err != nil {
		t.Fatalf("New 报错：%v", err)
	}
	var captured Principal
	handler := func(ctx context.Context, _ any) (any, error) {
		principal, ok := PrincipalFrom(ctx)
		if !ok {
			t.Fatal("handler 里没有 principal")
		}
		captured = principal
		return "ok", nil
	}
	if _, err := authenticator.UnaryInterceptor()(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/dev.v1.ProductService/CreateProduct"}, handler); err != nil {
		t.Fatalf("拦截器报错：%v", err)
	}
	if captured.Subject != "local-dev" || !captured.HasRole("DEV_ADMIN") {
		t.Fatalf("demo principal 不对：%+v", captured)
	}
}

func TestUnregisteredMethodIsRejected(t *testing.T) {
	authenticator, err := New(context.Background(), config.AuthConfig{
		Enabled:            false,
		DemoSubject:        "local-dev",
		DemoOrganizationID: "00000000-0000-0000-0000-000000000001",
	})
	if err != nil {
		t.Fatalf("New 报错：%v", err)
	}
	_, err = authenticator.UnaryInterceptor()(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/dev.v1.ProductService/DeleteEverything"}, func(context.Context, any) (any, error) {
			t.Fatal("未登记的方法不应进入 handler")
			return nil, nil
		})
	if err == nil {
		t.Fatal("未登记的方法应当被拒绝")
	}
}
