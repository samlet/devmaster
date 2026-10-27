// Package auth 做两件事：验证 OIDC token，以及按方法矩阵授权。
//
// 矩阵是 fail-closed 的：未登记的方法一律拒绝。好处是默认安全，
// 代价是漏登记的 RPC 在编译期完全正常、第一次调用才 403 —— 所以有一个
// 覆盖率测试盯着它（见 auth_test.go）。
package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/config"
)

type Principal struct {
	Subject        string
	OrganizationID uuid.UUID
	Roles          []string
}

func (p Principal) HasRole(role string) bool {
	for _, item := range p.Roles {
		if item == role {
			return true
		}
	}
	return false
}

type principalKey struct{}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}

func PrincipalFrom(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalKey{}).(Principal)
	return principal, ok
}

// Rule 是一个方法的最小权限要求。
type Rule struct {
	RequireOrganization bool
	Roles               []string
}

// MethodRules 是授权矩阵。新增 RPC 必须在这里登记。
var MethodRules = map[string]Rule{
	"/dev.v1.SystemService/Health": {RequireOrganization: false},

	"/dev.v1.ProductService/GetProduct":    {RequireOrganization: true},
	"/dev.v1.ProductService/ListProducts":  {RequireOrganization: true},
	"/dev.v1.ProductService/CreateProduct": {RequireOrganization: true, Roles: []string{"DEV_ADMIN"}},
}

type Authenticator struct {
	enabled  bool
	verifier *oidc.IDTokenVerifier
	demo     Principal
}

func New(ctx context.Context, cfg config.AuthConfig) (*Authenticator, error) {
	if !cfg.Enabled {
		orgID, err := uuid.Parse(cfg.DemoOrganizationID)
		if err != nil {
			return nil, fmt.Errorf("auth.demo_organization_id: %w", err)
		}
		return &Authenticator{
			enabled: false,
			demo: Principal{
				Subject:        cfg.DemoSubject,
				OrganizationID: orgID,
				Roles:          cfg.DemoRoles,
			},
		}, nil
	}
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC provider %s: %w", cfg.Issuer, err)
	}
	return &Authenticator{
		enabled:  true,
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.Audience}),
	}, nil
}

// UnaryInterceptor 验证身份并注入 Principal，然后按矩阵授权。
func (a *Authenticator) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// 反射是开发工具；生产应由网关关闭，这里不额外开口子。
		if strings.HasPrefix(info.FullMethod, "/grpc.reflection.") {
			return handler(ctx, req)
		}
		rule, ok := MethodRules[info.FullMethod]
		if !ok {
			return nil, status.Errorf(codes.PermissionDenied,
				"%s is not registered in the authorization matrix", info.FullMethod)
		}

		principal, err := a.principalFrom(ctx)
		if err != nil {
			return nil, err
		}
		if rule.RequireOrganization && principal.OrganizationID == uuid.Nil {
			return nil, status.Error(codes.Unauthenticated, "token has no organization")
		}
		for _, role := range rule.Roles {
			if !principal.HasRole(role) {
				return nil, status.Errorf(codes.PermissionDenied, "role %s is required", role)
			}
		}
		return handler(WithPrincipal(ctx, principal), req)
	}
}

func (a *Authenticator) principalFrom(ctx context.Context) (Principal, error) {
	if !a.enabled {
		return a.demo, nil
	}
	raw, err := bearerToken(ctx)
	if err != nil {
		return Principal{}, err
	}
	token, err := a.verifier.Verify(ctx, raw)
	if err != nil {
		return Principal{}, status.Errorf(codes.Unauthenticated, "invalid token: %v", err)
	}
	var claims struct {
		OrganizationID  string   `json:"org_id"`
		OrganizationIDs []string `json:"org_ids"`
		Roles           []string `json:"roles"`
		RealmAccess     struct {
			Roles []string `json:"roles"`
		} `json:"realm_access"`
	}
	if err := token.Claims(&claims); err != nil {
		return Principal{}, status.Errorf(codes.Unauthenticated, "invalid claims: %v", err)
	}
	orgID, err := resolveOrganization(claims.OrganizationID, claims.OrganizationIDs)
	if err != nil {
		return Principal{}, status.Errorf(codes.Unauthenticated, "%v", err)
	}
	roles := append([]string{}, claims.Roles...)
	roles = append(roles, claims.RealmAccess.Roles...)
	return Principal{Subject: token.Subject, OrganizationID: orgID, Roles: roles}, nil
}

func bearerToken(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "missing metadata")
	}
	values := md.Get("authorization")
	if len(values) == 0 {
		return "", status.Error(codes.Unauthenticated, "missing authorization header")
	}
	const prefix = "bearer "
	value := values[0]
	if len(value) < len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
		return "", status.Error(codes.Unauthenticated, "authorization must be Bearer")
	}
	return strings.TrimSpace(value[len(prefix):]), nil
}

func resolveOrganization(single string, many []string) (uuid.UUID, error) {
	candidate := strings.TrimSpace(single)
	if candidate == "" && len(many) > 0 {
		candidate = strings.TrimSpace(many[0])
	}
	if candidate == "" {
		return uuid.Nil, fmt.Errorf("token has no org_id or org_ids claim")
	}
	orgID, err := uuid.Parse(candidate)
	if err != nil {
		return uuid.Nil, fmt.Errorf("token organization is not a UUID: %q", candidate)
	}
	return orgID, nil
}
