export const dynamic = "force-dynamic";

type WorkItem = {
  id: string;
  key: string;
  product_id: string;
  type: string;
  status: string;
  resolution: string;
  title: string;
  priority: string;
  assignee_id: string;
  reporter_id: string;
  version: number;
  updated_at: string;
};

type WorkItemList = {
  items: WorkItem[];
  total: number;
};

const API_URL = process.env.DEVMASTER_API_URL ?? "http://127.0.0.1:8291";

const STATUS_LABELS: Record<string, string> = {
  BACKLOG: "待办池",
  TODO: "待办",
  OPEN: "待处理",
  IN_PROGRESS: "进行中",
  IN_REVIEW: "评审中",
  RESOLVED: "已修复",
  VERIFIED: "已验证",
  DONE: "完成",
  CLOSED: "关闭",
  REOPENED: "重新打开",
  WONTFIX: "不修复",
  CANCELLED: "已取消",
};

const TYPE_LABELS: Record<string, string> = {
  EPIC: "史诗",
  STORY: "需求",
  TASK: "任务",
  BUG: "缺陷",
  SUBTASK: "子任务",
};

async function loadWorkItems(status: string, query: string): Promise<{ data?: WorkItemList; error?: string }> {
  const params = new URLSearchParams();
  if (status) params.set("status", status);
  if (query) params.set("q", query);
  try {
    const response = await fetch(`${API_URL}/api/work-items?${params.toString()}`, { cache: "no-store" });
    if (!response.ok) {
      return { error: `后端返回 HTTP ${response.status}` };
    }
    return { data: (await response.json()) as WorkItemList };
  } catch (error) {
    return { error: error instanceof Error ? error.message : "无法连接后端" };
  }
}

export default async function WorkItemsPage({
  searchParams,
}: {
  searchParams: Promise<{ status?: string; q?: string }>;
}) {
  const params = await searchParams;
  const status = (params.status ?? "").toUpperCase();
  const query = params.q ?? "";
  const { data, error } = await loadWorkItems(status, query);
  const items = data?.items ?? [];

  return (
    <main>
      <header className="page">
        <h1>工作项</h1>
        <span className="hint">
          <a href="/">产品</a> · <a href="/board">看板</a> · <a href="/versions">版本</a> · <a href="/inbox">收件箱</a> · <a href="/reports">报表</a> · devmaster
        </span>
      </header>

      <form className="filters" method="get">
        <input name="q" defaultValue={query} placeholder="搜索标题或描述" />
        <select name="status" defaultValue={status}>
          <option value="">全部状态</option>
          {Object.entries(STATUS_LABELS).map(([value, label]) => (
            <option key={value} value={value}>
              {label}
            </option>
          ))}
        </select>
        <button type="submit">筛选</button>
      </form>

      {error ? (
        <p className="error">
          读取工作项失败：{error}
          <br />
          请确认后端已启动（<code>make run</code>）。
        </p>
      ) : (
        <div className="card">
          {items.length === 0 ? (
            <div className="empty">没有匹配的工作项。</div>
          ) : (
            <table>
              <thead>
                <tr>
                  <th>编号</th>
                  <th>类型</th>
                  <th>标题</th>
                  <th>状态</th>
                  <th>优先级</th>
                  <th>经办人</th>
                  <th>更新</th>
                </tr>
              </thead>
              <tbody>
                {items.map((item) => (
                  <tr key={item.id}>
                    <td className="key">{item.key}</td>
                    <td className="muted">{TYPE_LABELS[item.type] ?? item.type}</td>
                    <td>{item.title}</td>
                    <td>
                      <span className={`pill status-${item.status.toLowerCase()}`}>
                        {STATUS_LABELS[item.status] ?? item.status}
                      </span>
                    </td>
                    <td className="muted">{item.priority}</td>
                    <td className="muted">{item.assignee_id || "—"}</td>
                    <td className="muted">{new Date(item.updated_at).toLocaleString("zh-CN")}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}

      <footer className="page">
        <span>只读控制台 · M1 工作项</span>
        <span>{data ? `共 ${data.total} 条` : ""}</span>
      </footer>
    </main>
  );
}
