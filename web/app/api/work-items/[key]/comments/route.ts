import { redirect } from "next/navigation";

import { bffConfigured, signedFetch } from "@/lib/bff";
import { readSession } from "@/lib/session";

// 评论的 BFF 通道。作者取自会话，客户端改不了。

export const dynamic = "force-dynamic";

function backTo(key: string, returnTo: string, ok: boolean, message: string) {
  const base = returnTo.startsWith("/work-items") ? returnTo : `/work-items/${encodeURIComponent(key)}`;
  const separator = base.includes("?") ? "&" : "?";
  const param = ok ? "commented=1" : `error=${encodeURIComponent(message)}`;
  redirect(`${base}${separator}${param}`);
}

export async function POST(request: Request, context: { params: Promise<{ key: string }> }) {
  const { key } = await context.params;
  const form = await request.formData();
  const returnTo = String(form.get("return_to") ?? "").trim();
  const bodyMD = String(form.get("body_md") ?? "").trim();

  const session = await readSession();
  if (!session) {
    redirect(`/login?error=${encodeURIComponent("评论需要先登录")}`);
  }
  if (!bffConfigured()) {
    backTo(key, returnTo, false, "未配置 DEVMASTER_BFF_SECRET，写操作已禁用");
  }
  if (!bodyMD) {
    backTo(key, returnTo, false, "评论内容不能为空");
  }

  let failure = "";
  try {
    const response = await signedFetch(`/api/work-items/${encodeURIComponent(key)}/comments`, {
      method: "POST",
      body: JSON.stringify({ body_md: bodyMD, actor: session!.sub }),
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
