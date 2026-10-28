import { createHmac } from "node:crypto";
import { redirect } from "next/navigation";

// BFF 写通道：浏览器只提交表单，签名与用户身份都在服务端完成。
//
// 为什么不在浏览器里调后端：写端点要求 HMAC 签名，密钥不能下发到浏览器。
// submitted_by 由服务端决定（本地固定为 demo 主体，生产应从会话取），
// 客户端传什么都覆盖不了它——否则任何人都能冒充别人提交发布。

const API_URL = process.env.DEVMASTER_API_URL ?? "http://127.0.0.1:8291";
const BFF_SECRET = process.env.DEVMASTER_BFF_SECRET ?? "";
const CONSOLE_SUBJECT = process.env.DEVMASTER_CONSOLE_SUBJECT ?? "local-dev";

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

  if (!BFF_SECRET) {
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
    submitted_by: CONSOLE_SUBJECT,
  });
  const timestamp = Math.floor(Date.now() / 1000).toString();
  const signature = createHmac("sha256", BFF_SECRET)
    .update(timestamp)
    .update("\n")
    .update(body)
    .digest("hex");

  let failure = "";
  try {
    const response = await fetch(`${API_URL}/api/releases`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-BFF-Timestamp": timestamp,
        "X-BFF-Signature": signature,
      },
      body,
      cache: "no-store",
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
  backTo(productID, "submitted", "1");
}
