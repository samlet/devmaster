export const dynamic = "force-dynamic";

type Product = { id: string; key: string; name: string };

type BoardItem = {
  id: string;
  key: string;
  title: string;
  type: string;
  status: string;
  priority: string;
  assignee_id: string;
  story_points: number;
  rank: string;
  version: number;
};

type BoardColumn = {
  column: string;
  statuses: string[];
  work_items: BoardItem[];
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
};

type Board = {
  sprint?: Sprint;
  columns: BoardColumn[];
};

const API_URL = process.env.DEVMASTER_API_URL ?? "http://127.0.0.1:8291";

const COLUMN_LABELS: Record<string, string> = {
  TODO: "待办",
  IN_PROGRESS: "进行中",
  IN_REVIEW: "评审中",
  DONE: "完成",
};

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

export default async function BoardPage({
  searchParams,
}: {
  searchParams: Promise<{ product_id?: string }>;
}) {
  const params = await searchParams;
  const productsResult = await getJSON<{ items: Product[] }>("/api/products");
  const products = productsResult.data?.items ?? [];

  // 没有显式指定产品时，优先选"有进行中冲刺"的产品：默认落到第一个产品
  // 常常是一个空看板，看起来像坏了。
  let productID = params.product_id ?? "";
  let boardResult = productID
    ? await getJSON<Board>(`/api/board?product_id=${productID}`)
    : { data: undefined as Board | undefined, error: undefined as string | undefined };
  if (!params.product_id) {
    for (const product of products.slice(0, 10)) {
      const candidate = await getJSON<Board>(`/api/board?product_id=${product.id}`);
      if (candidate.data?.sprint) {
        productID = product.id;
        boardResult = candidate;
        break;
      }
    }
    if (!productID) {
      productID = products[0]?.id ?? "";
      boardResult = productID
        ? await getJSON<Board>(`/api/board?product_id=${productID}`)
        : boardResult;
    }
  }

  if (!productID) {
    return (
      <main>
        <header className="page">
          <h1>看板</h1>
        </header>
        <p className="error">还没有产品。先创建一个产品，再开冲刺。</p>
      </main>
    );
  }

  const board = boardResult.data;
  const sprint = board?.sprint;
  const columns = board?.columns ?? [];
  const totalPoints = columns
    .flatMap((column) => column.work_items)
    .reduce((sum, item) => sum + item.story_points, 0);

  return (
    <main className="board-page">
      <header className="page">
        <h1>{sprint ? sprint.name : "看板"}</h1>
        <span className="hint">
          <a href="/">产品</a> · <a href="/work-items">工作项</a> · <a href="/versions">版本</a> · <a href="/inbox">收件箱</a> · <a href="/reports">报表</a> · devmaster
        </span>
      </header>

      <div className="board-meta">
        <form method="get" className="product-switch">
          <select name="product_id" defaultValue={productID}>
            {products.map((product) => (
              <option key={product.id} value={product.id}>
                {product.key} · {product.name}
              </option>
            ))}
          </select>
          <button type="submit">切换</button>
        </form>
        {sprint ? (
          <span className="muted">
            {sprint.state === "ACTIVE" ? "进行中" : sprint.state === "PLANNED" ? "未开始" : "已关闭"}
            {sprint.start_at ? ` · ${sprint.start_at.slice(0, 10)} → ${sprint.end_at.slice(0, 10)}` : ""}
            {` · 承诺 ${sprint.committed_points} 点 · 当前 ${totalPoints} 点`}
          </span>
        ) : (
          <span className="muted">没有进行中的冲刺（用 SprintService 创建一个并启动）</span>
        )}
      </div>

      {boardResult.error ? (
        <p className="error">读取看板失败：{boardResult.error}</p>
      ) : (
        <div className="board">
          {columns.map((column) => (
            <section key={column.column} className="board-column">
              <header>
                <span>{COLUMN_LABELS[column.column] ?? column.column}</span>
                <span className="count">{column.work_items.length}</span>
              </header>
              <div className="board-cards">
                {column.work_items.map((item) => (
                  <article key={item.id} className="board-card">
                    <div className="card-top">
                      <span className="key">{item.key}</span>
                      <span className="muted">{TYPE_LABELS[item.type] ?? item.type}</span>
                    </div>
                    <p>{item.title}</p>
                    <div className="card-bottom">
                      <span className={`pill status-${item.status.toLowerCase()}`}>{item.status}</span>
                      <span className="muted">
                        {item.story_points > 0 ? `${item.story_points} 点` : "未估点"}
                        {item.assignee_id ? ` · ${item.assignee_id}` : ""}
                      </span>
                    </div>
                  </article>
                ))}
                {column.work_items.length === 0 ? <div className="board-empty">空</div> : null}
              </div>
            </section>
          ))}
        </div>
      )}

      <footer className="page">
        <span>只读看板 · M2 冲刺</span>
        <span>{sprint ? `版本 v${sprint.version}` : ""}</span>
      </footer>
    </main>
  );
}
