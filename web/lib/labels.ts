// 控制台共用的标签映射。
//
// 抽出来是因为列表页、详情页、看板都要用同一套中文标签——各写一份迟早出现
// 「同一个状态在三个页面叫三个名字」。

export const STATUS_LABELS: Record<string, string> = {
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

export const TYPE_LABELS: Record<string, string> = {
  EPIC: "史诗",
  STORY: "需求",
  TASK: "任务",
  SUBTASK: "子任务",
  BUG: "缺陷",
};

export const RESOLUTION_LABELS: Record<string, string> = {
  FIXED: "已修复",
  WONTFIX: "不修复",
  DUPLICATE: "重复",
  CANNOT_REPRODUCE: "无法复现",
  BY_DESIGN: "设计如此",
  OBSOLETE: "已过时",
};

export const PRIORITY_VALUES: Record<string, number> = { P0: 0, P1: 1, P2: 2, P3: 3 };

export const LINK_TYPE_LABELS: Record<string, string> = {
  BLOCKS: "阻塞",
  BLOCKED_BY: "被阻塞",
  RELATES: "相关",
  DUPLICATES: "重复于",
  CAUSES: "导致",
};

export const EVENT_LABELS: Record<string, string> = {
  created: "创建",
  updated: "更新",
  transitioned: "状态变更",
  commented: "评论",
  linked: "建立关联",
  unlinked: "移除关联",
  assigned: "指派",
};

export function label(map: Record<string, string>, value: string): string {
  return map[value] ?? value;
}
