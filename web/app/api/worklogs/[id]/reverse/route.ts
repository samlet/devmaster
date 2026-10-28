import { redirect } from "next/navigation";

import { bffConfigured, signedFetch } from "@/lib/bff";
import { readSession } from "@/lib/session";

// 工时冲正的 BFF 通道。
//
// 冲正不是「修改」：后端会追加一条负向记录，原记录不动。
// 这里强制要求理由——半年后没人记得为什么扣掉这 30 分钟。

export const dynamic = "force-dynamic";

function backTo(returnTo: string, key: "error" | "reversed", value: string) {
  const base = returnTo.startsWith("/worklogs") ? returnTo : "/worklogs";
  const separator = base.includes("?") ? "&" : "?";
  redirect(`${base}${separator}${key}=${encodeURIComponent(value)}`);
}

export async function POST(request: Request, context: { params: Promise<{ id: string }> }) {
  const { id } = await context.params;
  const form = await request.formData();
  const returnTo = String(form.get("return_to") ?? "/worklogs").trim();
  const note = String(form.get("note") ?? "").trim();
  const rawMinutes = String(form.get("minutes") ?? "").trim();

  const session = await readSession();
  if (!session) {
    redirect(`/login?error=${encodeURIComponent("冲正工时需要先登录")}`);
  }
  if (!bffConfigured()) {
    backTo(returnTo, "error", "未配置 DEVMASTER_BFF_SECRET，写操作已禁用");
  }
  if (!note) {
    backTo(returnTo, "error", "冲正必须填写理由");
  }

  let minutes: number | undefined;
  if (rawMinutes) {
    const parsed = Number(rawMinutes);
    if (!Number.isInteger(parsed) || parsed <= 0) {
      backTo(returnTo, "error", "冲正分钟数必须是正整数");
    }
    minutes = parsed;
  }

  let failure = "";
  try {
    const response = await signedFetch(`/api/worklogs/${encodeURIComponent(id)}/reverse`, {
      method: "POST",
      body: JSON.stringify({ minutes, note, actor: session!.sub }),
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
  backTo(returnTo, "reversed", "1");
}
