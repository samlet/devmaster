import { redirect } from "next/navigation";

import { bffConfigured, signedFetch } from "@/lib/bff";
import { readSession } from "@/lib/session";

// 记工时的 BFF 通道。作者取自会话：工时是成本凭证，不能替别人记。

export const dynamic = "force-dynamic";

function backTo(returnTo: string, key: "error" | "logged", value: string) {
  const base = returnTo.startsWith("/worklogs") ? returnTo : "/worklogs";
  const separator = base.includes("?") ? "&" : "?";
  redirect(`${base}${separator}${key}=${encodeURIComponent(value)}`);
}

export async function POST(request: Request) {
  const form = await request.formData();
  const returnTo = String(form.get("return_to") ?? "/worklogs").trim();
  const workItemKey = String(form.get("work_item_key") ?? "").trim().toUpperCase();
  const minutes = Number(form.get("minutes") ?? "0");
  const workDate = String(form.get("work_date") ?? "").trim();
  const note = String(form.get("note") ?? "").trim();

  const session = await readSession();
  if (!session) {
    redirect(`/login?error=${encodeURIComponent("记工时需要先登录")}`);
  }
  if (!bffConfigured()) {
    backTo(returnTo, "error", "未配置 DEVMASTER_BFF_SECRET，写操作已禁用");
  }
  if (!workItemKey) {
    backTo(returnTo, "error", "工作项 key 不能为空");
  }
  if (!Number.isInteger(minutes) || minutes <= 0) {
    backTo(returnTo, "error", "分钟数必须是正整数");
  }
  if (!/^\d{4}-\d{2}-\d{2}$/.test(workDate)) {
    backTo(returnTo, "error", "日期格式应为 YYYY-MM-DD");
  }

  let failure = "";
  try {
    const response = await signedFetch("/api/worklogs", {
      method: "POST",
      body: JSON.stringify({
        work_item_key: workItemKey,
        minutes,
        work_date: workDate,
        note: note || undefined,
        actor: session!.sub,
      }),
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
  backTo(returnTo, "logged", workItemKey);
}
