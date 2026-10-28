// 收件箱：把 notify 的多个收件人键合并成一个列表。
//
// 为什么要多键：通知是按 recipient_id 投递的，而一个人可能同时是
// 「他自己」（user:<anchor>）、「审批角色成员」（role:RELEASE_APPROVER）
// 和「团队成员」（team:devmaster）。notify 只接受单个 recipient_id，
// 所以合并放在这一层——notify 不需要知道身份解析。
//
// 本地没有登录态，user: 键只能靠手工传；生产由 OIDC 会话提供。

const NOTIFY_URL = process.env.DEVMASTER_NOTIFY_URL ?? "http://127.0.0.1:18091";
const NOTIFY_TENANT = process.env.DEVMASTER_NOTIFY_TENANT ?? "devmaster";
const DEFAULT_RECIPIENTS = (process.env.DEVMASTER_INBOX_RECIPIENTS ?? "team:devmaster,role:RELEASE_APPROVER")
  .split(",")
  .map((value) => value.trim())
  .filter(Boolean);

type InboxItem = {
  id: string;
  recipient_id: string;
  title: string;
  body: string;
  read_at: string | null;
  created_at: string;
};

export const dynamic = "force-dynamic";

async function readInbox(recipient: string): Promise<{ items: InboxItem[]; error?: string }> {
  const url = `${NOTIFY_URL}/v1/inbox?tenant=${encodeURIComponent(NOTIFY_TENANT)}&recipient_id=${encodeURIComponent(recipient)}`;
  try {
    const response = await fetch(url, { cache: "no-store" });
    if (!response.ok) {
      return { items: [], error: `notify 返回 HTTP ${response.status}` };
    }
    const payload = (await response.json()) as { items?: InboxItem[] };
    return { items: payload.items ?? [] };
  } catch (error) {
    return { items: [], error: error instanceof Error ? error.message : "无法连接 notify" };
  }
}

export default async function InboxPage({
  searchParams,
}: {
  searchParams: Promise<{ recipient?: string }>;
}) {
  const params = await searchParams;
  const extra = (params.recipient ?? "").trim();
  const recipients = extra ? [extra, ...DEFAULT_RECIPIENTS] : DEFAULT_RECIPIENTS;

  const results = await Promise.all(recipients.map((recipient) => readInbox(recipient)));
  const errors = results.map((result) => result.error).filter((value): value is string => Boolean(value));
  // 同一事实可能投给多个键（比如既有角色箱又有团队箱），按 id 去重。
  const seen = new Set<string>();
  const items = results
    .flatMap((result) => result.items)
    .filter((item) => (seen.has(item.id) ? false : (seen.add(item.id), true)))
    .sort((left, right) => right.created_at.localeCompare(left.created_at));

  return (
    <main>
      <header className="page">
        <h1>收件箱</h1>
        <span className="hint">
          <a href="/">产品</a> · <a href="/work-items">工作项</a> · <a href="/board">看板</a> ·{" "}
          <a href="/versions">版本</a> · <a href="/reports">报表</a> · devmaster
        </span>
      </header>

      <form className="filters" method="get">
        <input
          name="recipient"
          defaultValue={extra}
          placeholder="附加收件人键，如 user:<anchor_id>"
          size={44}
        />
        <button type="submit">合并</button>
      </form>

      <p className="hint">
        合并自 {recipients.join(" · ")}
      </p>

      {errors.length > 0 ? <p className="error">读取收件箱失败：{errors.join("；")}</p> : null}

      <div className="card">
        {items.length === 0 ? (
          <div className="empty">没有通知。发布成功/失败、待审批催办都会出现在这里。</div>
        ) : (
          <table>
            <thead>
              <tr>
                <th>时间</th>
                <th>收件人</th>
                <th>标题</th>
                <th>内容</th>
                <th>状态</th>
              </tr>
            </thead>
            <tbody>
              {items.map((item) => (
                <tr key={item.id}>
                  <td className="muted">{new Date(item.created_at).toLocaleString("zh-CN")}</td>
                  <td className="key">{item.recipient_id}</td>
                  <td>{item.title}</td>
                  <td className="muted">{item.body}</td>
                  <td className="muted">{item.read_at ? "已读" : "未读"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <footer className="page">
        <span>通知来自 notify · 收件人键遵循 user: / role: / team: 约定</span>
        <span>{`共 ${items.length} 条`}</span>
      </footer>
    </main>
  );
}
