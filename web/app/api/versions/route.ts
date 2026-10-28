import { redirect } from "next/navigation";

import { bffConfigured, signedFetch } from "@/lib/bff";
import { readSession } from "@/lib/session";

// 新建版本的 BFF 通道。

export const dynamic = "force-dynamic";

function backTo(productID: string, ok: boolean, message: string) {
  const params = new URLSearchParams();
  if (productID) params.set("product_id", productID);
  params.set(ok ? "created" : "error", message);
  redirect(`/versions?${params.toString()}`);
}

export async function POST(request: Request) {
  const form = await request.formData();
  const productID = String(form.get("product_id") ?? "").trim();
  const name = String(form.get("name") ?? "").trim();
  const milestone = String(form.get("milestone_label") ?? "").trim();
  const plannedDate = String(form.get("planned_date") ?? "").trim();

  const session = await readSession();
  if (!session) {
    redirect(`/login?error=${encodeURIComponent("新建版本需要先登录")}`);
  }
  if (!bffConfigured()) {
    backTo(productID, false, "未配置 DEVMASTER_BFF_SECRET，写操作已禁用");
  }
  if (!productID) {
    backTo(productID, false, "请选择产品");
  }
  if (!name) {
    backTo(productID, false, "版本号不能为空");
  }

  let failure = "";
  try {
    const response = await signedFetch("/api/versions", {
      method: "POST",
      body: JSON.stringify({
        product_id: productID,
        name,
        milestone_label: milestone || undefined,
        planned_date: plannedDate || undefined,
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
    backTo(productID, false, failure);
  }
  backTo(productID, true, name);
}
