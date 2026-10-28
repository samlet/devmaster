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
