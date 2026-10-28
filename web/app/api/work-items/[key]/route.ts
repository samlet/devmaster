import { redirect } from "next/navigation";

import { bffConfigured, signedFetch } from "@/lib/bff";
import { readSession } from "@/lib/session";

// 编辑工作项的 BFF 通道。
//
// 浏览器表单只能 GET/POST，所以这里用 POST 收表单，内部发 PATCH。
// assignee_id 始终显式带上（哪怕为空串）：空串表示取消指派，
// 缺省表示「不动经办人」——两种意图不能混。

export const dynamic = "force-dynamic";

function backTo(returnTo: string, key: "error" | "updated", value: string) {
  const base = returnTo.startsWith("/work-items") ? returnTo : "/work-items";
  const separator = base.includes("?") ? "&" : "?";
  redirect(`${base}${separator}${key}=${encodeURIComponent(value)}`);
}

export async function POST(request: Request, context: { params: Promise<{ key: string }> }) {
  const { key } = await context.params;
  const form = await request.formData();
  const returnTo = String(form.get("return_to") ?? "/work-items").trim();
  const expectedVersion = Number(form.get("expected_version") ?? "0");
  const title = String(form.get("title") ?? "").trim();
  const description = String(form.get("description_md") ?? "");
  const priority = String(form.get("priority") ?? "").trim();
  const assignee = String(form.get("assignee_id") ?? "");

  const session = await readSession();
  if (!session) {
    redirect(`/login?error=${encodeURIComponent("编辑工作项需要先登录")}`);
  }
  if (!bffConfigured()) {
    backTo(returnTo, "error", "未配置 DEVMASTER_BFF_SECRET，写操作已禁用");
  }
  if (!Number.isInteger(expectedVersion) || expectedVersion < 0) {
    backTo(returnTo, "error", "缺少工作项版本，请刷新页面后重试");
  }

  const body = JSON.stringify({
    expected_version: expectedVersion,
    title: title || undefined,
    description_md: description,
    priority: priority ? Number(priority) : undefined,
    assignee_id: assignee.trim(),
    actor: session!.sub,
  });

  let failure = "";
  try {
    const response = await signedFetch(`/api/work-items/${encodeURIComponent(key)}`, {
      method: "PATCH",
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
  backTo(returnTo, "updated", "1");
}
