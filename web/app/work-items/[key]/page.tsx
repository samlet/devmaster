import Link from "next/link";

import { Nav } from "../../nav";
import {
  EVENT_LABELS,
  LINK_TYPE_LABELS,
  PRIORITY_VALUES,
  RESOLUTION_LABELS,
  STATUS_LABELS,
  TYPE_LABELS,
  label,
} from "@/lib/labels";

export const dynamic = "force-dynamic";

const API_URL = process.env.DEVMASTER_API_URL ?? "http://127.0.0.1:8291";

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
  next_statuses: string[];
};

type Comment = { id: string; author_id: string; body_md: string; created_at: string };
type LinkEntry = { type: string; other_key: string; other_title: string; other_status: string };
type EventEntry = {
  event_type: string;
  actor_id: string;
  occurred_at: string;
  from_status?: string;
  to_status?: string;
};

type Detail = {
  item: WorkItem;
  comments: Comment[];
  links: { outgoing: LinkEntry[]; incoming: LinkEntry[] };
  events: EventEntry[];
};

async function loadDetail(key: string): Promise<{ data?: Detail; error?: string }> {
  try {
    const response = await fetch(`${API_URL}/api/work-items/${encodeURIComponent(key)}`, {
      cache: "no-store",
    });
    if (response.status === 404) {
      return { error: `找不到工作项 ${key}` };
    }
    if (!response.ok) {
      return { error: `后端返回 HTTP ${response.status}` };
    }
    return { data: (await response.json()) as Detail };
  } catch (error) {
    return { error: error instanceof Error ? error.message : "无法连接后端" };
  }
}

