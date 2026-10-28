import { redirect } from "next/navigation";

import { bffConfigured, signedFetch } from "@/lib/bff";
import { readSession } from "@/lib/session";

// 冲刺动作的 BFF 通道：开始、结束、范围增删。
//
// 表单只能 GET/POST，所以用隐藏的 action 字段区分动作，
// 一个文件处理四种操作——它们共享同一套鉴权与错误回跳。

export const dynamic = "force-dynamic";

function backTo(productID: string, sprintID: string, ok: boolean, message: string) {
  const params = new URLSearchParams();
  if (productID) params.set("product_id", productID);
  if (sprintID) params.set("sprint_id", sprintID);
  if (ok) {
    params.set("done", message);
  } else {
    params.set("error", message);
  }
  redirect(`/sprints?${params.toString()}`);
}

export async function POST(request: Request, context: { params: Promise<{ id: string }> }) {
  const { id } = await context.params;
  const form = await request.formData();
  const productID = String(form.get("product_id") ?? "").trim();
  const action = String(form.get("action") ?? "").trim();
  const expectedVersion = Number(form.get("expected_version") ?? "0");
  const itemKeys = String(form.get("item_keys") ?? "").trim();

  const session = await readSession();
  if (!session) {
    redirect(`/login?error=${encodeURIComponent("冲刺操作需要先登录")}`);
  }
  if (!bffConfigured()) {
    backTo(productID, id, false, "未配置 DEVMASTER_BFF_SECRET，写操作已禁用");
  }

  const keys = itemKeys
    .split(/[\s,]+/)
    .map((value) => value.trim().toUpperCase())
    .filter(Boolean);

  let failure = "";
  let success = "";
  try {
    let response: Response;
    switch (action) {
      case "start":
      case "close":
        response = await signedFetch(`/api/sprints/${encodeURIComponent(id)}/${action}`, {
          method: "POST",
          body: JSON.stringify({ expected_version: expectedVersion, actor: session!.sub }),
        });
        success = action === "start" ? "已开始" : "已结束";
        break;
      case "add-items":
      case "remove-items":
        if (keys.length === 0) {
          backTo(productID, id, false, "请填写工作项 key");
        }
        response = await signedFetch(`/api/sprints/${encodeURIComponent(id)}/items`, {
          method: action === "add-items" ? "POST" : "DELETE",
          body:
            action === "add-items"
              ? JSON.stringify({ item_keys: keys, actor: session!.sub })
              : undefined,
          query:
            action === "remove-items"
              ? { item_keys: keys.join(","), actor: session!.sub }
              : undefined,
        });
        success = action === "add-items" ? "已加入范围" : "已移出范围";
        break;
      default:
        backTo(productID, id, false, `未知操作：${action}`);
        return;
    }
    if (!response.ok) {
      failure = (await response.text()).trim() || `后端返回 HTTP ${response.status}`;
    }
  } catch (error) {
    failure = error instanceof Error ? error.message : "无法连接后端";
  }

  if (failure) {
    backTo(productID, id, false, failure);
  }
  backTo(productID, id, true, success);
}
