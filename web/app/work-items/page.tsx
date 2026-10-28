import Link from "next/link";
import { Nav } from "../nav";

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
  /** 当前状态允许流转到的目标，由后端状态机给出。 */
  next_statuses: string[];
};

type Product = { id: string; key: string; name: string };

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

const PRIORITY_VALUES: Record<string, number> = { P0: 0, P1: 1, P2: 2, P3: 3 };

const RESOLUTION_LABELS: Record<string, string> = {
  FIXED: "已修复",
  WONTFIX: "不修复",
  DUPLICATE: "重复",
  CANNOT_REPRODUCE: "无法复现",
  BY_DESIGN: "设计如此",
  OBSOLETE: "已过时",
};

const TYPE_LABELS: Record<string, string> = {
  EPIC: "史诗",
  STORY: "需求",
  TASK: "任务",
  BUG: "缺陷",
  SUBTASK: "子任务",
};

async function loadProducts(): Promise<Product[]> {
  try {
    const response = await fetch(`${API_URL}/api/products?limit=50`, { cache: "no-store" });
    if (!response.ok) {
      return [];
    }
    const payload = (await response.json()) as { items?: Product[] };
    return payload.items ?? [];
  } catch {
    // 产品列表取不到不该挡住工作项列表：新建表单会退化成没有产品可选。
    return [];
  }
}

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
  searchParams: Promise<{
    status?: string;
    q?: string;
    error?: string;
    transitioned?: string;
    created?: string;
    updated?: string;
  }>;
}) {
  const params = await searchParams;
  const status = (params.status ?? "").toUpperCase();
  const query = params.q ?? "";
  // 流转后回到同一筛选视图，别把用户弹回未筛选的列表。
  const returnTo = `/work-items?${new URLSearchParams({ ...(status ? { status } : {}), ...(query ? { q: query } : {}) }).toString()}`;
  const { data, error } = await loadWorkItems(status, query);
  const items = data?.items ?? [];
  const products = await loadProducts();

  return (
    <main>
      <header className="page">
        <h1>工作项</h1>
        <Nav />
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
      ) : null}

      <details className="card create-work-item" open={items.length === 0}>
          <summary>新建工作项</summary>
          {products.length === 0 ? (
            <p className="muted">没有可用的产品，先在「产品」页确认数据。</p>
          ) : (
            <form className="create-form" action="/api/work-items" method="post">
              <input type="hidden" name="return_to" value={returnTo} />
              <label>
                产品
                <select name="product_id" defaultValue={products[0].id}>
                  {products.map((product) => (
                    <option key={product.id} value={product.id}>
                      {product.key} · {product.name}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                类型
                <select name="type" defaultValue="TASK">
                  <option value="STORY">需求</option>
                  <option value="TASK">任务</option>
                  <option value="BUG">缺陷</option>
                  <option value="SUBTASK">子任务</option>
                  <option value="EPIC">史诗</option>
                </select>
              </label>
              <label>
                标题
                <input name="title" placeholder="一句话说清要做什么" required size={28} />
              </label>
              <label>
                优先级
                <select name="priority" defaultValue="2">
                  <option value="0">P0</option>
                  <option value="1">P1</option>
                  <option value="2">P2</option>
                  <option value="3">P3</option>
                </select>
              </label>
              <label>
                经办人
                <input name="assignee_id" placeholder="可选" size={12} />
              </label>
              <button type="submit">创建</button>
            </form>
          )}
        </details>

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
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {items.map((item) => (
                  <tr key={item.id}>
                    <td className="key">
                      <Link href={`/work-items/${item.key}`}>{item.key}</Link>
                    </td>
                    <td className="muted">{TYPE_LABELS[item.type] ?? item.type}</td>
                    <td>
                      <Link href={`/work-items/${item.key}`}>{item.title}</Link>
                    </td>
                    <td>
                      <span className={`pill status-${item.status.toLowerCase()}`}>
                        {STATUS_LABELS[item.status] ?? item.status}
                      </span>
                    </td>
                    <td className="muted">{item.priority}</td>
                    <td className="muted">{item.assignee_id || "—"}</td>
                    <td className="muted">{new Date(item.updated_at).toLocaleString("zh-CN")}</td>
                    <td>
                      <details className="row-edit">
                        <summary>编辑</summary>
                        <form className="edit-form" action={`/api/work-items/${item.key}`} method="post">
                          <input type="hidden" name="return_to" value={returnTo} />
                          <input type="hidden" name="expected_version" value={item.version} />
                          <input name="title" defaultValue={item.title} size={24} required />
                          <select name="priority" defaultValue={String(PRIORITY_VALUES[item.priority] ?? 2)}>
                            <option value="0">P0</option>
                            <option value="1">P1</option>
                            <option value="2">P2</option>
                            <option value="3">P3</option>
                          </select>
                          <input name="assignee_id" defaultValue={item.assignee_id} placeholder="经办人" size={12} />
                          <input name="description_md" defaultValue="" placeholder="描述（留空表示清空）" size={20} />
                          <button type="submit">保存</button>
                        </form>
                      </details>
                      {item.next_statuses.length === 0 ? (
                        <span className="muted">—</span>
                      ) : (
                        <form
                          className="transition-form"
                          action={`/api/work-items/${item.key}/transition`}
                          method="post"
                        >
                          <input type="hidden" name="return_to" value={returnTo} />
                          <input type="hidden" name="expected_version" value={item.version} />
                          <select name="to" defaultValue={item.next_statuses[0]}>
                            {item.next_statuses.map((next) => (
                              <option key={next} value={next}>
                                {STATUS_LABELS[next] ?? next}
                              </option>
                            ))}
                          </select>
                          {item.type === "BUG" ? (
                            <select name="resolution" defaultValue="">
                              <option value="">resolution</option>
                              {Object.entries(RESOLUTION_LABELS).map(([value, label]) => (
                                <option key={value} value={value}>
                                  {label}
                                </option>
                              ))}
                            </select>
                          ) : null}
                          <input name="comment" placeholder="备注" size={8} />
                          <button type="submit">流转</button>
                        </form>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>

      <footer className="page">
        <span>M1 工作项 · 可新建、编辑与流转</span>
        <span>{data ? `共 ${data.total} 条` : ""}</span>
      </footer>
    </main>
  );
}
