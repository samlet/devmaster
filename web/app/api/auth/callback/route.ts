import { cookies } from "next/headers";
import { redirect } from "next/navigation";

import {
  OIDC_CLIENT_ID,
  OIDC_REDIRECT_URI,
  decodeIdToken,
  tokenEndpoint,
  type TokenResponse,
} from "@/lib/oidc";
import { newSession, writeSession } from "@/lib/session";

export const dynamic = "force-dynamic";

const STATE_COOKIE = "devmaster_oidc_state";
const VERIFIER_COOKIE = "devmaster_oidc_verifier";

function fail(reason: string): never {
  redirect(`/login?error=${encodeURIComponent(reason)}`);
}

export async function GET(request: Request) {
  const url = new URL(request.url);
  const code = url.searchParams.get("code");
  const state = url.searchParams.get("state");
  const error = url.searchParams.get("error");

  if (error) {
    fail(url.searchParams.get("error_description") ?? error);
  }
  if (!code) {
    fail("回调缺少 code");
  }

  const store = await cookies();
  const expectedState = store.get(STATE_COOKIE)?.value;
  const verifier = store.get(VERIFIER_COOKIE)?.value;
  // state 一次性使用：无论成败都清掉，避免重放。
  store.delete(STATE_COOKIE);
  store.delete(VERIFIER_COOKIE);

  if (!expectedState || !state || expectedState !== state) {
    fail("state 不匹配，请重新登录");
  }
  if (!verifier) {
    fail("缺少 PKCE verifier，请重新登录");
  }

  let tokens: TokenResponse;
  try {
    const response = await fetch(tokenEndpoint, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({
        grant_type: "authorization_code",
        code: code as string,
        redirect_uri: OIDC_REDIRECT_URI,
        client_id: OIDC_CLIENT_ID,
        code_verifier: verifier as string,
      }),
      cache: "no-store",
    });
    tokens = (await response.json()) as TokenResponse;
    if (!response.ok || !tokens.id_token) {
      fail(tokens.error_description ?? tokens.error ?? `token 端点返回 ${response.status}`);
    }
  } catch (cause) {
    fail(cause instanceof Error ? cause.message : "换取令牌失败");
  }

  let claims;
  try {
    claims = decodeIdToken(tokens!.id_token as string);
  } catch (cause) {
    fail(cause instanceof Error ? cause.message : "id_token 无效");
  }

  const sub = claims.sub as string;
  const name = claims.name ?? claims.preferred_username ?? sub;
  const roles = claims.realm_access?.roles ?? [];
  await writeSession(newSession(sub, name, claims.email ?? "", roles));
  redirect("/");
}
