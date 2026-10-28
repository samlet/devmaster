import { redirect } from "next/navigation";

import { bffConfigured, signedFetch } from "@/lib/bff";
import { readSession } from "@/lib/session";

// 版本挂工作项的 BFF 通道。
//
// 范围冻结后（已提交过发布）后端会拒绝，这里把 409 的原文回传给页面。

export const dynamic = "force-dynamic";

function backTo(productID: string, ok: boolean, message: string) {
  const params = new URLSearchParams();
  if (productID) params.set("product_id", productID);
  params.set(ok ? "assigned" : "error", message);
  redirect(`/versions?${params.toString()}`);
}

export async function POST(request: Request, context: { params: Promise<{ id: string }> }) {
  const { id } = await context.params;
  const form = await request.formData();
  const productID = String(form.get("product_id") ?? "").trim();
  const raw = String(form.get("item_keys") ?? "").trim();

  const keys = raw
    .split(/[\s,]+/)
    .map((value) => value.trim().toUpperCase())
    .filter(Boolean);

  const session = await readSession();
  if (!session) {
    redirect(`/login?error=${encodeURIComponent("挂工作项需要先登录")}`);
  }
  if (!bffConfigured()) {
    backTo(productID, false, "未配置 DEVMASTER_BFF_SECRET，写操作已禁用");
  }
  if (keys.length === 0) {
    backTo(productID, false, "请填写工作项 key");
  }

  let failure = "";
  let affected = 0;
  try {
    const response = await signedFetch(`/api/versions/${encodeURIComponent(id)}/items`, {
      method: "POST",
      body: JSON.stringify({ item_keys: keys, actor: session!.sub }),
    });
    if (!response.ok) {
      failure = (await response.text()).trim() || `后端返回 HTTP ${response.status}`;
    } else {
      const payload = (await response.json()) as { affected?: number };
      affected = payload.affected ?? 0;
    }
  } catch (error) {
    failure = error instanceof Error ? error.message : "无法连接后端";
  }

  if (failure) {
    backTo(productID, false, failure);
  }
  backTo(productID, true, `${affected} 个工作项`);
}
