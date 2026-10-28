import { Nav } from "../nav";
import { readSession } from "@/lib/session";

export const dynamic = "force-dynamic";

const API_URL = process.env.DEVMASTER_API_URL ?? "http://127.0.0.1:8291";

type WorkLog = {
  id: string;
  work_item_key: string;
  author_id: string;
  minutes: number;
  work_date: string;
  note?: string;
  source: string;
  is_reversal: boolean;
};

type WorkLogList = { items: WorkLog[]; total: number; total_minutes: number };

async function loadWorkLogs(
  author: string,
  from: string,
  to: string,
): Promise<{ data?: WorkLogList; error?: string }> {
  const params = new URLSearchParams({ limit: "200" });
  if (author) params.set("author_id", author);
  if (from) params.set("from", from);
  if (to) params.set("to", to);
  try {
    const response = await fetch(`${API_URL}/api/worklogs?${params.toString()}`, { cache: "no-store" });
    if (!response.ok) {
      return { error: `后端返回 HTTP ${response.status}` };
    }
    return { data: (await response.json()) as WorkLogList };
  } catch (error) {
    return { error: error instanceof Error ? error.message : "无法连接后端" };
  }
}

function formatHours(minutes: number): string {
  const sign = minutes < 0 ? "-" : "";
  const abs = Math.abs(minutes);
  const hours = Math.floor(abs / 60);
  const rest = abs % 60;
  if (hours === 0) {
    return `${sign}${rest} 分钟`;
  }
  return rest === 0 ? `${sign}${hours} 小时` : `${sign}${hours} 小时 ${rest} 分`;
}

function today(): string {
  return new Date().toISOString().slice(0, 10);
}

export default async function WorkLogsPage({
  searchParams,
}: {
  searchParams: Promise<{
    author_id?: string;
    from?: string;
    to?: string;
    error?: string;
    logged?: string;
    reversed?: string;
  }>;
}) {
  const params = await searchParams;
  const session = await readSession();
  // 默认看自己的工时：多数人打开这页就是记今天干了什么。
  const author = params.author_id ?? session?.sub ?? "";
  const from = params.from ?? "";
  const to = params.to ?? "";
  const { data, error } = await loadWorkLogs(author, from, to);
  const items = data?.items ?? [];
  const totalMinutes = data?.total_minutes ?? 0;

  const returnTo = `/worklogs?${new URLSearchParams({
    ...(author ? { author_id: author } : {}),
    ...(from ? { from } : {}),
    ...(to ? { to } : {}),
  }).toString()}`;

  return (
    <main>
      <header className="page">
        <h1>工时</h1>
        <Nav />
      </header>

      {params.error ? <p className="error">操作失败：{params.error}</p> : null}
      {params.logged ? <p className="ok">已记录 {params.logged} 的工时。</p> : null}
      {params.reversed ? <p className="ok">已提交冲正。</p> : null}

      <form className="filters" method="get">
        <input name="author_id" defaultValue={author} placeholder="成员（sub）" size={20} />
        <input name="from" defaultValue={from} type="date" />
        <input name="to" defaultValue={to} type="date" />
        <button type="submit">筛选</button>
      </form>

      <div className="card summary-card">
        <div>
          <div className="summary-value">{formatHours(totalMinutes)}</div>
          <div className="summary-label">合计工时</div>
        </div>
        <div>
          <div className="summary-value">{items.length}</div>
          <div className="summary-label">条目数</div>
        </div>
        <div>
          <div className="summary-value">{author || "全部成员"}</div>
          <div className="summary-label">筛选成员</div>
        </div>
      </div>

      <details className="card create-work-item" open={items.length === 0}>
        <summary>记工时</summary>
        <form className="create-form" action="/api/worklogs" method="post">
          <input type="hidden" name="return_to" value={returnTo} />
          <label>
            工作项
            <input name="work_item_key" placeholder="如 HOSP-3" required size={12} />
          </label>
          <label>
            日期
            <input name="work_date" type="date" defaultValue={today()} required />
          </label>
          <label>
            分钟
            <input name="minutes" type="number" min="1" step="1" placeholder="90" required size={6} />
          </label>
          <label>
            备注
            <input name="note" placeholder="做了什么" size={20} />
          </label>
          <button type="submit">记录</button>
        </form>
      </details>

      {error ? <p className="error">读取工时失败：{error}</p> : null}

      <div className="card">
        {items.length === 0 ? (
          <div className="empty">
            还没有工时记录。用上面的表单记一条——工时是链 B（成本）的唯一入口。
          </div>
        ) : (
          <table>
            <thead>
              <tr>
                <th>日期</th>
                <th>工作项</th>
                <th>成员</th>
                <th>工时</th>
                <th>备注</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {items.map((entry) => (
                <tr key={entry.id}>
                  <td className="muted">{entry.work_date}</td>
                  <td className="key">{entry.work_item_key}</td>
                  <td className="muted">{entry.author_id.slice(0, 8)}</td>
                  <td className={entry.minutes < 0 ? "minutes-negative" : undefined}>
                    {formatHours(entry.minutes)}
                    {entry.is_reversal ? <span className="muted"> · 冲正</span> : null}
                  </td>
                  <td className="muted">{entry.note || "—"}</td>
                  <td>
                    {entry.minutes > 0 ? (
                      <details className="row-edit">
                        <summary>冲正</summary>
                        <form
                          className="edit-form"
                          action={`/api/worklogs/${entry.id}/reverse`}
                          method="post"
                        >
                          <input type="hidden" name="return_to" value={returnTo} />
                          <input
                            name="minutes"
                            type="number"
                            min="1"
                            step="1"
                            max={entry.minutes}
                            placeholder={`最多 ${entry.minutes}`}
                            size={8}
                          />
                          <input name="note" placeholder="理由（必填）" required size={16} />
                          <button type="submit">提交冲正</button>
                        </form>
                      </details>
                    ) : (
                      <span className="muted">—</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <footer className="page">
        <span>工时 append-only：只能新增与冲正，不能修改</span>
        <span>{data ? `共 ${data.total} 条` : ""}</span>
      </footer>
    </main>
  );
}
