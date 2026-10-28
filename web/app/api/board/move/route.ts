import { NextResponse } from "next/server";

import { bffConfigured, signedFetch } from "@/lib/bff";
import { readSession } from "@/lib/session";

// 看板拖拽的 BFF 通道。用 JSON 而不是表单：拖拽是客户端发起的，
// 没有 <form>，所以这里返回 JSON 而不是 303。

export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const session = await readSession();
  if (!session) {
    return NextResponse.json({ error: "移动工作项需要先登录" }, { status: 401 });
  }
  if (!bffConfigured()) {
    return NextResponse.json({ error: "未配置 DEVMASTER_BFF_SECRET，写操作已禁用" }, { status: 503 });
  }

  let payload: { work_item_key?: string; to?: string; expected_version?: number };
  try {
    payload = (await request.json()) as typeof payload;
  } catch {
    return NextResponse.json({ error: "请求体不是合法 JSON" }, { status: 400 });
  }
  const workItemKey = (payload.work_item_key ?? "").trim();
  const to = (payload.to ?? "").trim();
  const expectedVersion = Number(payload.expected_version ?? -1);
  if (!workItemKey || !to || expectedVersion < 0) {
    return NextResponse.json({ error: "缺少 work_item_key / to / expected_version" }, { status: 400 });
  }

  try {
    const response = await signedFetch("/api/board/move", {
      method: "POST",
      body: JSON.stringify({
        work_item_key: workItemKey,
        to,
        expected_version: expectedVersion,
        actor: session.sub,
        actor_roles: session.roles,
      }),
    });
    if (!response.ok) {
      const message = (await response.text()).trim() || `后端返回 HTTP ${response.status}`;
      return NextResponse.json({ error: message }, { status: response.status });
    }
    return NextResponse.json(await response.json());
  } catch (error) {
    return NextResponse.json(
      { error: error instanceof Error ? error.message : "无法连接后端" },
      { status: 502 },
    );
  }
}
