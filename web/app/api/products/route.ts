import { redirect } from "next/navigation";

import { bffConfigured, signedFetch } from "@/lib/bff";
import { readSession } from "@/lib/session";

// 新建产品的 BFF 通道。产品是低频对象，只开放创建。

export const dynamic = "force-dynamic";

function backTo(ok: boolean, message: string) {
  const param = ok ? `created=${encodeURIComponent(message)}` : `error=${encodeURIComponent(message)}`;
  redirect(`/?${param}`);
}

export async function POST(request: Request) {
  const form = await request.formData();
  const key = String(form.get("key") ?? "").trim().toUpperCase();
  const name = String(form.get("name") ?? "").trim();
  const description = String(form.get("description") ?? "").trim();

  const session = await readSession();
  if (!session) {
    redirect(`/login?error=${encodeURIComponent("新建产品需要先登录")}`);
  }
  if (!bffConfigured()) {
    backTo(false, "未配置 DEVMASTER_BFF_SECRET，写操作已禁用");
  }
  if (!key || !name) {
    backTo(false, "产品 key 与名称都不能为空");
  }

  let failure = "";
  try {
    const response = await signedFetch("/api/products", {
      method: "POST",
      body: JSON.stringify({ key, name, description: description || undefined, actor: session!.sub }),
    });
    if (!response.ok) {
      failure = (await response.text()).trim() || `后端返回 HTTP ${response.status}`;
    }
  } catch (error) {
    failure = error instanceof Error ? error.message : "无法连接后端";
  }

  if (failure) {
    backTo(false, failure);
  }
  backTo(true, `${key} · ${name}`);
}