export default async function WorkItemDetailPage({
  params,
  searchParams,
}: {
  params: Promise<{ key: string }>;
  searchParams: Promise<{ error?: string; commented?: string; linked?: string }>;
}) {
  const { key } = await params;
  const query = await searchParams;
  const { data, error } = await loadDetail(key);
  const returnTo = `/work-items/${encodeURIComponent(key)}`;

  if (error || !data) {
    return (
      <main>
        <header className="page">
          <h1>工作项</h1>
          <Nav />
        </header>
        <p className="error">{error ?? "读取失败"}</p>
        <p className="hint">
          <Link href="/work-items">返回工作项列表</Link>
        </p>
      </main>
    );
  }

  const { item, comments, links, events } = data;

  return (
    <main>
      <header className="page">
        <h1>
          <span className="key">{item.key}</span> {item.title}
        </h1>
        <Nav />
      </header>

      <p className="hint">
        <Link href="/work-items">← 工作项列表</Link>
      </p>

      {query.error ? <p className="error">操作失败：{query.error}</p> : null}
      {query.commented ? <p className="ok">评论已提交。</p> : null}
      {query.linked ? <p className="ok">关联已更新。</p> : null}

      <div className="card detail-card">
        <div className="detail-head">
          <span className={`pill status-${item.status.toLowerCase()}`}>
            {label(STATUS_LABELS, item.status)}
          </span>
          <span className="pill">{label(TYPE_LABELS, item.type)}</span>
          <span className="muted">{item.priority}</span>
          {item.resolution ? (
            <span className="muted">resolution：{label(RESOLUTION_LABELS, item.resolution)}</span>
          ) : null}
        </div>
        <dl className="detail-meta">
          <div>
            <dt>经办人</dt>
            <dd>{item.assignee_id || "—"}</dd>
          </div>
          <div>
            <dt>报告人</dt>
            <dd>{item.reporter_id || "—"}</dd>
          </div>
          <div>
            <dt>更新时间</dt>
            <dd>{new Date(item.updated_at).toLocaleString("zh-CN")}</dd>
          </div>
          <div>
            <dt>版本号</dt>
            <dd>{item.version}</dd>
          </div>
        </dl>
      </div>

      <details className="card create-work-item">
        <summary>编辑</summary>
        <form className="edit-form" action={`/api/work-items/${item.key}`} method="post">
          <input type="hidden" name="return_to" value={returnTo} />
          <input type="hidden" name="expected_version" value={item.version} />
          <input name="title" defaultValue={item.title} size={28} required />
          <select name="priority" defaultValue={String(PRIORITY_VALUES[item.priority] ?? 2)}>
            <option value="0">P0</option>
            <option value="1">P1</option>
            <option value="2">P2</option>
            <option value="3">P3</option>
          </select>
          <input name="assignee_id" defaultValue={item.assignee_id} placeholder="经办人" size={12} />
          <button type="submit">保存</button>
        </form>
      </details>

      {item.next_statuses.length > 0 ? (
        <div className="card card-pad">
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
                  {label(STATUS_LABELS, next)}
                </option>
              ))}
            </select>
            {item.type === "BUG" ? (
              <select name="resolution" defaultValue="">
                <option value="">resolution</option>
                {Object.entries(RESOLUTION_LABELS).map(([value, text]) => (
                  <option key={value} value={value}>
                    {text}
                  </option>
                ))}
              </select>
            ) : null}
            <input name="comment" placeholder="备注" size={16} />
            <button type="submit">流转</button>
          </form>
        </div>
      ) : null}

      <h2>评论</h2>
      <div className="card card-pad">
        {comments.length === 0 ? (
          <div className="empty">还没有评论。第一条评论往往是最有用的上下文。</div>
        ) : (
          <ul className="comment-list">
            {comments.map((comment) => (
              <li key={comment.id}>
                <div className="comment-head">
                  <strong>{comment.author_id}</strong>
                  <span className="muted">{new Date(comment.created_at).toLocaleString("zh-CN")}</span>
                </div>
                <p className="comment-body">{comment.body_md}</p>
              </li>
            ))}
          </ul>
        )}
        <form className="comment-form" action={`/api/work-items/${item.key}/comments`} method="post">
          <input type="hidden" name="return_to" value={returnTo} />
          <textarea name="body_md" rows={3} placeholder="写点什么…" required />
          <button type="submit">提交评论</button>
        </form>
      </div>

      <h2>关联</h2>
      <div className="card card-pad">
        <div className="link-groups">
          <div>
            <h3>出向</h3>
            {links.outgoing.length === 0 ? (
              <p className="muted">—</p>
            ) : (
              <ul className="link-list">
                {links.outgoing.map((entry) => (
                  <li key={`${entry.type}-${entry.other_key}`}>
                    <span className="muted">{label(LINK_TYPE_LABELS, entry.type)}</span>{" "}
                    <Link href={`/work-items/${entry.other_key}`}>{entry.other_key}</Link>{" "}
                    <span>{entry.other_title}</span>
                  </li>
                ))}
              </ul>
            )}
          </div>
          <div>
            <h3>入向</h3>
            {links.incoming.length === 0 ? (
              <p className="muted">—</p>
            ) : (
              <ul className="link-list">
                {links.incoming.map((entry) => (
                  <li key={`${entry.type}-${entry.other_key}`}>
                    <Link href={`/work-items/${entry.other_key}`}>{entry.other_key}</Link>{" "}
                    <span className="muted">{label(LINK_TYPE_LABELS, entry.type)}</span>{" "}
                    <span>{entry.other_title}</span>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
        <form className="link-form" action={`/api/work-items/${item.key}/links`} method="post">
          <input type="hidden" name="return_to" value={returnTo} />
          <select name="type" defaultValue="RELATES">
            <option value="BLOCKS">阻塞</option>
            <option value="BLOCKED_BY">被阻塞</option>
            <option value="RELATES">相关</option>
            <option value="DUPLICATES">重复于</option>
            <option value="CAUSES">导致</option>
          </select>
          <input name="target_key" placeholder="目标 key，如 HOSP-5" required size={16} />
          <button type="submit">添加关联</button>
        </form>
      </div>

      <h2>事件历史</h2>
      <div className="card">
        {events.length === 0 ? (
          <div className="empty">没有事件记录。</div>
        ) : (
          <table>
            <thead>
              <tr>
                <th>时间</th>
                <th>事件</th>
                <th>操作人</th>
                <th>变化</th>
              </tr>
            </thead>
            <tbody>
              {events.map((event, index) => (
                <tr key={`${event.event_type}-${event.occurred_at}-${index}`}>
                  <td className="muted">{new Date(event.occurred_at).toLocaleString("zh-CN")}</td>
                  <td>{label(EVENT_LABELS, event.event_type)}</td>
                  <td className="muted">{event.actor_id}</td>
                  <td className="muted">
                    {event.from_status && event.to_status
                      ? `${label(STATUS_LABELS, event.from_status)} → ${label(STATUS_LABELS, event.to_status)}`
                      : "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <footer className="page">
        <span>工作项详情 · 评论与事件不可编辑（append-only）</span>
        <span>{`${comments.length} 条评论 · ${events.length} 条事件`}</span>
      </footer>
    </main>
  );
}
