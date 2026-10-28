import Link from "next/link";

import { readSession } from "@/lib/session";

/**
 * 顶部导航。抽出来是因为它出现在每个页面，而登录状态是全局的——
 * 复制六份就意味着加一次「登录/登出」要改六处。
 */
export async function Nav() {
  const session = await readSession();
  return (
    <span className="hint">
      <Link href="/">产品</Link> · <Link href="/work-items">工作项</Link> · <Link href="/board">看板</Link> ·{" "}
      <Link href="/versions">版本</Link> · <Link href="/inbox">收件箱</Link> · <Link href="/reports">报表</Link>
      {" · "}
      {session ? (
        <>
          <span title={session.sub}>{session.name}</span> · <a href="/api/auth/logout">登出</a>
        </>
      ) : (
        <Link href="/login">登录</Link>
      )}
    </span>
  );
}
