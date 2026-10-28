export const dynamic = "force-dynamic";

type Product = { id: string; key: string; name: string };

type Version = {
  id: string;
  name: string;
  status: string;
  milestone_label: string;
  planned_date: string;
  released_at: string;
  scope_frozen: boolean;
};

const API_URL = process.env.DEVMASTER_API_URL ?? "http://127.0.0.1:8291";

const STATUS_LABELS: Record<string, string> = {
  PLANNED: "计划中",
  IN_PROGRESS: "开发中",
  RELEASED: "已发布",
  ARCHIVED: "已归档",
  CANCELLED: "已取消",
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

export default async function VersionsPage({
  searchParams,
}: {
  searchParams: Promise<{ product_id?: string; error?: string; submitted?: string }>;
}) {
  const params = await searchParams;
  const productsResult = await getJSON<{ items: Product[] }>("/api/products");
  const products = productsResult.data?.items ?? [];

  // 没指定产品时优先选"有版本"的产品：默认第一个产品常常是空的。
  let productID = params.product_id ?? "";
  let versionsResult: { data?: { items: Version[] }; error?: string } = {};
  if (!productID) {
    for (const product of products.slice(0, 10)) {
      const candidate = await getJSON<{ items: Version[] }>(`/api/versions?product_id=${product.id}`);
      if ((candidate.data?.items ?? []).length > 0) {
        productID = product.id;
        versionsResult = candidate;
        break;
      }
    }
    if (!productID) {
      productID = products[0]?.id ?? "";
      versionsResult = productID
        ? await getJSON<{ items: Version[] }>(`/api/versions?product_id=${productID}`)
        : versionsResult;
    }
  } else {
    versionsResult = await getJSON<{ items: Version[] }>(`/api/versions?product_id=${productID}`);
  }
  const versions = versionsResult.data?.items ?? [];
  // 已发布/已归档/已取消的版本不能再提交；其余状态都可以发起一次发布。
  const releasable = versions.filter(
    (version) => !["RELEASED", "ARCHIVED", "CANCELLED"].includes(version.status),
  );

  return (
    <main>
      <header className="page">
        <h1>版本</h1>
        <span className="hint">
          <a href="/">产品</a> · <a href="/work-items">工作项</a> · <a href="/board">看板</a> ·{" "}
          <a href="/inbox">收件箱</a> · <a href="/reports">报表</a> · devmaster
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

      {params.error ? <p className="error">提交失败：{params.error}</p> : null}
      {params.submitted ? <p className="ok">已提交发布，等待审批。审批通过后会自动打 tag、存证并发布。</p> : null}

      {releasable.length > 0 ? (
        <form className="card release-form" action="/api/releases" method="post">
          <input type="hidden" name="product_id" value={productID} />
          <label>
            版本
            <select name="version_id" required defaultValue={releasable[0].id}>
              {releasable.map((version) => (
                <option key={version.id} value={version.id}>
                  {version.name}
                </option>
              ))}
            </select>
          </label>
          <label>
            commit from
            <input name="commit_from" placeholder="可选，如 abc1234" />
          </label>
          <label>
            commit to
            <input name="commit_to" placeholder="必填，打 tag 的目标提交" required />
          </label>
          <button type="submit">提交发布</button>
        </form>
      ) : null}

      {versionsResult.error ? (
        <p className="error">读取版本失败：{versionsResult.error}</p>
      ) : (
        <div className="card">
          {versions.length === 0 ? (
            <div className="empty">还没有版本。用 VersionService 创建一个，再挂工作项并提交发布。</div>
          ) : (
            <table>
              <thead>
                <tr>
                  <th>版本</th>
                  <th>里程碑</th>
                  <th>状态</th>
                  <th>计划日期</th>
                  <th>发布时间</th>
                  <th>范围</th>
                </tr>
              </thead>
              <tbody>
                {versions.map((version) => (
                  <tr key={version.id}>
                    <td className="key">{version.name}</td>
                    <td className="muted">{version.milestone_label || "—"}</td>
                    <td>
                      <span className={`pill version-${version.status.toLowerCase()}`}>
                        {STATUS_LABELS[version.status] ?? version.status}
                      </span>
                    </td>
                    <td className="muted">{version.planned_date || "—"}</td>
                    <td className="muted">
                      {version.released_at ? new Date(version.released_at).toLocaleString("zh-CN") : "—"}
                    </td>
                    <td className="muted">{version.scope_frozen ? "已冻结" : "可调整"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}

      <footer className="page">
        <span>M3 版本与发布 · 提交后进入审批链</span>
        <span>{versionsResult.data ? `共 ${versions.length} 个版本` : ""}</span>
      </footer>
    </main>
  );
}
