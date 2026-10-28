import { redirect } from "next/navigation";

import { bffConfigured, signedFetch } from "@/lib/bff";
import { readSession } from "@/lib/session";

// 工作项状态流转的 BFF 通道。
//
// actor 与角色取自加密 session：客户端改不了自己是谁、有什么角色，
// 否则「只有 DEV_LEAD 能取消」这类守卫形同虚设。

export const dynamic = "force-dynamic";

// 只允许回跳到工作项页：return_to 来自表单，不做白名单就是开放重定向。
function backTo(returnTo: string, key: "error" | "transitioned", value: string) {
  const base = returnTo.startsWith("/work-items") ? returnTo : "/work-items";
  const separator = base.includes("?") ? "&" : "?";
  redirect(`${base}${separator}${key}=${encodeURIComponent(value)}`);
}

export async function POST(request: Request, context: { params: Promise<{ id: string }> }) {
  const { id } = await context.params;
  const form = await request.formData();
  const returnTo = String(form.get("return_to") ?? "/work-items").trim();
  const to = String(form.get("to") ?? "").trim();
  const resolution = String(form.get("resolution") ?? "").trim();
  const comment = String(form.get("comment") ?? "").trim();
  const expectedVersion = Number(form.get("expected_version") ?? "0");

  const session = await readSession();
  if (!session) {
    redirect(`/login?error=${encodeURIComponent("流转工作项需要先登录")}`);
  }
  if (!bffConfigured()) {
    backTo(returnTo, "error", "未配置 DEVMASTER_BFF_SECRET，写操作已禁用");
  }
  if (!to) {
    backTo(returnTo, "error", "请选择目标状态");
  }
  if (!Number.isInteger(expectedVersion) || expectedVersion < 0) {
    backTo(returnTo, "error", "缺少工作项版本，请刷新页面后重试");
  }

  const body = JSON.stringify({
    to,
    resolution: resolution || undefined,
    comment: comment || undefined,
    actor: session!.sub,
    actor_roles: session!.roles,
    expected_version: expectedVersion,
  });

  let failure = "";
  try {
    const response = await signedFetch(`/api/work-items/${encodeURIComponent(id)}/transition`, {
      method: "POST",
      body,
    });
    if (!response.ok) {
      failure = (await response.text()).trim() || `后端返回 HTTP ${response.status}`;
    }
  } catch (error) {
    failure = error instanceof Error ? error.message : "无法连接后端";
  }

  if (failure) {
    backTo(returnTo, "error", failure);
  }
  backTo(returnTo, "transitioned", to);
}
