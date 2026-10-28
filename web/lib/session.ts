import { createCipheriv, createDecipheriv, randomBytes, scryptSync } from "node:crypto";
import { cookies } from "next/headers";

// 会话只存「你是谁」，不存 access token：
// 控制台的读走服务端直连、写走 BFF 签名，都不需要把用户令牌带在浏览器里。
// 少一个令牌就少一处泄露面。

const COOKIE_NAME = "devmaster_session";
const COOKIE_MAX_AGE = 8 * 60 * 60;

export type Session = {
  sub: string;
  name: string;
  email: string;
  roles: string[];
  /** 过期时间（epoch 秒）。cookie 过期与它取较早者。 */
  exp: number;
};

function secret(): string {
  const value = process.env.DEVMASTER_SESSION_SECRET ?? "";
  if (value.length < 32) {
    throw new Error("DEVMASTER_SESSION_SECRET must be at least 32 characters");
  }
  return value;
}

// 用 scrypt 派生固定长度密钥：secret 是人写的，长度不定，直接当 AES key 不安全。
function key(): Buffer {
  return scryptSync(secret(), "devmaster-session-v1", 32);
}

/** AES-256-GCM 封装：iv(12) + tag(16) + ciphertext。 */
export function seal(session: Session): string {
  const iv = randomBytes(12);
  const cipher = createCipheriv("aes-256-gcm", key(), iv);
  const ciphertext = Buffer.concat([cipher.update(JSON.stringify(session), "utf8"), cipher.final()]);
  return Buffer.concat([iv, cipher.getAuthTag(), ciphertext]).toString("base64url");
}

export function open(token: string): Session | null {
  try {
    const raw = Buffer.from(token, "base64url");
    if (raw.length <= 28) {
      return null;
    }
    const decipher = createDecipheriv("aes-256-gcm", key(), raw.subarray(0, 12));
    decipher.setAuthTag(raw.subarray(12, 28));
    const plaintext = Buffer.concat([decipher.update(raw.subarray(28)), decipher.final()]).toString("utf8");
    const session = JSON.parse(plaintext) as Session;
    if (!session.sub || session.exp * 1000 < Date.now()) {
      return null;
    }
    return session;
  } catch {
    // 密钥轮换、篡改、过期都会走到这里：一律当作未登录，不抛给页面。
    return null;
  }
}

export async function readSession(): Promise<Session | null> {
  const store = await cookies();
  const raw = store.get(COOKIE_NAME)?.value;
  return raw ? open(raw) : null;
}

export async function writeSession(session: Session): Promise<void> {
  const store = await cookies();
  store.set(COOKIE_NAME, seal(session), {
    httpOnly: true,
    sameSite: "lax",
    path: "/",
    maxAge: COOKIE_MAX_AGE,
    // 本地是 http；生产（https）应打开 secure。用环境判断而不是写死，
    // 免得本地调试时 cookie 直接被浏览器丢掉。
    secure: process.env.NODE_ENV === "production",
  });
}

export async function clearSession(): Promise<void> {
  const store = await cookies();
  store.delete(COOKIE_NAME);
}

export function newSession(sub: string, name: string, email: string, roles: string[]): Session {
  return { sub, name, email, roles, exp: Math.floor(Date.now() / 1000) + COOKIE_MAX_AGE };
}
