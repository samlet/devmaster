export const dynamic = "force-dynamic";

type Product = { id: string; key: string; name: string };

type CycleSummary = {
  type: string;
  p50_hours: number;
  p85_hours: number;
  p95_hours: number;
  count: number;
};

type ThroughputBucket = { Week: string; Type: string; Count: number };

type LoadRow = {
  Assignee: string;
  OpenItems: number;
  WIPItems: number;
  OpenPoints: number;
};

type CFDPoint = {
  Date: string;
  Todo: number;
  InProgress: number;
  InReview: number;
  Done: number;
};

const API_URL = process.env.DEVMASTER_API_URL ?? "http://127.0.0.1:8291";

const TYPE_LABELS: Record<string, string> = {
  EPIC: "史诗",
  STORY: "需求",
  TASK: "任务",
  BUG: "缺陷",
  SUBTASK: "子任务",
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

function shortDate(value: string) {
  return value.slice(5, 10);
}

export default async function ReportsPage({
  searchParams,
}: {
  searchParams: Promise<{ product_id?: string }>;
}) {
  const params = await searchParams;
  const productsResult = await getJSON<{ items: Product[] }>("/api/products");
  const products = productsResult.data?.items ?? [];
  // 没指定产品时优先选"有数据"的产品：默认第一个常常是空报表。
  let productID = params.product_id ?? "";
  if (!productID) {
    for (const product of products.slice(0, 10)) {
      const probe = await getJSON<{ items: LoadRow[] }>(`/api/reports/team-load?product_id=${product.id}`);
      if ((probe.data?.items ?? []).length > 0) {
        productID = product.id;
        break;
      }
    }
    if (!productID) {
      productID = products[0]?.id ?? "";
    }
  }

  const query = productID ? `product_id=${productID}` : "";
  const [cycle, throughput, load, cfd] = await Promise.all([
    getJSON<{ items: CycleSummary[] }>(`/api/reports/cycle-time?days=30&${query}`),
    getJSON<{ items: ThroughputBucket[] }>(`/api/reports/throughput?weeks=12&${query}`),
    getJSON<{ items: LoadRow[] }>(`/api/reports/team-load?${query}`),
    getJSON<{ items: CFDPoint[] }>(`/api/reports/cfd?days=14&${query}`),
  ]);

  const cycleItems = cycle.data?.items ?? [];
  const loadItems = load.data?.items ?? [];
  const cfdItems = cfd.data?.items ?? [];

  // 吞吐按周汇总（接口按 type 分行）。
  const weekMap = new Map<string, number>();
  for (const bucket of throughput.data?.items ?? []) {
    weekMap.set(bucket.Week, (weekMap.get(bucket.Week) ?? 0) + bucket.Count);
  }
  const weeks = [...weekMap.entries()].sort((a, b) => a[0].localeCompare(b[0]));
  const maxWeek = Math.max(1, ...weeks.map(([, count]) => count));

  const error = cycle.error || throughput.error || load.error || cfd.error;

  return (
    <main className="reports-page">
      <header className="page">
        <h1>报表</h1>
        <span className="hint">
          <a href="/">产品</a> · <a href="/work-items">工作项</a> · <a href="/board">看板</a> ·{" "}
          <a href="/versions">版本</a> · <a href="/inbox">收件箱</a> · devmaster
        </span>
      </header>

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

      {error ? (
        <p className="error">读取报表失败：{error}</p>
      ) : (
        <div className="report-grid">
          <section className="card report-card">
            <header>
              <h2>周期时间</h2>
              <span className="muted">近 30 天完成 · 小时</span>
            </header>
            {cycleItems.length === 0 ? (
              <div className="empty">样本不足 5 个，暂不输出分位数。</div>
            ) : (
              <table>
                <thead>
                  <tr>
                    <th>类型</th>
                    <th>P50</th>
                    <th>P85</th>
                    <th>P95</th>
                    <th>样本</th>
                  </tr>
                </thead>
                <tbody>
                  {cycleItems.map((item) => (
                    <tr key={item.type}>
                      <td>{TYPE_LABELS[item.type] ?? item.type}</td>
                      <td>{item.p50_hours}</td>
                      <td>{item.p85_hours}</td>
                      <td>{item.p95_hours}</td>
                      <td className="muted">{item.count}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </section>

          <section className="card report-card">
            <header>
              <h2>吞吐</h2>
              <span className="muted">近 12 周 · 完成数（不看点数）</span>
            </header>
            {weeks.length === 0 ? (
              <div className="empty">这段时间没有完成的工作项。</div>
            ) : (
              <div className="bars">
                {weeks.map(([week, count]) => (
                  <div className="bar-row" key={week}>
                    <span className="bar-label">{shortDate(week)}</span>
                    <span className="bar-track">
                      <span className="bar-fill" style={{ width: `${(count / maxWeek) * 100}%` }} />
                    </span>
                    <span className="bar-value">{count}</span>
                  </div>
                ))}
              </div>
            )}
          </section>

          <section className="card report-card">
            <header>
              <h2>团队负载</h2>
              <span className="muted">未完成 / 在研 / 点数（负载≠绩效）</span>
            </header>
            {loadItems.length === 0 ? (
              <div className="empty">没有数据。</div>
            ) : (
              <table>
                <thead>
                  <tr>
                    <th>经办人</th>
                    <th>未完成</th>
                    <th>在研</th>
                    <th>未完成点数</th>
                  </tr>
                </thead>
                <tbody>
                  {loadItems.map((row) => (
                    <tr key={row.Assignee}>
                      <td className="key">{row.Assignee === "UNASSIGNED" ? "未指派" : row.Assignee}</td>
                      <td>{row.OpenItems}</td>
                      <td>{row.WIPItems}</td>
                      <td className="muted">{row.OpenPoints}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </section>

          <section className="card report-card report-cfd">
            <header>
              <h2>累积流图</h2>
              <span className="muted">近 14 天 · 每日各列存量</span>
            </header>
            {cfdItems.length === 0 ? (
              <div className="empty">没有数据。</div>
            ) : (
              <div className="cfd">
                {cfdItems.map((point) => {
                  const total = Math.max(1, point.Todo + point.InProgress + point.InReview + point.Done);
                  return (
                    <div className="cfd-row" key={point.Date}>
                      <span className="bar-label">{shortDate(point.Date)}</span>
                      <span className="cfd-track">
                        <span
                          className="cfd-seg todo"
                          style={{ width: `${(point.Todo / total) * 100}%` }}
                          title={`待办 ${point.Todo}`}
                        />
                        <span
                          className="cfd-seg progress"
                          style={{ width: `${(point.InProgress / total) * 100}%` }}
                          title={`进行中 ${point.InProgress}`}
                        />
                        <span
                          className="cfd-seg review"
                          style={{ width: `${(point.InReview / total) * 100}%` }}
                          title={`评审中 ${point.InReview}`}
                        />
                        <span
                          className="cfd-seg done"
                          style={{ width: `${(point.Done / total) * 100}%` }}
                          title={`完成 ${point.Done}`}
                        />
                      </span>
                      <span className="bar-value">{total}</span>
                    </div>
                  );
                })}
                <div className="legend">
                  <span><i className="dot todo" />待办</span>
                  <span><i className="dot progress" />进行中</span>
                  <span><i className="dot review" />评审中</span>
                  <span><i className="dot done" />完成</span>
                </div>
              </div>
            )}
          </section>
        </div>
      )}

      <footer className="page">
        <span>只读报表 · M6</span>
        <span>口径见 docs/05</span>
      </footer>
    </main>
  );
}
