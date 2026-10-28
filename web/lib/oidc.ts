// OIDC 客户端配置与 PKCE 工具。
//
// 不引 next-auth：控制台只需要「登录一次、拿到 sub、写个加密 cookie」，
// 引一整套 session/provider 适配会让排障面变大。这里的流程是标准
// Authorization Code + PKCE，四十行能看清每一步。

import { createHash, randomBytes } from "node:crypto";

export const OIDC_ISSUER = (process.env.DEVMASTER_OIDC_ISSUER ?? "http://127.0.0.1:18281/realms/devmaster").replace(
  /\/$/,
  "",
);
export const OIDC_CLIENT_ID = process.env.DEVMASTER_OIDC_CLIENT_ID ?? "devmaster-web";
export const OIDC_REDIRECT_URI =
  process.env.DEVMASTER_OIDC_REDIRECT_URI ?? "http://127.0.0.1:3031/api/auth/callback";

/** dev 模式跳过 Keycloak 直接给一个身份，仅用于本地没有 IdP 时。 */
export const AUTH_MODE = (process.env.DEVMASTER_AUTH_MODE ?? "oidc").toLowerCase();
export const DEV_DEFAULT_SUBJECT = process.env.DEVMASTER_CONSOLE_SUBJECT ?? "local-dev";

/**
 * 快速登录的预设账号。
 *
 * sub 与 deploy/keycloak/realm-devmaster.json 里的用户 id 保持一致——
 * 这样快速登录与真实 Keycloak 登录拿到的是同一个主体，subject-registry 里
 * 已有的绑定（收件箱的 user: 键）不用重新绑。
 */
export const DEV_ACCOUNTS = [
  {
    sub: "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa",
    name: "Alice Chen",
    label: "Alice · DEV_LEAD",
    roles: ["DEV_MEMBER", "DEV_LEAD"],
  },
  {
    sub: "bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb",
    name: "Bob Liu",
    label: "Bob · RELEASE_APPROVER",
    roles: ["DEV_MEMBER", "RELEASE_APPROVER"],
  },
] as const;

/**
 * 快速登录是否可用。
 *
 * 它跳过 Keycloak 直接写会话，等于「免认证」——生产环境必须永远关闭。
 * 用 NODE_ENV 判断而不是自定义开关：`next build` 会把它编译成常量，
 * 生产包里这条分支直接消失，比运行时判断更难被误开。
 */
export function quickLoginAvailable(): boolean {
  return process.env.NODE_ENV !== "production";
}

export const authorizationEndpoint = `${OIDC_ISSUER}/protocol/openid-connect/auth`;
export const tokenEndpoint = `${OIDC_ISSUER}/protocol/openid-connect/token`;
export const endSessionEndpoint = `${OIDC_ISSUER}/protocol/openid-connect/logout`;

export function randomToken(bytes = 32): string {
  return randomBytes(bytes).toString("base64url");
}

export function codeChallenge(verifier: string): string {
  return createHash("sha256").update(verifier).digest("base64url");
}

export type TokenResponse = {
  access_token?: string;
  id_token?: string;
  error?: string;
  error_description?: string;
};

export type IdTokenClaims = {
  sub?: string;
  name?: string;
  preferred_username?: string;
  email?: string;
  exp?: number;
  iss?: string;
  aud?: string | string[];
  // roles scope 带出来的 realm 角色。状态机守卫需要它（取消要 DEV_LEAD、
  // 代验 bug 也要 DEV_LEAD），所以必须从 id_token 里取，不能默认空。
  realm_access?: { roles?: string[] };
};

/**
 * 解析 id_token 的声明。
 *
 * 不做本地验签：这个 token 是服务端从 token 端点直接换回来的（HTTPS/loopback
 * 直连 + code 交换 + PKCE），不是从浏览器带回来的。要验签就得再引一个 JWKS 库，
 * 而它防的攻击面在这里并不存在。仍然校验 iss/aud/exp——那是防配置错，不是防伪造。
 */
export function decodeIdToken(idToken: string): IdTokenClaims {
  const parts = idToken.split(".");
  if (parts.length !== 3) {
    throw new Error("id_token 格式不正确");
  }
  const claims = JSON.parse(Buffer.from(parts[1], "base64url").toString("utf8")) as IdTokenClaims;
  if (claims.iss && claims.iss.replace(/\/$/, "") !== OIDC_ISSUER) {
    throw new Error(`id_token 的 issuer 不匹配：${claims.iss}`);
  }
  const audience = Array.isArray(claims.aud) ? claims.aud : [claims.aud];
  if (!audience.includes(OIDC_CLIENT_ID)) {
    throw new Error("id_token 的 audience 不匹配");
  }
  if (claims.exp && claims.exp * 1000 < Date.now()) {
    throw new Error("id_token 已过期");
  }
  if (!claims.sub) {
    throw new Error("id_token 缺少 sub");
  }
  return claims;
}
