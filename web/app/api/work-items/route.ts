import { redirect } from "next/navigation";

import { bffConfigured, signedFetch } from "@/lib/bff";
import { readSession } from "@/lib/session";

// 新建工作项的 BFF 通道。
//
// reporter 由后端从 actor 取，客户端传不了；key 由后端分配。
// 这里只负责把表单字段整理成请求体并签名。

export const dynamic = "force-dynamic";

function backTo(returnTo: string, key: "error" | "created", value: string) {
  const base = returnTo.startsWith("/work-items") ? returnTo : "/work-items";
  const separator = base.includes("?") ? "&" : "?";
  redirect(`${base}${separator}${key}=${encodeURIComponent(value)}`);
}

export async function POST(request: Request) {
  const form = await request.formData();
  const returnTo = String(form.get("return_to") ?? "/work-items").trim();
  const productID = String(form.get("product_id") ?? "").trim();
  const type = String(form.get("type") ?? "").trim();
  const title = String(form.get("title") ?? "").trim();
  const description = String(form.get("description_md") ?? "").trim();
  const priority = String(form.get("priority") ?? "").trim();
  const assignee = String(form.get("assignee_id") ?? "").trim();

  const session = await readSession();
  if (!session) {
    redirect(`/login?error=${encodeURIComponent("新建工作项需要先登录")}`);
  }
  if (!bffConfigured()) {
    backTo(returnTo, "error", "未配置 DEVMASTER_BFF_SECRET，写操作已禁用");
  }
  if (!productID) {
    backTo(returnTo, "error", "请选择产品");
  }
  if (!title) {
    backTo(returnTo, "error", "标题不能为空");
  }

  const body = JSON.stringify({
    product_id: productID,
    type,
    title,
    description_md: description || undefined,
    priority: priority ? Number(priority) : undefined,
    assignee_id: assignee || undefined,
    actor: session!.sub,
  });

  let failure = "";
  let createdKey = "";
  try {
    const response = await signedFetch("/api/work-items", { method: "POST", body });
    if (!response.ok) {
      failure = (await response.text()).trim() || `后端返回 HTTP ${response.status}`;
    } else {
      const payload = (await response.json()) as { key?: string };
      createdKey = payload.key ?? "";
    }
  } catch (error) {
    failure = error instanceof Error ? error.message : "无法连接后端";
  }

  if (failure) {
    backTo(returnTo, "error", failure);
  }
  backTo(returnTo, "created", createdKey || "1");
}
