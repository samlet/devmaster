"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";

// 看板拖拽。这是控制台里唯一需要客户端组件的页面——
// 其余页面都是表单 + 303 回跳，不需要 JS。
//
// 用原生 HTML5 drag & drop，不引 dnd 库：拖到另一列的语义只是「改状态」，
// 不需要排序、嵌套、触摸手势这些库才擅长的事。

type BoardItem = {
  id: string;
  key: string;
  title: string;
  type: string;
  status: string;
  priority: string;
  assignee_id: string;
  story_points: number;
  version: number;
};

type BoardColumn = {
  column: string;
  statuses: string[];
  work_items: BoardItem[];
};

const TYPE_LABELS: Record<string, string> = {
  EPIC: "史诗",
  STORY: "需求",
  TASK: "任务",
  SUBTASK: "子任务",
  BUG: "缺陷",
};

export function DraggableBoard({ columns }: { columns: BoardColumn[] }) {
  const router = useRouter();
  const [dragging, setDragging] = useState<BoardItem | null>(null);
  const [hovered, setHovered] = useState<string>("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function drop(targetStatus: string) {
    if (!dragging || busy) {
      return;
    }
    if (dragging.status === targetStatus) {
      setDragging(null);
      setHovered("");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const response = await fetch("/api/board/move", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          work_item_key: dragging.key,
          to: targetStatus,
          expected_version: dragging.version,
        }),
      });
      if (!response.ok) {
        setError((await response.text()).trim() || `后端返回 HTTP ${response.status}`);
      } else {
        router.refresh();
      }
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "无法连接后端");
    } finally {
      setBusy(false);
      setDragging(null);
      setHovered("");
    }
  }

  return (
    <div>
      {error ? <p className="error">移动失败：{error}</p> : null}
      {busy ? <p className="hint">正在移动…</p> : null}
      <div className="board">
        {columns.map((column) => {
          // 列可能对应多个状态（如「待办」含 BACKLOG/TODO），拖进来落到第一个。
          const targetStatus = column.statuses[0] ?? "";
          return (
            <section
              key={column.column}
              className={`board-column ${hovered === column.column ? "board-column-hover" : ""}`}
              onDragOver={(event) => {
                event.preventDefault();
                setHovered(column.column);
              }}
              onDragLeave={() => setHovered("")}
              onDrop={(event) => {
                event.preventDefault();
                void drop(targetStatus);
              }}
            >
              <h3>
                {column.column}
                <span className="muted"> {column.work_items.length}</span>
              </h3>
              {column.work_items.length === 0 ? (
                <p className="board-empty">拖到这里</p>
              ) : (
                column.work_items.map((item) => (
                  <article
                    key={item.id}
                    className={`board-card ${dragging?.id === item.id ? "board-card-dragging" : ""}`}
                    draggable
                    onDragStart={() => setDragging(item)}
                    onDragEnd={() => {
                      setDragging(null);
                      setHovered("");
                    }}
                  >
                    <div className="card-top">
                      <Link href={`/work-items/${item.key}`}>{item.key}</Link>
                      <span className="muted">{TYPE_LABELS[item.type] ?? item.type}</span>
                    </div>
                    <p className="card-title">{item.title}</p>
                    <div className="card-bottom">
                      <span className="muted">{item.assignee_id || "未指派"}</span>
                      <span className="muted">{item.priority}</span>
                    </div>
                  </article>
                ))
              )}
            </section>
          );
        })}
      </div>
    </div>
  );
}
