import { Nav } from "./nav";

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

export default async function Page({
  searchParams,
}: {
  searchParams: Promise<{ error?: string; created?: string }>;
}) {
  const params = await searchParams;
  const { data, error } = await loadProducts();
  const items = data?.items ?? [];

  return (
    <main>
      <header className="page">
        <h1>产品</h1>
        <Nav />
      </header>

      {params.error ? <p className="error">操作失败：{params.error}</p> : null}
      {params.created ? <p className="ok">已创建产品「{params.created}」。</p> : null}

      {error ? (
        <p className="error">
          读取产品失败：{error}
          <br />
          请确认后端已启动（<code>make run</code>，HTTP 8291）。
        </p>
      ) : null}

      <details className="card create-work-item" open={items.length === 0}>
          <summary>新建产品</summary>
          <form className="create-form" action="/api/products" method="post">
            <label>
              Key
              <input name="key" placeholder="PAY" required size={8} />
            </label>
            <label>
              名称
              <input name="name" placeholder="支付平台" required size={16} />
            </label>
            <label>
              描述
              <input name="description" placeholder="一句话说明这个产品做什么" size={28} />
            </label>
            <button type="submit">创建</button>
          </form>
        </details>

        <div className="card">
          {items.length === 0 ? (
            <div className="empty">还没有产品。用上面的表单建一个，再在版本页建第一个版本。</div>
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

      <footer className="page">
        <span>产品 · 版本与冲刺的起点</span>
        <span>{data ? `共 ${data.total} 个产品` : ""}</span>
      </footer>
    </main>
  );
}
