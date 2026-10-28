export const dynamic = "force-dynamic";

type Product = {
  id: string;
  key: string;
  name: string;
  description: string;
  status: "ACTIVE" | "ARCHIVED";
  created_at: string;
  updated_at: string;
};

type ProductList = {
  items: Product[];
  total: number;
};

const API_URL = process.env.DEVMASTER_API_URL ?? "http://127.0.0.1:8291";

async function loadProducts(): Promise<{ data?: ProductList; error?: string }> {
  try {
    const response = await fetch(`${API_URL}/api/products`, { cache: "no-store" });
    if (!response.ok) {
      return { error: `后端返回 HTTP ${response.status}` };
    }
    return { data: (await response.json()) as ProductList };
  } catch (error) {
    return { error: error instanceof Error ? error.message : "无法连接后端" };
  }
}

export default async function Page() {
  const { data, error } = await loadProducts();
  const items = data?.items ?? [];

  return (
    <main>
      <header className="page">
        <h1>产品</h1>
        <span className="hint">
          <a href="/work-items">工作项</a> · <a href="/board">看板</a> · <a href="/versions">版本</a> · <a href="/inbox">收件箱</a> · <a href="/reports">报表</a> · devmaster
        </span>
      </header>

      {error ? (
        <p className="error">
          读取产品失败：{error}
          <br />
          请确认后端已启动（<code>make run</code>，HTTP 8291）。
        </p>
      ) : (
        <div className="card">
          {items.length === 0 ? (
            <div className="empty">还没有产品。用 gRPC 创建一个，或导入走查数据包。</div>
          ) : (
            <table>
              <thead>
                <tr>
                  <th>Key</th>
                  <th>名称</th>
                  <th>描述</th>
                  <th>状态</th>
                  <th>更新时间</th>
                </tr>
              </thead>
              <tbody>
                {items.map((product) => (
                  <tr key={product.id}>
                    <td className="key">{product.key}</td>
                    <td>{product.name}</td>
                    <td className="muted">{product.description || "—"}</td>
                    <td>
                      <span className={`pill ${product.status === "ACTIVE" ? "active" : "archived"}`}>
                        {product.status === "ACTIVE" ? "启用" : "归档"}
                      </span>
                    </td>
                    <td className="muted">{new Date(product.updated_at).toLocaleString("zh-CN")}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}

      <footer className="page">
        <span>只读控制台 · M0 骨架</span>
        <span>{data ? `共 ${data.total} 个产品` : ""}</span>
      </footer>
    </main>
  );
}
