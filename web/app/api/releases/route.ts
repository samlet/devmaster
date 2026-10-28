import { redirect } from "next/navigation";

import { bffConfigured, signedFetch } from "@/lib/bff";
import { readSession } from "@/lib/session";

// BFF 写通道：浏览器只提交表单，签名与用户身份都在服务端完成。
//
// submitted_by 取自加密 session，客户端传什么都覆盖不了——
// 否则任何人都能冒充别人提交发布。

export const dynamic = "force-dynamic";

function backTo(productID: string, key: "error" | "submitted", value: string) {
  const params = new URLSearchParams();
  if (productID) {
    params.set("product_id", productID);
  }
  params.set(key, value);
  redirect(`/versions?${params.toString()}`);
}

export async function POST(request: Request) {
  const form = await request.formData();
  const productID = String(form.get("product_id") ?? "").trim();
  const versionID = String(form.get("version_id") ?? "").trim();
  const commitFrom = String(form.get("commit_from") ?? "").trim();
  const commitTo = String(form.get("commit_to") ?? "").trim();

  const session = await readSession();
  if (!session) {
    redirect(`/login?error=${encodeURIComponent("提交发布需要先登录")}`);
  }
  if (!bffConfigured()) {
    backTo(productID, "error", "未配置 DEVMASTER_BFF_SECRET，写操作已禁用");
  }
  if (!versionID) {
    backTo(productID, "error", "请选择要发布的版本");
  }
  if (!commitTo) {
    backTo(productID, "error", "commit to 必填：打 tag 需要知道发布到哪个提交");
  }

  const body = JSON.stringify({
    version_id: versionID,
    commit_from: commitFrom,
    commit_to: commitTo,
    submitted_by: session!.sub,
  });

  let failure = "";
  try {
    const response = await signedFetch("/api/releases", { method: "POST", body });
    if (!response.ok) {
      failure = (await response.text()).trim() || `后端返回 HTTP ${response.status}`;
    }
  } catch (error) {
    failure = error instanceof Error ? error.message : "无法连接后端";
  }

  if (failure) {
    backTo(productID, "error", failure);
  }
  backTo(productID, "submitted", "1");
}
