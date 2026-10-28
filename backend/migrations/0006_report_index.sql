-- 0006_report_index.sql
-- 累积流图要取"每个工作项在窗口开始前最后一条有状态的迁移"。
-- 没有这个索引时，DISTINCT ON 会对全部事件排序；10 万项时 CFD 超基线。
CREATE INDEX work_item_events_org_item_time_idx
    ON work_item_events (organization_id, work_item_id, occurred_at DESC, id DESC)
    WHERE to_status IS NOT NULL;
