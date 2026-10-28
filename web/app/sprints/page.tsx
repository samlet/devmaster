import Link from "next/link";

import { Nav } from "../nav";

export const dynamic = "force-dynamic";

const API_URL = process.env.DEVMASTER_API_URL ?? "http://127.0.0.1:8291";

type Product = { id: string; key: string; name: string };

type SprintStats = {
  committed_points: number;
  added_points: number;
  removed_points: number;
  completed_points: number;
  scope_points: number;
  remaining_points: number;
  creep_rate: number;
};

type Sprint = {
  id: string;
  name: string;
  goal: string;
  state: string;
  start_at: string;
  end_at: string;
  committed_points: number;
  version: number;
  stats?: SprintStats;
};

type BurndownPoint = {
  date: string;
  scope_points: number;
  completed_points: number;
  remaining_points: number;
};

const STATE_LABELS: Record<string, string> = {
  PLANNED: "计划中",
  ACTIVE: "进行中",
  CLOSED: "已结束",
};

async function getJSON<T>(path: string): Promise<{ data?: T; error?: string }> {
  try {
    const response = await fetch(`${API_URL}${path}`, { cache: "no-store" });
    if (!response.ok) {
      return { error: `后端返回 HTTP ${response.status}` };
    }
    return { data: (await response.json()) as T };
  } catch (error) {
    return { error: error instanceof Error ? error.message : "无法连接后端" };
  }
}

/**
 * 燃尽图用 SVG 手绘，不引图表库。
 * 两条线：范围（scope，虚线）与实际剩余（remaining，实线）。
 * 两者分叉的地方就是范围蔓延。
 */
function BurndownChart({ points }: { points: BurndownPoint[] }) {
  if (points.length === 0) {
    return (
      <div className="empty">
        还没有燃尽快照。跑 <code>make snapshot</code> 写入当天的快照后再看。
      </div>
    );
  }
  const width = 620;
  const height = 180;
  const padding = 28;
  const max = Math.max(...points.map((point) => point.scope_points), 1);
  const stepX = points.length > 1 ? (width - padding * 2) / (points.length - 1) : 0;
  const toY = (value: number) => height - padding - (value / max) * (height - padding * 2);
  const line = (pick: (point: BurndownPoint) => number) =>
    points.map((point, index) => `${index === 0 ? "M" : "L"}${padding + index * stepX},${toY(pick(point))}`).join(" ");

  return (
    <svg viewBox={`0 0 ${width} ${height}`} className="burndown" role="img">
      <title>冲刺燃尽图</title>
      <line x1={padding} y1={height - padding} x2={width - padding} y2={height - padding} stroke="var(--border)" strokeWidth="1" />
      <line x1={padding} y1={padding} x2={padding} y2={height - padding} stroke="var(--border)" strokeWidth="1" />
      <path d={line((point) => point.scope_points)} fill="none" stroke="var(--muted)" strokeWidth="1.5" strokeDasharray="4 4" />
      <path d={line((point) => point.remaining_points)} fill="none" stroke="#2563eb" strokeWidth="2" />
      <text x={padding} y={padding - 8} fontSize="11" fill="var(--muted)">
        范围 {max} 点上限
      </text>
      <text x={width - padding} y={height - 8} fontSize="11" fill="var(--muted)" textAnchor="end">
        {points[points.length - 1].date}
      </text>
    </svg>
  );
}

