import { redirect } from "next/navigation";

import { AUTH_MODE, DEV_ACCOUNTS, DEV_DEFAULT_SUBJECT, OIDC_ISSUER, quickLoginAvailable } from "@/lib/oidc";
import { readSession } from "@/lib/session";

export const dynamic = "force-dynamic";

export default async function LoginPage({
  searchParams,
}: {
  searchParams: Promise<{ error?: string }>;
}) {
  const session = await readSession();
  if (session) {
    redirect("/");
  }
  const params = await searchParams;
  const quickAvailable = quickLoginAvailable();

  return (
    <main>
      <header className="page">
        <h1>登录</h1>
        <span className="hint">devmaster</span>
      </header>

      {params.error ? <p className="error">登录失败：{params.error}</p> : null}

      {quickAvailable ? (
        <div className="card login-card">
          <h2>快速登录</h2>
          <p className="hint">
            仅开发环境可用：跳过 Keycloak，直接以预设身份进入。账号的主体与 realm 里的用户一致，
            收件箱与审批记录的归属不受影响。
          </p>
          <div className="quick-login">
            {DEV_ACCOUNTS.map((account) => (
              <a key={account.sub} className="button-link" href={`/api/auth/login?quick=${account.sub}`}>
                {account.label}
              </a>
            ))}
          </div>
        </div>
      ) : null}

      <div className="card login-card">
        <h2>{quickAvailable ? "用 Keycloak 登录" : "登录"}</h2>
        {AUTH_MODE === "dev" ? (
          <>
            <p className="hint">
              当前是 <code>DEVMASTER_AUTH_MODE=dev</code>：不经过 Keycloak，直接以指定主体登录。
            </p>
            <form method="get" action="/api/auth/login" className="filters">
              <input name="subject" defaultValue={DEV_DEFAULT_SUBJECT} placeholder="主体标识" />
              <button type="submit">以该主体登录</button>
            </form>
          </>
        ) : (
          <>
            <p>登录后提交发布时会记录你的身份，收件箱也会包含发给你的通知。</p>
            <p className="hint">issuer：{OIDC_ISSUER}</p>
            <a className="button-link" href="/api/auth/login">
              使用 Keycloak 登录
            </a>
          </>
        )}
      </div>

      <footer className="page">
        <span>登录状态只保存在加密 cookie 里，不存 access token</span>
      </footer>
    </main>
  );
}
