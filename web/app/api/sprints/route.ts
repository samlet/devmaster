import { redirect } from "next/navigation";

import { bffConfigured, signedFetch } from "@/lib/bff";
import { readSession } from "@/lib/session";

// 新建冲刺的 BFF 通道。

export const dynamic = "force-dynamic";

function backTo(productID: string, key: "error" | "created", value: string) {
  const params = new URLSearchParams();
  if (productID) params.set("product_id", productID);
  params.set(key, value);
  redirect(`/sprints?${params.toString()}`);
}

export async function POST(request: Request) {
  const form = await request.formData();
  const productID = String(form.get("product_id") ?? "").trim();
  const name = String(form.get("name") ?? "").trim();
  const goal = String(form.get("goal") ?? "").trim();
  const startAt = String(form.get("start_at") ?? "").trim();
  const endAt = String(form.get("end_at") ?? "").trim();
  const capacity = String(form.get("capacity_points") ?? "").trim();

  const session = await readSession();
  if (!session) {
    redirect(`/login?error=${encodeURIComponent("新建冲刺需要先登录")}`);
  }
  if (!bffConfigured()) {
    backTo(productID, "error", "未配置 DEVMASTER_BFF_SECRET，写操作已禁用");
  }
  if (!productID) {
    backTo(productID, "error", "请选择产品");
  }
  if (!name) {
    backTo(productID, "error", "冲刺名称不能为空");
  }

  let failure = "";
  try {
    const response = await signedFetch("/api/sprints", {
      method: "POST",
      body: JSON.stringify({
        product_id: productID,
        name,
        goal: goal || undefined,
        start_at: startAt || undefined,
        end_at: endAt || undefined,
        capacity_points: capacity ? Number(capacity) : undefined,
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
    backTo(productID, "error", failure);
  }
  backTo(productID, "created", name);
}
