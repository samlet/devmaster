-- 0007_event_product.sql
-- 报表按产品过滤时不必再 join work_items：事件表直接带 product_id。
--
-- 用 BEFORE INSERT 触发器统一填值，而不是改所有写入点：工时/评论/链接/
-- 冲刺结转/导入器都写事件，漏一处就会产生 NULL，报表就会静默少数据。
ALTER TABLE work_item_events ADD COLUMN product_id uuid;

-- 回填要跨组织：临时关掉 work_items 的 RLS，否则 owner 在 FORCE RLS 下
-- 也只能看到当前组织（app.organization_id 未设置时一行都看不到）。
ALTER TABLE work_items DISABLE ROW LEVEL SECURITY;
ALTER TABLE work_item_events DISABLE ROW LEVEL SECURITY;
ALTER TABLE work_item_events DISABLE TRIGGER work_item_events_no_update;
UPDATE work_item_events e
   SET product_id = w.product_id
  FROM work_items w
 WHERE e.work_item_id = w.id AND e.product_id IS NULL;
ALTER TABLE work_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE work_item_events ENABLE ROW LEVEL SECURITY;

ALTER TABLE work_item_events ALTER COLUMN product_id SET NOT NULL;

CREATE OR REPLACE FUNCTION work_item_events_fill_product() RETURNS trigger AS $$
BEGIN
    IF NEW.product_id IS NULL THEN
        SELECT product_id INTO NEW.product_id FROM work_items WHERE id = NEW.work_item_id;
    END IF;
    IF NEW.product_id IS NULL THEN
        RAISE EXCEPTION 'work_item_events: cannot resolve product for work item %', NEW.work_item_id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER work_item_events_fill_product
    BEFORE INSERT ON work_item_events
    FOR EACH ROW EXECUTE FUNCTION work_item_events_fill_product();

-- 报表两条主查询的索引：按产品 + 时间；完成态再收窄。
CREATE INDEX work_item_events_org_product_time_idx
    ON work_item_events (organization_id, product_id, occurred_at DESC);
CREATE INDEX work_item_events_org_product_done_idx
    ON work_item_events (organization_id, product_id, to_status, occurred_at)
    WHERE to_status IN ('DONE', 'CLOSED', 'VERIFIED', 'WONTFIX');
