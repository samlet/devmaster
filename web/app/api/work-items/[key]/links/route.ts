import { redirect } from "next/navigation";

import { bffConfigured, signedFetch } from "@/lib/bff";
import { readSession } from "@/lib/session";

// 关联的 BFF 通道。表单只能 GET/POST，删除走 POST + 隐藏的 _method=DELETE。

export const dynamic = "force-dynamic";

function backTo(key: string, returnTo: string, ok: boolean, message: string) {
  const base = returnTo.startsWith("/work-items") ? returnTo : `/work-items/${encodeURIComponent(key)}`;
  const separator = base.includes("?") ? "&" : "?";
  const param = ok ? "linked=1" : `error=${encodeURIComponent(message)}`;
  redirect(`${base}${separator}${param}`);
}

export async function POST(request: Request, context: { params: Promise<{ key: string }> }) {
  const { key } = await context.params;
  const form = await request.formData();
  const returnTo = String(form.get("return_to") ?? "").trim();
  const targetKey = String(form.get("target_key") ?? "").trim();
  const type = String(form.get("type") ?? "").trim();
  const method = String(form.get("_method") ?? "POST").toUpperCase();

  const session = await readSession();
  if (!session) {
    redirect(`/login?error=${encodeURIComponent("关联操作需要先登录")}`);
  }
  if (!bffConfigured()) {
    backTo(key, returnTo, false, "未配置 DEVMASTER_BFF_SECRET，写操作已禁用");
  }
  if (!targetKey) {
    backTo(key, returnTo, false, "目标 key 不能为空");
  }

  let failure = "";
  try {
    const path =
      method === "DELETE"
        ? `/api/work-items/${encodeURIComponent(key)}/links?target_key=${encodeURIComponent(targetKey)}&type=${encodeURIComponent(type)}&actor=${encodeURIComponent(session!.sub)}`
        : `/api/work-items/${encodeURIComponent(key)}/links`;
    const response = await signedFetch(path, {
      method,
      body: method === "DELETE" ? undefined : JSON.stringify({ target_key: targetKey, type, actor: session!.sub }),
    });
    if (!response.ok) {
      failure = (await response.text()).trim() || `后端返回 HTTP ${response.status}`;
    }
  } catch (error) {
    failure = error instanceof Error ? error.message : "无法连接后端";
  }

  if (failure) {
    backTo(key, returnTo, false, failure);
  }
  backTo(key, returnTo, true, "");
}
