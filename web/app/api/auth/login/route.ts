import { redirect } from "next/navigation";
import { cookies } from "next/headers";

import {
  AUTH_MODE,
  DEV_DEFAULT_SUBJECT,
  OIDC_CLIENT_ID,
  OIDC_REDIRECT_URI,
  authorizationEndpoint,
  codeChallenge,
  randomToken,
} from "@/lib/oidc";
import { newSession, writeSession } from "@/lib/session";

export const dynamic = "force-dynamic";

const STATE_COOKIE = "devmaster_oidc_state";
const VERIFIER_COOKIE = "devmaster_oidc_verifier";

export async function GET(request: Request) {
  const url = new URL(request.url);

  // dev 模式：本地没有 IdP 时给一个固定身份，方便把界面跑起来。
  // 它只在显式设置 DEVMASTER_AUTH_MODE=dev 时生效，默认走 oidc。
  if (AUTH_MODE === "dev") {
    const subject = (url.searchParams.get("subject") ?? DEV_DEFAULT_SUBJECT).trim() || DEV_DEFAULT_SUBJECT;
    await writeSession(newSession(subject, subject, "", ["DEV_ADMIN"]));
    redirect("/");
  }

  const state = randomToken(24);
  const verifier = randomToken(48);
  const store = await cookies();
  store.set(STATE_COOKIE, state, { httpOnly: true, sameSite: "lax", path: "/", maxAge: 600 });
  store.set(VERIFIER_COOKIE, verifier, { httpOnly: true, sameSite: "lax", path: "/", maxAge: 600 });

  const authorize = new URL(authorizationEndpoint);
  authorize.searchParams.set("client_id", OIDC_CLIENT_ID);
  authorize.searchParams.set("response_type", "code");
  authorize.searchParams.set("scope", "openid profile email");
  authorize.searchParams.set("redirect_uri", OIDC_REDIRECT_URI);
  authorize.searchParams.set("state", state);
  authorize.searchParams.set("code_challenge", codeChallenge(verifier));
  authorize.searchParams.set("code_challenge_method", "S256");
  redirect(authorize.toString());
}
