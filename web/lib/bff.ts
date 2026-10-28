// 服务端到后端的签名通道。
//
// 控制台的写操作与身份解析都走这里：签名密钥只在服务端，浏览器拿不到。
// 读面（GET /api/*）仍然直连，不签名——那些端点本来就没有鉴权。

import { createHmac } from "node:crypto";

const API_URL = process.env.DEVMASTER_API_URL ?? "http://127.0.0.1:8291";

function secret(): string {
  return process.env.DEVMASTER_BFF_SECRET ?? "";
}

export function bffConfigured(): boolean {
  return secret().length > 0;
}

function sign(timestamp: string, body: string): string {
  return createHmac("sha256", secret()).update(timestamp).update("\n").update(body).digest("hex");
}

/**
 * 调后端受签名保护的端点。
 * body 为空（GET）时签名覆盖空串——时间戳仍在签名里，所以 GET 也防重放。
 */
export async function signedFetch(
  path: string,
  init?: { method?: string; body?: string; query?: Record<string, string> },
): Promise<Response> {
  const body = init?.body ?? "";
  const timestamp = Math.floor(Date.now() / 1000).toString();
  // query 不参与签名：签名覆盖的是 body 与时间戳，GET/DELETE 用空 body。
  const query = init?.query ? `?${new URLSearchParams(init.query).toString()}` : "";
  return fetch(`${API_URL}${path}${query}`, {
    method: init?.method ?? "GET",
    headers: {
      "Content-Type": "application/json",
      "X-BFF-Timestamp": timestamp,
      "X-BFF-Signature": sign(timestamp, body),
    },
    body: body || undefined,
    cache: "no-store",
  });
}

/** 把 keycloak/<sub> 解析成主体锚点。未接入主体目录时返回 null。 */
export async function resolveAnchor(subject: string): Promise<string | null> {
  if (!bffConfigured() || !subject) {
    return null;
  }
  try {
    const response = await signedFetch(
      `/api/subjects/resolve?source=keycloak&external_id=${encodeURIComponent(subject)}`,
    );
    if (!response.ok) {
      return null;
    }
    const payload = (await response.json()) as { found?: boolean; anchor_id?: string };
    return payload.found && payload.anchor_id ? payload.anchor_id : null;
  } catch {
    return null;
  }
}
