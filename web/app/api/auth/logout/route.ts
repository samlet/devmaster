import { redirect } from "next/navigation";

import { clearSession } from "@/lib/session";

export const dynamic = "force-dynamic";

// 只清本地 session，不跳 Keycloak 的 end_session：
// 控制台没有长期会话的需求，本地登出足够；接 SSO 单点登出要另外评估
// 其他系统的会话联动，不该顺手做。
export async function GET() {
  await clearSession();
  redirect("/login");
}

export async function POST() {
  await clearSession();
  redirect("/login");
}