export default async function SprintsPage({
  searchParams,
}: {
  searchParams: Promise<{
    product_id?: string;
    sprint_id?: string;
    error?: string;
    created?: string;
    done?: string;
  }>;
}) {
  const params = await searchParams;
  const productsResult = await getJSON<{ items: Product[] }>("/api/products?limit=50");
  const products = productsResult.data?.items ?? [];

  let productID = params.product_id ?? "";
  if (!productID) {
    productID = products[0]?.id ?? "";
  }
  const sprintsResult = productID
    ? await getJSON<{ items: Sprint[] }>(`/api/sprints?product_id=${productID}`)
    : {};
  const sprints = sprintsResult.data?.items ?? [];

  const selectedID = params.sprint_id ?? "";
  const selected = sprints.find((sprint) => sprint.id === selectedID);
  const burndown = selectedID
    ? await getJSON<{ points: BurndownPoint[] }>(`/api/sprints/${selectedID}/burndown`)
    : {};

  const returnParams = new URLSearchParams({
    ...(productID ? { product_id: productID } : {}),
    ...(selectedID ? { sprint_id: selectedID } : {}),
  }).toString();

  return (
    <main>
      <header className="page">
        <h1>冲刺</h1>
        <Nav />
      </header>

      {params.error ? <p className="error">操作失败：{params.error}</p> : null}
      {params.created ? <p className="ok">已创建冲刺「{params.created}」。</p> : null}
      {params.done ? <p className="ok">{params.done}。</p> : null}

      <form className="filters" method="get">
        <select name="product_id" defaultValue={productID}>
          {products.map((product) => (
            <option key={product.id} value={product.id}>
              {product.key} · {product.name}
            </option>
          ))}
        </select>
        <button type="submit">切换</button>
      </form>

      <details className="card create-work-item" open={sprints.length === 0}>
        <summary>新建冲刺</summary>
        {products.length === 0 ? (
          <p className="muted">没有可用的产品。</p>
        ) : (
          <form className="create-form" action="/api/sprints" method="post">
            <input type="hidden" name="product_id" value={productID} />
            <label>
              名称
              <input name="name" placeholder="Sprint 12" required size={14} />
            </label>
            <label>
              目标
              <input name="goal" placeholder="这一轮要达成什么" size={24} />
            </label>
            <label>
              开始
              <input name="start_at" type="date" />
            </label>
            <label>
              结束
              <input name="end_at" type="date" />
            </label>
            <label>
              容量（点）
              <input name="capacity_points" type="number" min="1" step="1" placeholder="20" size={6} />
            </label>
            <button type="submit">创建</button>
          </form>
        )}
      </details>

      {sprintsResult.error ? <p className="error">读取冲刺失败：{sprintsResult.error}</p> : null}

      <div className="card">
        {sprints.length === 0 ? (
          <div className="empty">还没有冲刺。用上面的表单建一个，再把工作项拉进来。</div>
        ) : (
          <table>
            <thead>
              <tr>
                <th>名称</th>
                <th>状态</th>
                <th>起止</th>
                <th>承诺 / 新增 / 移除</th>
                <th>蔓延率</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {sprints.map((sprint) => (
                <tr key={sprint.id}>
                  <td>
                    <Link href={`/sprints?product_id=${productID}&sprint_id=${sprint.id}`}>
                      {sprint.name}
                    </Link>
                  </td>
                  <td>
                    <span className={`pill sprint-${sprint.state.toLowerCase()}`}>
                      {STATE_LABELS[sprint.state] ?? sprint.state}
                    </span>
                  </td>
                  <td className="muted">
                    {sprint.start_at || "—"} ~ {sprint.end_at || "—"}
                  </td>
                  <td className="muted">
                    {sprint.stats
                      ? `${sprint.stats.committed_points} / +${sprint.stats.added_points} / -${sprint.stats.removed_points}`
                      : "—"}
                  </td>
                  <td className="muted">
                    {sprint.stats ? `${(sprint.stats.creep_rate * 100).toFixed(0)}%` : "—"}
                  </td>
                  <td>
                    <form className="transition-form" action={`/api/sprints/${sprint.id}`} method="post">
                      <input type="hidden" name="product_id" value={productID} />
                      <input type="hidden" name="expected_version" value={sprint.version} />
                      {sprint.state === "PLANNED" ? (
                        <button type="submit" name="action" value="start">
                          开始
                        </button>
                      ) : null}
                      {sprint.state === "ACTIVE" ? (
                        <button type="submit" name="action" value="close">
                          结束
                        </button>
                      ) : null}
                      <details className="row-edit">
                        <summary>范围</summary>
                        <div className="edit-form">
                          <input name="item_keys" placeholder="HOSP-3, HOSP-5" size={18} />
                          <button type="submit" name="action" value="add-items">
                            加入
                          </button>
                          <button type="submit" name="action" value="remove-items">
                            移出
                          </button>
                        </div>
                      </details>
                    </form>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {selected ? (
        <>
          <h2>
            燃尽 · {selected.name}
            {selected.stats ? (
              <span className="muted">
                {" "}
                · 剩余 {selected.stats.remaining_points} / 范围 {selected.stats.scope_points} 点
              </span>
            ) : null}
          </h2>
          <div className="card">
            {burndown.error ? (
              <p className="error">读取燃尽失败：{burndown.error}</p>
            ) : (
              <BurndownChart points={burndown.data?.points ?? []} />
            )}
          </div>
        </>
      ) : null}

      <footer className="page">
        <span>冲刺范围变更会记入蔓延率：(新增 − 移除) / 承诺</span>
        <span>{`共 ${sprints.length} 个冲刺`}</span>
      </footer>
    </main>
  );
}
